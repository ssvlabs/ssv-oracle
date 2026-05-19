package updater

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/ssvlabs/ssv-oracle/contract"
	"github.com/ssvlabs/ssv-oracle/merkle"
	"github.com/ssvlabs/ssv-oracle/storage"
	"github.com/ssvlabs/ssv-oracle/txmanager"
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
func (m *mockStorage) GetCluster(_ context.Context, clusterID []byte) (*storage.ClusterRow, error) {
	return m.clusters[string(clusterID)], nil
}

// GetCommitByBlock returns a commit by reference block.
func (m *mockStorage) GetCommitByBlock(_ context.Context, blockNum uint64) (*storage.OracleCommit, error) {
	return m.commits[blockNum], nil
}

// mockContract implements the updaterContract interface for testing. It
// returns a fixed currentBalance for every GetClusterEffectiveBalance call
// and a fixed updateErr (or success receipt) for every UpdateClusterBalance
// call. updateCalls / balanceCalls let assertions verify how far a batch ran
// before aborting.
type mockContract struct {
	currentBalance uint32
	updateErr      error
	balanceCalls   int
	updateCalls    int
}

func (m *mockContract) SubscribeRootCommitted(_ context.Context, _ *uint64) (<-chan *contract.RootCommittedEvent, <-chan error, error) {
	return nil, nil, errors.New("not used in tests")
}

func (m *mockContract) GetClusterEffectiveBalance(_ context.Context, _ common.Address, _ []uint64, _ contract.Cluster) (uint32, error) {
	m.balanceCalls++
	return m.currentBalance, nil
}

func (m *mockContract) UpdateClusterBalance(_ context.Context, _ uint64, _ common.Address, _ []uint64, _ contract.Cluster, _ uint32, _ [][32]byte) (*types.Receipt, error) {
	m.updateCalls++
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	return &types.Receipt{
		TxHash:      common.Hash{0xab},
		BlockNumber: big.NewInt(1234),
	}, nil
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

// TestProcessAllClusters_RevertClassifications exercises each branch of the
// revert-reason switch in processAllClusters and asserts that stats counters,
// staleLeaves accumulation, and batch-abort behavior match the intended
// classification (SSV-OR-3 / SSV-OR-8 / SSV-OR-9 audit findings).
func TestProcessAllClusters_RevertClassifications(t *testing.T) {
	clusterIDs := [][32]byte{{0x01}, {0x02}, {0x03}}
	balances := make([]storage.ClusterBalance, len(clusterIDs))
	for i, id := range clusterIDs {
		balances[i] = storage.ClusterBalance{
			ClusterID:        id[:],
			EffectiveBalance: 32,
		}
	}
	tree := buildTree(balances)

	const (
		// currentBalanceStale differs from the leaf's EffectiveBalance, so
		// processCluster always reaches the UpdateClusterBalance call.
		currentBalanceStale = uint32(16)
		// currentBalanceFresh matches the leaf, so processCluster short-circuits
		// at the "balance unchanged" check before any UpdateClusterBalance call.
		currentBalanceFresh = uint32(32)
	)

	tests := []struct {
		name             string
		currentBalance   uint32
		updateErr        error
		wantUpdated      int
		wantSkipped      int
		wantFailed       int
		wantStaleLeaves  int
		wantUpdateCalls  int
		wantBalanceCalls int
	}{
		{
			name:             "balance unchanged — counted as skipped, no update attempted",
			currentBalance:   currentBalanceFresh,
			wantSkipped:      3,
			wantBalanceCalls: 3,
		},
		{
			name:             "update succeeds — counted as updated",
			currentBalance:   currentBalanceStale,
			wantUpdated:      3,
			wantUpdateCalls:  3,
			wantBalanceCalls: 3,
		},
		{
			name:             "IncorrectClusterState — all clusters become stale (SSV-OR-3)",
			currentBalance:   currentBalanceStale,
			updateErr:        &txmanager.RevertError{Reason: "IncorrectClusterState"},
			wantStaleLeaves:  3,
			wantUpdateCalls:  3,
			wantBalanceCalls: 3,
		},
		{
			name:             "ClusterIsLiquidated — all clusters become stale (SSV-OR-8)",
			currentBalance:   currentBalanceStale,
			updateErr:        &txmanager.RevertError{Reason: "ClusterIsLiquidated"},
			wantStaleLeaves:  3,
			wantUpdateCalls:  3,
			wantBalanceCalls: 3,
		},
		{
			name:             "MustUseLatestRoot — aborts batch after one cluster (SSV-OR-9)",
			currentBalance:   currentBalanceStale,
			updateErr:        &txmanager.RevertError{Reason: "MustUseLatestRoot"},
			wantSkipped:      1,
			wantUpdateCalls:  1,
			wantBalanceCalls: 1,
		},
		{
			name:             "RootNotFound — aborts batch after one cluster (SSV-OR-9)",
			currentBalance:   currentBalanceStale,
			updateErr:        &txmanager.RevertError{Reason: "RootNotFound"},
			wantSkipped:      1,
			wantUpdateCalls:  1,
			wantBalanceCalls: 1,
		},
		{
			name:             "unknown revert reason — counted as skipped, batch continues",
			currentBalance:   currentBalanceStale,
			updateErr:        &txmanager.RevertError{Reason: "SomethingNotClassified"},
			wantSkipped:      3,
			wantUpdateCalls:  3,
			wantBalanceCalls: 3,
		},
		{
			name:             "non-revert error — counted as failed, batch continues",
			currentBalance:   currentBalanceStale,
			updateErr:        errors.New("rpc timeout"),
			wantFailed:       3,
			wantUpdateCalls:  3,
			wantBalanceCalls: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockStorage()
			for _, id := range clusterIDs {
				store.clusters[string(id[:])] = &storage.ClusterRow{
					ClusterID:    id[:],
					OwnerAddress: make([]byte, 20),
					OperatorIDs:  []uint64{1, 2, 3, 4},
					IsActive:     true,
					Balance:      big.NewInt(0),
				}
			}

			mc := &mockContract{
				currentBalance: tt.currentBalance,
				updateErr:      tt.updateErr,
			}
			u := &Updater{storage: store, contractClient: mc}

			stats, staleLeaves := u.processAllClusters(context.Background(), 1000, tree, storageLookup(store))

			if stats.updated != tt.wantUpdated {
				t.Errorf("stats.updated = %d, want %d", stats.updated, tt.wantUpdated)
			}
			if stats.skipped != tt.wantSkipped {
				t.Errorf("stats.skipped = %d, want %d", stats.skipped, tt.wantSkipped)
			}
			if stats.failed != tt.wantFailed {
				t.Errorf("stats.failed = %d, want %d", stats.failed, tt.wantFailed)
			}
			if len(staleLeaves) != tt.wantStaleLeaves {
				t.Errorf("len(staleLeaves) = %d, want %d", len(staleLeaves), tt.wantStaleLeaves)
			}
			if mc.updateCalls != tt.wantUpdateCalls {
				t.Errorf("UpdateClusterBalance calls = %d, want %d", mc.updateCalls, tt.wantUpdateCalls)
			}
			if mc.balanceCalls != tt.wantBalanceCalls {
				t.Errorf("GetClusterEffectiveBalance calls = %d, want %d", mc.balanceCalls, tt.wantBalanceCalls)
			}
		})
	}
}
