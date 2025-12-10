package oracle

import (
	"context"
	"testing"

	"github.com/attestantio/go-eth2-client/spec/phase0"

	"ssv-oracle/pkg/ethsync"
)

// mockStorage implements the storage interface for testing.
type mockStorage struct {
	validators      []ethsync.ActiveValidator
	commits         map[uint64]*ethsync.OracleCommit
	insertErr       error
	updateErr       error
	insertedCommits []ethsync.OracleCommit
	updatedStatuses []commitStatusUpdate
}

type commitStatusUpdate struct {
	roundID uint64
	status  ethsync.CommitStatus
	txHash  []byte
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		commits:         make(map[uint64]*ethsync.OracleCommit),
		insertedCommits: []ethsync.OracleCommit{},
		updatedStatuses: []commitStatusUpdate{},
	}
}

func (m *mockStorage) GetActiveValidators(ctx context.Context) ([]ethsync.ActiveValidator, error) {
	return m.validators, nil
}

func (m *mockStorage) InsertPendingCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []ethsync.ClusterBalance) error {
	if m.insertErr != nil {
		return m.insertErr
	}
	m.insertedCommits = append(m.insertedCommits, ethsync.OracleCommit{
		RoundID:         roundID,
		TargetEpoch:     targetEpoch,
		MerkleRoot:      merkleRoot,
		ReferenceBlock:  referenceBlock,
		ClusterBalances: clusterBalances,
	})
	return nil
}

func (m *mockStorage) UpdateCommitStatus(ctx context.Context, roundID uint64, status ethsync.CommitStatus, txHash []byte) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedStatuses = append(m.updatedStatuses, commitStatusUpdate{
		roundID: roundID,
		status:  status,
		txHash:  txHash,
	})
	return nil
}

func TestNew(t *testing.T) {
	storage := newMockStorage()
	phases := []CommitPhase{{StartEpoch: 0, Interval: 225}}

	cfg := &Config{
		Storage:        nil, // Would be *ethsync.Storage
		ContractClient: nil, // Would be *contract.Client
		Phases:         phases,
	}

	o := New(cfg)
	if o == nil {
		t.Fatal("New() returned nil")
	}

	// Check phases are set
	if len(o.phases) != 1 {
		t.Errorf("Expected 1 phase, got %d", len(o.phases))
	}

	// Use storage variable to avoid unused warning
	_ = storage
}

func TestNew_EmptyConfig(t *testing.T) {
	cfg := &Config{}

	o := New(cfg)
	if o == nil {
		t.Fatal("New() returned nil with empty config")
	}

	// Note: Go interfaces holding nil concrete pointers are not nil interfaces.
	// We just verify the oracle was created successfully with empty config.
	if len(o.phases) != 0 {
		t.Errorf("Expected 0 phases with empty config, got %d", len(o.phases))
	}
}

func TestOracleStorageInterface(t *testing.T) {
	// Verify mockStorage implements the storage interface
	var _ storage = (*mockStorage)(nil)
}

func TestClusterBalanceAggregation(t *testing.T) {
	// Test the logic for aggregating balances by cluster
	// This mirrors what fetchClusterBalances does internally

	validators := []ethsync.ActiveValidator{
		{ClusterID: []byte{0x01}, ValidatorPubkey: make([]byte, 48)},
		{ClusterID: []byte{0x01}, ValidatorPubkey: make([]byte, 48)}, // Same cluster
		{ClusterID: []byte{0x02}, ValidatorPubkey: make([]byte, 48)},
	}

	// Simulate balance fetching (32 ETH each in gwei)
	balancePerValidator := uint64(32000000000)

	// Aggregate by cluster
	clusterTotals := make(map[string]uint64)
	for _, v := range validators {
		clusterKey := string(v.ClusterID)
		clusterTotals[clusterKey] += balancePerValidator
	}

	// Cluster 0x01 should have 64 ETH (2 validators)
	if clusterTotals[string([]byte{0x01})] != 64000000000 {
		t.Errorf("Cluster 0x01 balance: expected 64 ETH, got %d gwei", clusterTotals[string([]byte{0x01})])
	}

	// Cluster 0x02 should have 32 ETH (1 validator)
	if clusterTotals[string([]byte{0x02})] != 32000000000 {
		t.Errorf("Cluster 0x02 balance: expected 32 ETH, got %d gwei", clusterTotals[string([]byte{0x02})])
	}
}

