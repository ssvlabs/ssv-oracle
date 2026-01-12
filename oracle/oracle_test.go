package oracle

import (
	"context"
	"testing"

	"github.com/attestantio/go-eth2-client/spec/phase0"

	"github.com/ssvlabs/ssv-oracle/storage"
)

// mockStorage implements the oracleStorage interface for testing.
type mockStorage struct {
	validators      []storage.ActiveValidator
	commits         map[uint64]*storage.OracleCommit
	insertErr       error
	updateErr       error
	insertedCommits []storage.OracleCommit
	updatedStatuses []commitStatusUpdate
}

type commitStatusUpdate struct {
	targetEpoch uint64
	status      storage.CommitStatus
	txHash      []byte
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		commits:         make(map[uint64]*storage.OracleCommit),
		insertedCommits: []storage.OracleCommit{},
		updatedStatuses: []commitStatusUpdate{},
	}
}

// GetActiveValidators returns the stubbed validator set.
func (m *mockStorage) GetActiveValidators(ctx context.Context) ([]storage.ActiveValidator, error) {
	return m.validators, nil
}

// InsertPendingCommit records a pending commit for assertions.
func (m *mockStorage) InsertPendingCommit(ctx context.Context, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []storage.ClusterBalance) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	m.insertedCommits = append(m.insertedCommits, storage.OracleCommit{
		TargetEpoch:     targetEpoch,
		MerkleRoot:      merkleRoot,
		ReferenceBlock:  referenceBlock,
		ClusterBalances: clusterBalances,
	})
	return nil
}

// UpdateCommitStatus records a status update for assertions.
func (m *mockStorage) UpdateCommitStatus(ctx context.Context, targetEpoch uint64, status storage.CommitStatus, txHash []byte) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedStatuses = append(m.updatedStatuses, commitStatusUpdate{
		targetEpoch: targetEpoch,
		status:      status,
		txHash:      txHash,
	})
	return nil
}

func TestNew(t *testing.T) {
	schedule := CommitSchedule{{StartEpoch: 0, Interval: 225}}

	cfg := &Config{
		Storage:        nil, // Would be *storage.Storage
		ContractClient: nil, // Would be *contract.Client
		Schedule:       schedule,
	}

	o := New(cfg)
	if o == nil {
		t.Fatal("New() returned nil")
	}

	// Check schedule is set
	if len(o.schedule) != 1 {
		t.Errorf("Expected 1 phase, got %d", len(o.schedule))
	}
}

func TestOracleStorageInterface(t *testing.T) {
	// Verify mockStorage implements the oracleStorage interface
	var _ oracleStorage = (*mockStorage)(nil)
}

func TestAggregateByCluster_MultipleClusters(t *testing.T) {
	o := &Oracle{}

	// Create distinct pubkeys for each validator
	pk1, pk2, pk3 := make([]byte, 48), make([]byte, 48), make([]byte, 48)
	pk1[0], pk2[0], pk3[0] = 0x01, 0x02, 0x03

	cluster1, cluster2 := make([]byte, 32), make([]byte, 32)
	cluster1[0], cluster2[0] = 0x01, 0x02

	validators := []storage.ActiveValidator{
		{ClusterID: cluster1, ValidatorPubkey: pk1},
		{ClusterID: cluster1, ValidatorPubkey: pk2}, // Same cluster
		{ClusterID: cluster2, ValidatorPubkey: pk3},
	}

	// Build balance map: 32 ETH each
	balanceMap := make(map[phase0.BLSPubKey]uint64)
	var blsPk1, blsPk2, blsPk3 phase0.BLSPubKey
	copy(blsPk1[:], pk1)
	copy(blsPk2[:], pk2)
	copy(blsPk3[:], pk3)
	balanceMap[blsPk1] = 32_000_000_000
	balanceMap[blsPk2] = 32_000_000_000
	balanceMap[blsPk3] = 32_000_000_000

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	if notOnBeacon != 0 {
		t.Errorf("Expected 0 not on beacon, got %d", notOnBeacon)
	}
	if len(result) != 2 {
		t.Fatalf("Expected 2 clusters, got %d", len(result))
	}

	// Find cluster 0x01 and 0x02 in results
	var cluster1Balance, cluster2Balance uint32
	for _, r := range result {
		switch r.ClusterID[0] {
		case 0x01:
			cluster1Balance = r.EffectiveBalance
		case 0x02:
			cluster2Balance = r.EffectiveBalance
		}
	}

	if cluster1Balance != 64 {
		t.Errorf("Cluster 0x01: expected 64 ETH, got %d", cluster1Balance)
	}
	if cluster2Balance != 32 {
		t.Errorf("Cluster 0x02: expected 32 ETH, got %d", cluster2Balance)
	}
}

