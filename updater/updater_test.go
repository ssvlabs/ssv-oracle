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
// returns a fixed currentBalance for every GetClusterEffectiveBalance call.
// UpdateClusterBalance consults updateByCluster (if set) for per-cluster
// behavior, otherwise returns updateErr (or success). Counters let
// assertions verify how far a batch ran before aborting.
type mockContract struct {
	currentBalance  uint32
	updateErr       error
	updateByCluster func(cluster contract.Cluster) error
	balanceCalls    int
	updateCalls     int
}

func (m *mockContract) SubscribeRootCommitted(_ context.Context, _ *uint64) (<-chan *contract.RootCommittedEvent, <-chan error, error) {
	return nil, nil, errors.New("not used in tests")
}

func (m *mockContract) GetClusterEffectiveBalance(_ context.Context, _ common.Address, _ []uint64, _ contract.Cluster) (uint32, error) {
	m.balanceCalls++
	return m.currentBalance, nil
}

func (m *mockContract) UpdateClusterBalance(_ context.Context, _ uint64, _ common.Address, _ []uint64, cluster contract.Cluster, _ uint32, _ [][32]byte) (*types.Receipt, error) {
	m.updateCalls++
	err := m.updateErr
	if m.updateByCluster != nil {
		err = m.updateByCluster(cluster)
	}
	if err != nil {
		return nil, err
	}
	return &types.Receipt{
		TxHash:      common.Hash{0xab},
		BlockNumber: big.NewInt(1234),
	}, nil
}

// mockHeadStateBuilder implements the headStateBuilder interface. It returns
// a pre-baked overlay (or err) and records the ids it was called with.
type mockHeadStateBuilder struct {
	overlay map[[32]byte]storage.ClusterRow
	err     error
	calls   int
	lastIDs [][]byte
}