func TestPubkeyDeduplication(t *testing.T) {
	// Test that duplicate pubkeys are handled correctly
	// This mirrors the deduplication logic in fetchClusterBalances

	validators := []ethsync.ActiveValidator{
		{ClusterID: []byte{0x01}, ValidatorPubkey: []byte{0xAA}},
		{ClusterID: []byte{0x01}, ValidatorPubkey: []byte{0xAA}}, // Duplicate pubkey
		{ClusterID: []byte{0x01}, ValidatorPubkey: []byte{0xBB}}, // Different pubkey
	}

	// Deduplicate
	seen := make(map[string]struct{})
	var uniquePubkeys [][]byte
	for _, v := range validators {
		key := string(v.ValidatorPubkey)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			uniquePubkeys = append(uniquePubkeys, v.ValidatorPubkey)
		}
	}

	if len(uniquePubkeys) != 2 {
		t.Errorf("Expected 2 unique pubkeys, got %d", len(uniquePubkeys))
	}
}

func TestEmptyValidators(t *testing.T) {
	// When there are no validators, cluster balances should be empty
	storage := newMockStorage()
	storage.validators = []ethsync.ActiveValidator{}

	validators, err := storage.GetActiveValidators(context.Background())
	if err != nil {
		t.Fatalf("GetActiveValidators failed: %v", err)
	}

	if len(validators) != 0 {
		t.Errorf("Expected 0 validators, got %d", len(validators))
	}
}

func TestOraclePhases(t *testing.T) {
	// Verify Oracle correctly stores phases from config
	phases := []CommitPhase{
		{StartEpoch: 0, Interval: 225},
		{StartEpoch: 1000, Interval: 450},
	}

	cfg := &Config{
		Phases: phases,
	}

	o := New(cfg)

	if len(o.phases) != 2 {
		t.Fatalf("Expected 2 phases, got %d", len(o.phases))
	}

	if o.phases[0].StartEpoch != 0 {
		t.Errorf("Phase 0 start epoch: expected 0, got %d", o.phases[0].StartEpoch)
	}

	if o.phases[0].Interval != 225 {
		t.Errorf("Phase 0 interval: expected 225, got %d", o.phases[0].Interval)
	}

	if o.phases[1].StartEpoch != 1000 {
		t.Errorf("Phase 1 start epoch: expected 1000, got %d", o.phases[1].StartEpoch)
	}

	if o.phases[1].Interval != 450 {
		t.Errorf("Phase 1 interval: expected 450, got %d", o.phases[1].Interval)
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

	validators := []ethsync.ActiveValidator{
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
	expectedBalance := uint64(160_000_000_000)
	if result[0].EffectiveBalance != expectedBalance {
		t.Errorf("Expected cluster balance %d gwei (160 ETH), got %d gwei",
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

	validators := []ethsync.ActiveValidator{
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
	expectedBalance := uint64(64_000_000_000)
	if result[0].EffectiveBalance != expectedBalance {
		t.Errorf("Expected cluster balance %d gwei (64 ETH), got %d gwei",
			expectedBalance, result[0].EffectiveBalance)
	}
}

func TestAggregateByCluster_NotOnBeacon(t *testing.T) {
	o := &Oracle{}

	pk1 := make([]byte, 48)
	pk1[0] = 0x01

	clusterID := make([]byte, 32)
	clusterID[0] = 0xCC

	validators := []ethsync.ActiveValidator{
		{ClusterID: clusterID, ValidatorPubkey: pk1},
	}

	// Empty balance map - validator not on beacon
	balanceMap := make(map[phase0.BLSPubKey]uint64)

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	if notOnBeacon != 1 {
		t.Errorf("Expected 1 validator not on beacon, got %d", notOnBeacon)
	}

	// Should default to balance floor
	if result[0].EffectiveBalance != balanceFloorGwei {
		t.Errorf("Expected cluster balance %d gwei, got %d gwei",
			balanceFloorGwei, result[0].EffectiveBalance)
	}
}
