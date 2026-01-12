package updater

import (
	"bytes"
	"context"
	"testing"

	"github.com/ssvlabs/ssv-oracle/merkle"
	"github.com/ssvlabs/ssv-oracle/storage"
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

// GetCluster returns a cluster row by ID.
func (m *mockStorage) GetCluster(ctx context.Context, clusterID []byte) (*storage.ClusterRow, error) {
	return m.clusters[string(clusterID)], nil
}

// GetCommitByBlock returns a commit by reference block.
func (m *mockStorage) GetCommitByBlock(ctx context.Context, blockNum uint64) (*storage.OracleCommit, error) {
	return m.commits[blockNum], nil
}

func TestProcessCommit_EmptyClusters(t *testing.T) {
	store := newMockStorage()
	u := &Updater{storage: store}

	// Build empty tree to get the correct root
	emptyRoot := merkle.NewTree(nil).Root

	commit := &storage.OracleCommit{
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
		{ClusterID: clusterID[:], EffectiveBalance: 32},
	}

	commit := &storage.OracleCommit{
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