func TestDeduplicatePubkeys(t *testing.T) {
	o := &Oracle{}

	pk1, pk2 := make([]byte, 48), make([]byte, 48)
	pk1[0], pk2[0] = 0xAA, 0xBB

	validators := []storage.ActiveValidator{
		{ClusterID: []byte{0x01}, ValidatorPubkey: pk1},
		{ClusterID: []byte{0x01}, ValidatorPubkey: pk1}, // Duplicate pubkey
		{ClusterID: []byte{0x01}, ValidatorPubkey: pk2}, // Different pubkey
	}

	uniquePubkeys := o.deduplicatePubkeys(validators)

	if len(uniquePubkeys) != 2 {
		t.Errorf("Expected 2 unique pubkeys, got %d", len(uniquePubkeys))
	}
}

func TestEmptyValidators(t *testing.T) {
	// When there are no validators, cluster balances should be empty
	store := newMockStorage()
	store.validators = []storage.ActiveValidator{}

	validators, err := store.GetActiveValidators(context.Background())
	if err != nil {
		t.Fatalf("GetActiveValidators failed: %v", err)
	}

	if len(validators) != 0 {
		t.Errorf("Expected 0 validators, got %d", len(validators))
	}
}

func TestOracleSchedule(t *testing.T) {
	// Verify Oracle correctly stores schedule from config
	// Phases must be aligned: 900 = 0 + 4*225
	schedule := CommitSchedule{
		{StartEpoch: 0, Interval: 225},
		{StartEpoch: 900, Interval: 450},
	}

	cfg := &Config{
		Schedule: schedule,
	}

	o := New(cfg)

	if len(o.schedule) != 2 {
		t.Fatalf("Expected 2 phases, got %d", len(o.schedule))
	}

	if o.schedule[0].StartEpoch != 0 {
		t.Errorf("Phase 0 start epoch: expected 0, got %d", o.schedule[0].StartEpoch)
	}

	if o.schedule[0].Interval != 225 {
		t.Errorf("Phase 0 interval: expected 225, got %d", o.schedule[0].Interval)
	}

	if o.schedule[1].StartEpoch != 900 {
		t.Errorf("Phase 1 start epoch: expected 900, got %d", o.schedule[1].StartEpoch)
	}

	if o.schedule[1].Interval != 450 {
		t.Errorf("Phase 1 interval: expected 450, got %d", o.schedule[1].Interval)
	}
}