func (m *mockHeadStateBuilder) BuildHeadStateSnapshot(_ context.Context, ids [][]byte) (map[[32]byte]storage.ClusterRow, error) {
	m.calls++
	m.lastIDs = ids
	if m.err != nil {
		return nil, m.err
	}
	return m.overlay, nil
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

// TestProcessCommit_StaleLeafRetry exercises the SSV-OR-1 retry flow: when
// the first pass discovers stale clusters, processCommit must build a fresh
// in-memory overlay via the syncer and retry the stale clusters with that
// overlay (instead of writing to the shared clusters table).
func TestProcessCommit_StaleLeafRetry(t *testing.T) {
	clusterIDs := [][32]byte{{0x01}, {0x02}, {0x03}}
	balances := make([]storage.ClusterBalance, len(clusterIDs))
	for i, id := range clusterIDs {
		balances[i] = storage.ClusterBalance{
			ClusterID:        id[:],
			EffectiveBalance: 32,
		}
	}
	tree := buildTree(balances)
	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  1000,
		ClusterBalances: balances,
	}

	const (
		staleValidatorCount = uint32(5)
		freshValidatorCount = uint32(7)
	)

	seedStorage := func(validatorCount uint32) *mockStorage {
		store := newMockStorage()
		for _, id := range clusterIDs {
			store.clusters[string(id[:])] = &storage.ClusterRow{
				ClusterID:      id[:],
				OwnerAddress:   make([]byte, 20),
				OperatorIDs:    []uint64{1, 2, 3, 4},
				IsActive:       true,
				Balance:        big.NewInt(0),
				ValidatorCount: validatorCount,
			}
		}
		return store
	}

	buildOverlay := func(validatorCount uint32) map[[32]byte]storage.ClusterRow {
		overlay := make(map[[32]byte]storage.ClusterRow, len(clusterIDs))
		for _, id := range clusterIDs {
			overlay[id] = storage.ClusterRow{
				ClusterID:      id[:],
				OwnerAddress:   make([]byte, 20),
				OperatorIDs:    []uint64{1, 2, 3, 4},
				IsActive:       true,
				Balance:        big.NewInt(0),
				ValidatorCount: validatorCount,
			}
		}
		return overlay
	}

	// Rejects stale ValidatorCount with IncorrectClusterState; lets fresh through.
	rejectStale := func(cluster contract.Cluster) error {
		if cluster.ValidatorCount == staleValidatorCount {
			return &txmanager.RevertError{Reason: "IncorrectClusterState"}
		}
		return nil
	}

	t.Run("no stale leaves — BuildHeadStateSnapshot not called", func(t *testing.T) {
		store := seedStorage(staleValidatorCount)
		// currentBalance matches leaf → "balance unchanged" → no update attempt → no stale leaves
		mc := &mockContract{currentBalance: 32}
		mhsb := &mockHeadStateBuilder{}
		u := &Updater{storage: store, contractClient: mc, syncer: mhsb}

		if err := u.processCommit(context.Background(), commit); err != nil {
			t.Fatalf("processCommit: %v", err)
		}
		if mhsb.calls != 0 {
			t.Errorf("BuildHeadStateSnapshot calls = %d, want 0", mhsb.calls)
		}
		if mc.updateCalls != 0 {
			t.Errorf("UpdateClusterBalance calls = %d, want 0", mc.updateCalls)
		}
	})

	t.Run("stale leaves — retry with fresh overlay succeeds", func(t *testing.T) {
		store := seedStorage(staleValidatorCount)
		mc := &mockContract{
			currentBalance:  0, // forces UpdateClusterBalance attempt for each cluster
			updateByCluster: rejectStale,
		}
		mhsb := &mockHeadStateBuilder{overlay: buildOverlay(freshValidatorCount)}
		u := &Updater{storage: store, contractClient: mc, syncer: mhsb}

		if err := u.processCommit(context.Background(), commit); err != nil {
			t.Fatalf("processCommit: %v", err)
		}
		if mhsb.calls != 1 {
			t.Errorf("BuildHeadStateSnapshot calls = %d, want 1", mhsb.calls)
		}
		if len(mhsb.lastIDs) != len(clusterIDs) {
			t.Errorf("BuildHeadStateSnapshot ids = %d, want %d", len(mhsb.lastIDs), len(clusterIDs))
		}
		// 3 first-pass attempts (stale → IncorrectClusterState) + 3 retry attempts (fresh → success)
		if mc.updateCalls != 2*len(clusterIDs) {
			t.Errorf("UpdateClusterBalance calls = %d, want %d (first pass + retry)", mc.updateCalls, 2*len(clusterIDs))
		}
	})

	t.Run("stale leaves — BuildHeadStateSnapshot fails → no retry attempts", func(t *testing.T) {
		store := seedStorage(staleValidatorCount)
		mc := &mockContract{
			currentBalance: 0,
			updateByCluster: func(_ contract.Cluster) error {
				return &txmanager.RevertError{Reason: "IncorrectClusterState"}
			},
		}
		mhsb := &mockHeadStateBuilder{err: errors.New("rpc unavailable")}
		u := &Updater{storage: store, contractClient: mc, syncer: mhsb}

		if err := u.processCommit(context.Background(), commit); err != nil {
			t.Fatalf("processCommit: %v", err)
		}
		if mhsb.calls != 1 {
			t.Errorf("BuildHeadStateSnapshot calls = %d, want 1", mhsb.calls)
		}
		// Only first pass — retry path skipped because overlay build failed
		if mc.updateCalls != len(clusterIDs) {
			t.Errorf("UpdateClusterBalance calls = %d, want %d (first pass only)", mc.updateCalls, len(clusterIDs))
		}
	})

	t.Run("stale leaves — retry still fails (overlay also stale)", func(t *testing.T) {
		store := seedStorage(staleValidatorCount)
		mc := &mockContract{
			currentBalance:  0,
			updateByCluster: rejectStale,
		}
		// Overlay carries the same stale validator count → retry still rejected.
		mhsb := &mockHeadStateBuilder{overlay: buildOverlay(staleValidatorCount)}
		u := &Updater{storage: store, contractClient: mc, syncer: mhsb}

		if err := u.processCommit(context.Background(), commit); err != nil {
			t.Fatalf("processCommit: %v", err)
		}
		if mhsb.calls != 1 {
			t.Errorf("BuildHeadStateSnapshot calls = %d, want 1", mhsb.calls)
		}
		if mc.updateCalls != 2*len(clusterIDs) {
			t.Errorf("UpdateClusterBalance calls = %d, want %d (first pass + retry, both rejected)", mc.updateCalls, 2*len(clusterIDs))
		}
	})
}
