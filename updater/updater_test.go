package updater

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
)

// mockStorage implements the storage interface for testing.
type mockStorage struct {
	clusters map[string]*ethsync.ClusterRow
	commits  map[uint64]*ethsync.OracleCommit
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		clusters: make(map[string]*ethsync.ClusterRow),
		commits:  make(map[uint64]*ethsync.OracleCommit),
	}
}

func (m *mockStorage) GetCluster(ctx context.Context, clusterID []byte) (*ethsync.ClusterRow, error) {
	return m.clusters[string(clusterID)], nil
}

func (m *mockStorage) GetCommitByBlock(ctx context.Context, blockNum uint64) (*ethsync.OracleCommit, error) {
	return m.commits[blockNum], nil
}

// mockSyncer implements the Syncer interface for testing.
type mockSyncer struct{}

func (m *mockSyncer) SyncClustersToHead(ctx context.Context) error {
	return nil
}

func TestNew(t *testing.T) {
	cfg := &Config{
		Storage:        nil, // Would be *ethsync.Storage
		ContractClient: nil, // Would be *contract.Client
	}

	u := New(cfg)
	if u == nil {
		t.Fatal("New() returned nil")
	}
}

func TestProcessCommit_EmptyClusters(t *testing.T) {
	storage := newMockStorage()
	u := &Updater{storage: storage, syncer: &mockSyncer{}}

	// Build empty tree to get the correct root
	emptyRoot := merkle.BuildMerkleTree(nil)

	commit := &ethsync.OracleCommit{
		RoundID:         1,
		TargetEpoch:     100,
		MerkleRoot:      emptyRoot[:],
		ReferenceBlock:  1000,
		ClusterBalances: nil, // Empty
	}

	err := u.processCommit(context.Background(), commit)
	if err != nil {
		t.Errorf("processCommit() with empty clusters should not error, got: %v", err)
	}
}

func TestProcessCommit_RootMismatch(t *testing.T) {
	storage := newMockStorage()
	u := &Updater{storage: storage, syncer: &mockSyncer{}}

	// Create a cluster balance but with wrong root
	clusterID := [32]byte{0x01}
	clusterBalances := []ethsync.ClusterBalance{
		{ClusterID: clusterID[:], EffectiveBalance: 32000000000},
	}

	commit := &ethsync.OracleCommit{
		RoundID:         1,
		TargetEpoch:     100,
		MerkleRoot:      make([]byte, 32), // Wrong root (all zeros)
		ReferenceBlock:  1000,
		ClusterBalances: clusterBalances,
	}

	err := u.processCommit(context.Background(), commit)
	if err == nil {
		t.Error("processCommit() should error on root mismatch")
	}
	if err != nil && !bytes.Contains([]byte(err.Error()), []byte("root mismatch")) {
		t.Errorf("Expected 'root mismatch' error, got: %v", err)
	}
}

func TestProcessCommit_ValidRootNoContractClient(t *testing.T) {
	storage := newMockStorage()

	// Create a cluster in storage
	clusterID := [32]byte{0x01}
	storage.clusters[string(clusterID[:])] = &ethsync.ClusterRow{
		ClusterID:      clusterID[:],
		OwnerAddress:   make([]byte, 20),
		OperatorIDs:    []uint64{1, 2, 3, 4},
		ValidatorCount: 1,
		IsActive:       true,
		Balance:        big.NewInt(1000),
	}

	// Build tree with same data
	clusterMap := map[[32]byte]uint64{
		clusterID: 32000000000,
	}
	tree := merkle.BuildMerkleTreeWithProofs(clusterMap)

	clusterBalances := []ethsync.ClusterBalance{
		{ClusterID: clusterID[:], EffectiveBalance: 32000000000},
	}

	commit := &ethsync.OracleCommit{
		RoundID:         1,
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  1000,
		ClusterBalances: clusterBalances,
	}

	// Without contract client, processCluster will panic
	// This test verifies root validation passes before that point
	u := &Updater{storage: storage, contractClient: nil, syncer: &mockSyncer{}}

	// We expect a panic since contractClient is nil
	// This is a limitation - full testing requires mock contract client
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when contractClient is nil")
		}
	}()

	_ = u.processCommit(context.Background(), commit)
}
