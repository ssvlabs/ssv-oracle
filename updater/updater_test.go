package updater

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"ssv-oracle/merkle"
	"ssv-oracle/storage"
)

// mockStorage implements the updaterStorage interface for testing.
type mockStorage struct {
	clusters map[string]*storage.ClusterRow
	commits  map[uint64]*storage.OracleCommit
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		clusters: make(map[string]*storage.ClusterRow),
		commits:  make(map[uint64]*storage.OracleCommit),
	}
}

func (m *mockStorage) GetCluster(ctx context.Context, clusterID []byte) (*storage.ClusterRow, error) {
	return m.clusters[string(clusterID)], nil
}

func (m *mockStorage) GetCommitByBlock(ctx context.Context, blockNum uint64) (*storage.OracleCommit, error) {
	return m.commits[blockNum], nil
}

func TestNew(t *testing.T) {
	cfg := &Config{
		Storage:        nil, // Would be *storage.Storage
		ContractClient: nil, // Would be *contract.Client
	}

	u := New(cfg)
	if u == nil {
		t.Fatal("New() returned nil")
	}
}

func TestProcessCommit_EmptyClusters(t *testing.T) {
	store := newMockStorage()
	u := &Updater{storage: store}

	// Build empty tree to get the correct root
	emptyRoot := merkle.BuildMerkleTree(nil)

	commit := &storage.OracleCommit{
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
	store := newMockStorage()
	u := &Updater{storage: store}

	// Create a cluster balance but with wrong root
	clusterID := [32]byte{0x01}
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: clusterID[:], EffectiveBalance: 32000000000},
	}

	commit := &storage.OracleCommit{
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
	store := newMockStorage()

	// Create a cluster in store
	clusterID := [32]byte{0x01}
	store.clusters[string(clusterID[:])] = &storage.ClusterRow{
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

	clusterBalances := []storage.ClusterBalance{
		{ClusterID: clusterID[:], EffectiveBalance: 32000000000},
	}

	commit := &storage.OracleCommit{
		RoundID:         1,
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  1000,
		ClusterBalances: clusterBalances,
	}

	// Without contract client, processCluster will panic
	// This test verifies root validation passes before that point
	u := &Updater{storage: store, contractClient: nil}

	// We expect a panic since contractClient is nil
	// This is a limitation - full testing requires mock contract client
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when contractClient is nil")
		}
	}()

	_ = u.processCommit(context.Background(), commit)
}
