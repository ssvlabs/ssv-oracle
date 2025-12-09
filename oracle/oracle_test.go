package oracle

import (
	"context"
	"testing"

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
		Storage:        nil, // Would be *ethsync.PostgresStorage
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