func TestAggregateByCluster_BalanceFloor(t *testing.T) {
	o := &Oracle{}

	// Create test pubkeys
	pk1 := make([]byte, 48)
	pk1[0] = 0x01
	pk2 := make([]byte, 48)
	pk2[0] = 0x02
	pk3 := make([]byte, 48)
	pk3[0] = 0x03
	pk4 := make([]byte, 48)
	pk4[0] = 0x04

	clusterID := make([]byte, 32)
	clusterID[0] = 0xAA

	validators := []storage.ActiveValidator{
		{ClusterID: clusterID, ValidatorPubkey: pk1},
		{ClusterID: clusterID, ValidatorPubkey: pk2},
		{ClusterID: clusterID, ValidatorPubkey: pk3},
		{ClusterID: clusterID, ValidatorPubkey: pk4},
	}

	// Build balance map with various scenarios
	balanceMap := make(map[phase0.BLSPubKey]uint64)

	var blsPk1, blsPk2, blsPk3 phase0.BLSPubKey
	copy(blsPk1[:], pk1)
	copy(blsPk2[:], pk2)
	copy(blsPk3[:], pk3)
	// pk4 not in map (not on beacon)

	balanceMap[blsPk1] = 32_000_000_000 // 32 ETH - normal
	balanceMap[blsPk2] = 64_000_000_000 // 64 ETH - above 32, use actual
	balanceMap[blsPk3] = 16_000_000_000 // 16 ETH - at ejection threshold, should become 32

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	if notOnBeacon != 1 {
		t.Errorf("Expected 1 validator not on beacon, got %d", notOnBeacon)
	}

	if len(result) != 1 {
		t.Fatalf("Expected 1 cluster, got %d", len(result))
	}

	// Expected: 32 (normal) + 64 (high) + 32 (floored from 16) + 32 (not on beacon) = 160 ETH
	expectedBalance := uint32(160)
	if result[0].EffectiveBalance != expectedBalance {
		t.Errorf("Expected cluster balance %d ETH, got %d ETH",
			expectedBalance, result[0].EffectiveBalance)
	}
}

func TestAggregateByCluster_AllBelowThreshold(t *testing.T) {
	o := &Oracle{}

	pk1 := make([]byte, 48)
	pk1[0] = 0x01
	pk2 := make([]byte, 48)
	pk2[0] = 0x02

	clusterID := make([]byte, 32)
	clusterID[0] = 0xBB

	validators := []storage.ActiveValidator{
		{ClusterID: clusterID, ValidatorPubkey: pk1},
		{ClusterID: clusterID, ValidatorPubkey: pk2},
	}

	balanceMap := make(map[phase0.BLSPubKey]uint64)

	var blsPk1, blsPk2 phase0.BLSPubKey
	copy(blsPk1[:], pk1)
	copy(blsPk2[:], pk2)

	balanceMap[blsPk1] = 0              // Exited with 0
	balanceMap[blsPk2] = 15_000_000_000 // Below threshold

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	if notOnBeacon != 0 {
		t.Errorf("Expected 0 validators not on beacon, got %d", notOnBeacon)
	}

	// Both should be floored to 32 ETH each = 64 ETH total
	expectedBalance := uint32(64)
	if result[0].EffectiveBalance != expectedBalance {
		t.Errorf("Expected cluster balance %d ETH, got %d ETH",
			expectedBalance, result[0].EffectiveBalance)
	}
}

func TestAggregateByCluster_NotOnBeacon(t *testing.T) {
	// Per spec: validators missing from beacon response (not yet activated, pending deposit)
	// are floored to 32 ETH to ensure clusters always have a valid balance.
	o := &Oracle{}

	pk1 := make([]byte, 48)
	pk1[0] = 0x01

	clusterID := make([]byte, 32)
	clusterID[0] = 0xCC

	validators := []storage.ActiveValidator{
		{ClusterID: clusterID, ValidatorPubkey: pk1},
	}

	// Empty balance map - validator not on beacon (e.g., pending activation)
	balanceMap := make(map[phase0.BLSPubKey]uint64)

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	if notOnBeacon != 1 {
		t.Errorf("Expected 1 validator not on beacon, got %d", notOnBeacon)
	}

	// Should default to balance floor (32 ETH) per spec
	expectedBalance := uint32(32)
	if result[0].EffectiveBalance != expectedBalance {
		t.Errorf("Expected cluster balance %d ETH, got %d ETH",
			expectedBalance, result[0].EffectiveBalance)
	}
}
