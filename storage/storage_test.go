package storage

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"
)

func setupTestStorage(t *testing.T) *Storage {
	t.Helper()

	// Use temp file for each test to ensure isolation
	tmpFile, err := os.CreateTemp("", "test_*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	_ = tmpFile.Close()

	// Register cleanup
	t.Cleanup(func() {
		_ = os.Remove(tmpFile.Name())
		_ = os.Remove(tmpFile.Name() + "-wal")
		_ = os.Remove(tmpFile.Name() + "-shm")
	})

	storage, err := New(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	t.Cleanup(func() {
		_ = storage.Close()
	})

	return storage
}

func TestStorage_Connection(t *testing.T) {
	storage := setupTestStorage(t)

	ctx := context.Background()
	lastBlock, err := storage.GetLastSyncedBlock(ctx)
	if err != nil {
		t.Fatalf("Failed to get last synced block: %v", err)
	}

	if lastBlock != 0 {
		t.Errorf("Expected last synced block 0 for fresh DB, got %d", lastBlock)
	}
}

func TestStorage_SyncProgress(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	err := storage.UpdateLastSyncedBlock(ctx, 12345)
	if err != nil {
		t.Fatalf("Failed to update last synced block: %v", err)
	}

	lastBlock, err := storage.GetLastSyncedBlock(ctx)
	if err != nil {
		t.Fatalf("Failed to get last synced block: %v", err)
	}

	if lastBlock != 12345 {
		t.Errorf("Expected last synced block 12345, got %d", lastBlock)
	}
}

func TestStorage_ChainID(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	// Fresh DB should have nil chain ID
	chainID, err := storage.GetChainID(ctx)
	if err != nil {
		t.Fatalf("Failed to get chain ID: %v", err)
	}
	if chainID != nil {
		t.Errorf("Expected nil chain ID for fresh DB, got %d", *chainID)
	}

	// Set chain ID
	err = storage.SetChainID(ctx, 17000)
	if err != nil {
		t.Fatalf("Failed to set chain ID: %v", err)
	}

	// Verify chain ID was set
	chainID, err = storage.GetChainID(ctx)
	if err != nil {
		t.Fatalf("Failed to get chain ID after set: %v", err)
	}
	if chainID == nil || *chainID != 17000 {
		t.Errorf("Expected chain ID 17000, got %v", chainID)
	}
}

func TestStorage_Cluster(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	clusterID := make([]byte, 32)
	clusterID[0] = 0x01
	clusterID[1] = 0x02
	clusterID[2] = 0x03
	clusterID[3] = 0x04

	ownerAddress := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44}

	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    ownerAddress,
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  2,
		NetworkFeeIndex: 12345,
		Index:           67890,
		IsActive:        true,
		Balance:         big.NewInt(1000000000000000000),
	}

	err := storage.UpsertCluster(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to upsert cluster: %v", err)
	}

	retrieved, err := storage.GetCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("Failed to get cluster: %v", err)
	}
	if retrieved == nil {
		t.Fatal("Expected cluster, got nil")
	}
	if retrieved.ValidatorCount != 2 {
		t.Errorf("Expected validator count 2, got %d", retrieved.ValidatorCount)
	}
	if !retrieved.IsActive {
		t.Error("Expected cluster to be active")
	}
	if len(retrieved.OperatorIDs) != 4 {
		t.Errorf("Expected 4 operator IDs, got %d", len(retrieved.OperatorIDs))
	}
	if retrieved.Balance.Cmp(big.NewInt(1000000000000000000)) != 0 {
		t.Errorf("Expected balance 1e18, got %s", retrieved.Balance.String())
	}

	// Update cluster
	cluster.ValidatorCount = 3
	cluster.IsActive = false

	err = storage.UpsertCluster(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to update cluster: %v", err)
	}

	retrieved, err = storage.GetCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("Failed to get updated cluster: %v", err)
	}
	if retrieved.ValidatorCount != 3 {
		t.Errorf("Expected validator count 3, got %d", retrieved.ValidatorCount)
	}
	if retrieved.IsActive {
		t.Error("Expected cluster to be inactive")
	}
}

func TestStorage_GetCluster_NotFound(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	clusterID := make([]byte, 32)
	clusterID[0] = 0xff

	retrieved, err := storage.GetCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if retrieved != nil {
		t.Error("Expected nil for non-existent cluster")
	}
}

func TestStorage_Validator(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	clusterID := make([]byte, 32)
	clusterID[0] = 0x10
	clusterID[1] = 0x20

	ownerAddress := make([]byte, 20)
	ownerAddress[0] = 0x11

	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    ownerAddress,
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  1,
		NetworkFeeIndex: 0,
		Index:           0,
		IsActive:        true,
		Balance:         big.NewInt(0),
	}
	err := storage.UpsertCluster(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to upsert cluster: %v", err)
	}

	pubkey := make([]byte, 48)
	pubkey[0] = 0xaa
	pubkey[1] = 0xbb

	err = storage.InsertValidator(ctx, clusterID, pubkey)
	if err != nil {
		t.Fatalf("Failed to insert validator: %v", err)
	}

	// Insert duplicate should be idempotent
	err = storage.InsertValidator(ctx, clusterID, pubkey)
	if err != nil {
		t.Fatalf("Failed to insert duplicate validator: %v", err)
	}

	validators, err := storage.GetActiveValidators(ctx)
	if err != nil {
		t.Fatalf("Failed to get active validators: %v", err)
	}

	found := false
	for _, v := range validators {
		if v.ClusterID[0] == 0x10 && v.ValidatorPubkey[0] == 0xaa {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find our validator in active validators")
	}

	err = storage.DeleteValidator(ctx, clusterID, pubkey)
	if err != nil {
		t.Fatalf("Failed to delete validator: %v", err)
	}

	validators, err = storage.GetActiveValidators(ctx)
	if err != nil {
		t.Fatalf("Failed to get active validators after delete: %v", err)
	}

	found = false
	for _, v := range validators {
		if v.ClusterID[0] == 0x10 && v.ValidatorPubkey[0] == 0xaa {
			found = true
			break
		}
	}
	if found {
		t.Error("Should NOT find deleted validator")
	}
}

func TestStorage_ValidatorInvalidPubkey(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	clusterID := make([]byte, 32)
	invalidPubkey := make([]byte, 32) // Should be 48 bytes

	err := storage.InsertValidator(ctx, clusterID, invalidPubkey)
	if err == nil {
		t.Error("Expected error for invalid pubkey length")
	}
}

func TestStorage_Transaction(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	tx, err := storage.BeginTx(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	clusterID := make([]byte, 32)
	clusterID[0] = 0xaa
	clusterID[1] = 0xbb

	ownerAddress := make([]byte, 20)

	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    ownerAddress,
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  1,
		NetworkFeeIndex: 0,
		Index:           0,
		IsActive:        true,
		Balance:         big.NewInt(0),
	}
	err = tx.UpsertCluster(ctx, cluster)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("Failed to upsert in tx: %v", err)
	}

	pubkey := make([]byte, 48)
	pubkey[0] = 0xcc
	pubkey[1] = 0xdd

	err = tx.InsertValidator(ctx, clusterID, pubkey)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("Failed to insert validator in tx: %v", err)
	}

	err = tx.Commit()
	if err != nil {
		t.Fatalf("Failed to commit tx: %v", err)
	}

	// Verify data was committed
	retrieved, err := storage.GetCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("Failed to get cluster after commit: %v", err)
	}
	if retrieved == nil {
		t.Error("Expected cluster after commit")
	}
}

func TestStorage_TransactionRollback(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	tx, err := storage.BeginTx(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	clusterID := make([]byte, 32)
	clusterID[0] = 0xee
	clusterID[1] = 0xff

	ownerAddress := make([]byte, 20)

	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    ownerAddress,
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  1,
		NetworkFeeIndex: 0,
		Index:           0,
		IsActive:        true,
		Balance:         big.NewInt(0),
	}
	err = tx.UpsertCluster(ctx, cluster)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("Failed to upsert in tx: %v", err)
	}

	err = tx.Rollback()
	if err != nil {
		t.Fatalf("Failed to rollback tx: %v", err)
	}

	// Verify data was NOT committed
	retrieved, err := storage.GetCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("Failed to get cluster after rollback: %v", err)
	}
	if retrieved != nil {
		t.Error("Expected nil cluster after rollback")
	}
}

func TestStorage_InsertEvent(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	event := &ContractEvent{
		EventType:        "ValidatorAdded",
		BlockNumber:      500000,
		BlockHash:        []byte{0x01, 0x02},
		BlockTime:        time.Now(),
		TransactionHash:  []byte{0x03, 0x04},
		TransactionIndex: 0,
		LogIndex:         1,
		Error:            nil,
	}

	err := storage.InsertEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	// Insert duplicate should be idempotent
	err = storage.InsertEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert duplicate event: %v", err)
	}
}

func TestStorage_OracleCommit(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	targetEpoch := uint64(100)
	merkleRoot := make([]byte, 32)
	merkleRoot[0] = 0xff
	referenceBlock := uint64(500000)

	clusterBalances := []ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
	}
	clusterBalances[0].ClusterID[0] = 0x01
	clusterBalances[1].ClusterID[0] = 0x02

	err := storage.InsertPendingCommit(ctx, targetEpoch, merkleRoot, referenceBlock, clusterBalances)
	if err != nil {
		t.Fatalf("Failed to insert pending commit: %v", err)
	}

	commit, err := storage.GetCommitByBlock(ctx, referenceBlock)
	if err != nil {
		t.Fatalf("Failed to get commit by block: %v", err)
	}
	if commit == nil {
		t.Fatal("Expected commit, got nil")
	}
	if commit.TargetEpoch != targetEpoch {
		t.Errorf("Expected target epoch %d, got %d", targetEpoch, commit.TargetEpoch)
	}
	if len(commit.ClusterBalances) != 2 {
		t.Errorf("Expected 2 cluster balances, got %d", len(commit.ClusterBalances))
	}
	if commit.Status != CommitStatusPending {
		t.Errorf("Expected status %s, got %s", CommitStatusPending, commit.Status)
	}

	// Update status
	txHash := make([]byte, 32)
	txHash[0] = 0xee
	err = storage.UpdateCommitStatus(ctx, targetEpoch, CommitStatusConfirmed, txHash)
	if err != nil {
		t.Fatalf("Failed to update commit status: %v", err)
	}

	commit, err = storage.GetCommitByBlock(ctx, referenceBlock)
	if err != nil {
		t.Fatalf("Failed to get commit after status update: %v", err)
	}
	if commit.Status != CommitStatusConfirmed {
		t.Errorf("Expected status %s, got %s", CommitStatusConfirmed, commit.Status)
	}
	if commit.TxHash[0] != 0xee {
		t.Error("Expected tx hash to be set")
	}

	// Test idempotency (same target_epoch)
	err = storage.InsertPendingCommit(ctx, targetEpoch, merkleRoot, referenceBlock, clusterBalances)
	if err != nil {
		t.Fatalf("InsertPendingCommit should be idempotent, got error: %v", err)
	}
}

func TestStorage_GetCommitByBlock_NotFound(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	commit, err := storage.GetCommitByBlock(ctx, 999999)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if commit != nil {
		t.Error("Expected nil for non-existent commit")
	}
}

func TestStorage_GetLatestCommit_NoCommits(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	commit, err := storage.GetLatestCommit(ctx)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if commit != nil {
		t.Error("Expected nil for empty database")
	}
}

func TestStorage_GetLatestCommit_OnlyPending(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	// Insert a pending commit (not confirmed)
	clusterBalances := []ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	clusterBalances[0].ClusterID[0] = 0x01

	err := storage.InsertPendingCommit(ctx, 100, make([]byte, 32), 500000, clusterBalances)
	if err != nil {
		t.Fatalf("Failed to insert pending commit: %v", err)
	}

	// GetLatestCommit should return nil (only returns confirmed)
	commit, err := storage.GetLatestCommit(ctx)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if commit != nil {
		t.Error("Expected nil when only pending commits exist")
	}
}

func TestStorage_GetLatestCommit_ReturnsLatestConfirmed(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	// Insert two commits at different epochs
	clusterBalances1 := []ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	clusterBalances1[0].ClusterID[0] = 0x01

	clusterBalances2 := []ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
		{ClusterID: make([]byte, 32), EffectiveBalance: 128},
	}
	clusterBalances2[0].ClusterID[0] = 0x02
	clusterBalances2[1].ClusterID[0] = 0x03

	merkleRoot1 := make([]byte, 32)
	merkleRoot1[0] = 0xaa
	merkleRoot2 := make([]byte, 32)
	merkleRoot2[0] = 0xbb

	// Insert older commit
	err := storage.InsertPendingCommit(ctx, 100, merkleRoot1, 500000, clusterBalances1)
	if err != nil {
		t.Fatalf("Failed to insert first commit: %v", err)
	}
	txHash1 := make([]byte, 32)
	txHash1[0] = 0x11
	err = storage.UpdateCommitStatus(ctx, 100, CommitStatusConfirmed, txHash1)
	if err != nil {
		t.Fatalf("Failed to confirm first commit: %v", err)
	}

	// Insert newer commit
	err = storage.InsertPendingCommit(ctx, 200, merkleRoot2, 600000, clusterBalances2)
	if err != nil {
		t.Fatalf("Failed to insert second commit: %v", err)
	}
	txHash2 := make([]byte, 32)
	txHash2[0] = 0x22
	err = storage.UpdateCommitStatus(ctx, 200, CommitStatusConfirmed, txHash2)
	if err != nil {
		t.Fatalf("Failed to confirm second commit: %v", err)
	}

	// GetLatestCommit should return the newer one (epoch 200)
	commit, err := storage.GetLatestCommit(ctx)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if commit == nil {
		t.Fatal("Expected commit, got nil")
	}
	if commit.TargetEpoch != 200 {
		t.Errorf("Expected epoch 200, got %d", commit.TargetEpoch)
	}
	if commit.ReferenceBlock != 600000 {
		t.Errorf("Expected reference block 600000, got %d", commit.ReferenceBlock)
	}
	if commit.MerkleRoot[0] != 0xbb {
		t.Errorf("Expected merkle root starting with 0xbb, got 0x%x", commit.MerkleRoot[0])
	}
	if commit.TxHash[0] != 0x22 {
		t.Errorf("Expected tx hash starting with 0x22, got 0x%x", commit.TxHash[0])
	}
	if len(commit.ClusterBalances) != 2 {
		t.Errorf("Expected 2 cluster balances, got %d", len(commit.ClusterBalances))
	}
	if commit.Status != CommitStatusConfirmed {
		t.Errorf("Expected status %s, got %s", CommitStatusConfirmed, commit.Status)
	}
}

func TestStorage_ClearAllState(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	// Add some data
	err := storage.SetChainID(ctx, 17000)
	if err != nil {
		t.Fatalf("Failed to set chain ID: %v", err)
	}

	err = storage.UpdateLastSyncedBlock(ctx, 12345)
	if err != nil {
		t.Fatalf("Failed to update sync block: %v", err)
	}

	clusterID := make([]byte, 32)
	clusterID[0] = 0x01
	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    make([]byte, 20),
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  1,
		NetworkFeeIndex: 0,
		Index:           0,
		IsActive:        true,
		Balance:         big.NewInt(0),
	}
	err = storage.UpsertCluster(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to upsert cluster: %v", err)
	}

	// Clear all state
	err = storage.ClearAllState(ctx)
	if err != nil {
		t.Fatalf("Failed to clear state: %v", err)
	}

	// Verify state was cleared
	chainID, err := storage.GetChainID(ctx)
	if err != nil {
		t.Fatalf("Failed to get chain ID: %v", err)
	}
	if chainID != nil {
		t.Error("Expected nil chain ID after clear")
	}

	lastBlock, err := storage.GetLastSyncedBlock(ctx)
	if err != nil {
		t.Fatalf("Failed to get last synced block: %v", err)
	}
	if lastBlock != 0 {
		t.Errorf("Expected 0 last synced block after clear, got %d", lastBlock)
	}

	retrieved, err := storage.GetCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("Failed to get cluster: %v", err)
	}
	if retrieved != nil {
		t.Error("Expected nil cluster after clear")
	}
}

func TestStorage_DeleteCluster_CascadesValidators(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	clusterID := make([]byte, 32)
	clusterID[0] = 0x30
	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    make([]byte, 20),
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  1,
		NetworkFeeIndex: 0,
		Index:           0,
		IsActive:        true,
		Balance:         big.NewInt(0),
	}
	err := storage.UpsertCluster(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to upsert cluster: %v", err)
	}

	pubkey := make([]byte, 48)
	pubkey[0] = 0x40
	err = storage.InsertValidator(ctx, clusterID, pubkey)
	if err != nil {
		t.Fatalf("Failed to insert validator: %v", err)
	}

	// Verify validator exists
	validators, err := storage.GetActiveValidators(ctx)
	if err != nil {
		t.Fatalf("Failed to get validators: %v", err)
	}
	found := false
	for _, v := range validators {
		if v.ClusterID[0] == 0x30 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Expected to find validator before delete")
	}

	// Delete cluster
	err = storage.DeleteCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("Failed to delete cluster: %v", err)
	}

	// Verify validator was cascade deleted
	validators, err = storage.GetActiveValidators(ctx)
	if err != nil {
		t.Fatalf("Failed to get validators after delete: %v", err)
	}
	for _, v := range validators {
		if v.ClusterID[0] == 0x30 {
			t.Error("Validator should have been cascade deleted")
		}
	}
}

func TestStorage_WALModeEnabled(t *testing.T) {
	storage := setupTestStorage(t)

	var journalMode string
	err := storage.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to query journal_mode: %v", err)
	}

	if journalMode != "wal" {
		t.Errorf("Expected WAL mode, got %s", journalMode)
	}
}

func TestStorage_ForeignKeysEnabled(t *testing.T) {
	storage := setupTestStorage(t)

	var foreignKeys int
	err := storage.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	if err != nil {
		t.Fatalf("Failed to query foreign_keys: %v", err)
	}

	if foreignKeys != 1 {
		t.Errorf("Expected foreign_keys=1, got %d", foreignKeys)
	}
}

func insertCommit(t *testing.T, s *Storage, epoch, block uint64, status CommitStatus) {
	t.Helper()
	root := make([]byte, 32)
	root[0] = byte(epoch)
	balances := []ClusterBalance{{ClusterID: make([]byte, 32), EffectiveBalance: uint32(epoch)}}
	balances[0].ClusterID[0] = byte(epoch)
	if err := s.InsertPendingCommit(context.Background(), epoch, root, block, balances); err != nil {
		t.Fatalf("insert commit epoch %d: %v", epoch, err)
	}
	if status != CommitStatusPending {
		if err := s.UpdateCommitStatus(context.Background(), epoch, status, nil); err != nil {
			t.Fatalf("update commit epoch %d to %s: %v", epoch, status, err)
		}
	}
}

func TestStorage_GetCommitByEpoch(t *testing.T) {
	s := setupTestStorage(t)

	// Insert three commits: epochs 100, 200, 300
	insertCommit(t, s, 100, 500000, CommitStatusConfirmed)
	insertCommit(t, s, 200, 600000, CommitStatusConfirmed)
	insertCommit(t, s, 300, 700000, CommitStatusPending)

	tests := []struct {
		name      string
		epoch     uint64
		wantNil   bool
		wantPrev  *uint64
		wantNext  *uint64
		wantBlock uint64
	}{
		{
			name:      "not found",
			epoch:     999,
			wantNil:   true,
			wantPrev:  nil,
			wantNext:  nil,
			wantBlock: 0,
		},
		{
			name:      "first commit has no prev",
			epoch:     100,
			wantNil:   false,
			wantPrev:  nil,
			wantNext:  ptr(uint64(200)),
			wantBlock: 500000,
		},
		{
			name:      "middle commit has both",
			epoch:     200,
			wantNil:   false,
			wantPrev:  ptr(uint64(100)),
			wantNext:  ptr(uint64(300)),
			wantBlock: 600000,
		},
		{
			name:      "last commit has no next",
			epoch:     300,
			wantNil:   false,
			wantPrev:  ptr(uint64(200)),
			wantNext:  nil,
			wantBlock: 700000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commit, prev, next, err := s.GetCommitByEpoch(context.Background(), tt.epoch)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if commit != nil {
					t.Fatal("expected nil commit")
				}
				return
			}
			if commit == nil {
				t.Fatal("expected commit, got nil")
			}
			if commit.ReferenceBlock != tt.wantBlock {
				t.Errorf("reference block = %d, want %d", commit.ReferenceBlock, tt.wantBlock)
			}
			if commit.TargetEpoch != tt.epoch {
				t.Errorf("target epoch = %d, want %d", commit.TargetEpoch, tt.epoch)
			}
			if !ptrEqual(prev, tt.wantPrev) {
				t.Errorf("prev = %v, want %v", fmtPtr(prev), fmtPtr(tt.wantPrev))
			}
			if !ptrEqual(next, tt.wantNext) {
				t.Errorf("next = %v, want %v", fmtPtr(next), fmtPtr(tt.wantNext))
			}
		})
	}
}

func TestStorage_GetCommitByEpoch_ReturnsAllStatuses(t *testing.T) {
	s := setupTestStorage(t)

	insertCommit(t, s, 100, 500000, CommitStatusPending)
	insertCommit(t, s, 200, 600000, CommitStatusFailed)
	insertCommit(t, s, 300, 700000, CommitStatusConfirmed)

	for _, tt := range []struct {
		epoch  uint64
		status CommitStatus
	}{
		{100, CommitStatusPending},
		{200, CommitStatusFailed},
		{300, CommitStatusConfirmed},
	} {
		commit, _, _, err := s.GetCommitByEpoch(context.Background(), tt.epoch)
		if err != nil {
			t.Fatalf("epoch %d: unexpected error: %v", tt.epoch, err)
		}
		if commit == nil {
			t.Fatalf("epoch %d: expected commit", tt.epoch)
		}
		if commit.Status != tt.status {
			t.Errorf("epoch %d: status = %s, want %s", tt.epoch, commit.Status, tt.status)
		}
	}
}

func TestStorage_GetCommitByEpoch_PrevNextSpanAllStatuses(t *testing.T) {
	s := setupTestStorage(t)

	// prev/next should navigate across all statuses, not just confirmed
	insertCommit(t, s, 100, 500000, CommitStatusFailed)
	insertCommit(t, s, 200, 600000, CommitStatusConfirmed)
	insertCommit(t, s, 300, 700000, CommitStatusPending)

	commit, prev, next, err := s.GetCommitByEpoch(context.Background(), 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commit == nil {
		t.Fatal("expected commit")
	}
	if !ptrEqual(prev, ptr(uint64(100))) {
		t.Errorf("prev = %v, want 100 (failed commit)", fmtPtr(prev))
	}
	if !ptrEqual(next, ptr(uint64(300))) {
		t.Errorf("next = %v, want 300 (pending commit)", fmtPtr(next))
	}
}

func TestStorage_GetCommitByEpoch_SingleCommit(t *testing.T) {
	s := setupTestStorage(t)

	insertCommit(t, s, 100, 500000, CommitStatusConfirmed)

	commit, prev, next, err := s.GetCommitByEpoch(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commit == nil {
		t.Fatal("expected commit")
	}
	if prev != nil {
		t.Errorf("prev = %v, want nil", *prev)
	}
	if next != nil {
		t.Errorf("next = %v, want nil", *next)
	}
}

func TestStorage_GetCommitByEpoch_ClusterBalances(t *testing.T) {
	s := setupTestStorage(t)
	ctx := context.Background()

	balances := []ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
	}
	balances[0].ClusterID[0] = 0xaa
	balances[1].ClusterID[0] = 0xbb

	root := make([]byte, 32)
	if err := s.InsertPendingCommit(ctx, 100, root, 500000, balances); err != nil {
		t.Fatalf("insert commit: %v", err)
	}

	commit, _, _, err := s.GetCommitByEpoch(ctx, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if commit == nil {
		t.Fatal("expected commit")
	}
	if len(commit.ClusterBalances) != 2 {
		t.Fatalf("cluster balances len = %d, want 2", len(commit.ClusterBalances))
	}
	if commit.ClusterBalances[0].EffectiveBalance != 32 {
		t.Errorf("balance[0] = %d, want 32", commit.ClusterBalances[0].EffectiveBalance)
	}
	if commit.ClusterBalances[1].EffectiveBalance != 64 {
		t.Errorf("balance[1] = %d, want 64", commit.ClusterBalances[1].EffectiveBalance)
	}
}

func TestStorage_GetAllClusterInfo(t *testing.T) {
	s := setupTestStorage(t)
	ctx := context.Background()

	// Empty DB
	info, err := s.GetAllClusterInfo(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info) != 0 {
		t.Errorf("expected empty map, got %d entries", len(info))
	}

	// Insert two clusters
	c1 := &ClusterRow{
		ClusterID:    make([]byte, 32),
		OwnerAddress: []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44},
		OperatorIDs:  []uint64{1, 2, 3, 4},
		Balance:      big.NewInt(0),
	}
	c1.ClusterID[0] = 0xaa

	c2 := &ClusterRow{
		ClusterID:    make([]byte, 32),
		OwnerAddress: []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00, 0xff, 0xee, 0xdd, 0xcc},
		OperatorIDs:  []uint64{5, 6, 7, 8},
		Balance:      big.NewInt(0),
	}
	c2.ClusterID[0] = 0xbb

	if err := s.UpsertCluster(ctx, c1); err != nil {
		t.Fatalf("upsert c1: %v", err)
	}
	if err := s.UpsertCluster(ctx, c2); err != nil {
		t.Fatalf("upsert c2: %v", err)
	}

	info, err = s.GetAllClusterInfo(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(info) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(info))
	}

	key1 := fmt.Sprintf("%x", c1.ClusterID)
	if entry, ok := info[key1]; !ok {
		t.Errorf("missing cluster %s", key1)
	} else {
		if len(entry.OperatorIDs) != 4 || entry.OperatorIDs[0] != 1 {
			t.Errorf("c1 operator IDs = %v, want [1 2 3 4]", entry.OperatorIDs)
		}
		if entry.OwnerAddress[0] != 0x11 {
			t.Errorf("c1 owner address[0] = %x, want 11", entry.OwnerAddress[0])
		}
	}

	key2 := fmt.Sprintf("%x", c2.ClusterID)
	if entry, ok := info[key2]; !ok {
		t.Errorf("missing cluster %s", key2)
	} else {
		if len(entry.OperatorIDs) != 4 || entry.OperatorIDs[0] != 5 {
			t.Errorf("c2 operator IDs = %v, want [5 6 7 8]", entry.OperatorIDs)
		}
	}
}

func ptr(v uint64) *uint64 { return &v }

func ptrEqual(a, b *uint64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func fmtPtr(p *uint64) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d", *p)
}

func TestStorage_DecodeOperatorIDs_Malformed(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"invalid json", "not json"},
		{"wrong type", `{"key": "value"}`},
		{"array of strings", `["a", "b", "c"]`},
		{"unclosed bracket", "[1, 2, 3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeOperatorIDs(tt.input)
			if err == nil {
				t.Errorf("decodeOperatorIDs(%q) expected error, got nil", tt.input)
			}
		})
	}
}

func TestStorage_DecodeOperatorIDs_Valid(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []uint64
	}{
		{"empty array", "[]", []uint64{}},
		{"single element", "[1]", []uint64{1}},
		{"multiple elements", "[1,2,3,4]", []uint64{1, 2, 3, 4}},
		{"with spaces", "[1, 2, 3]", []uint64{1, 2, 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeOperatorIDs(tt.input)
			if err != nil {
				t.Fatalf("decodeOperatorIDs(%q) error: %v", tt.input, err)
			}
			if len(got) != len(tt.expect) {
				t.Errorf("len = %d, want %d", len(got), len(tt.expect))
				return
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("got[%d] = %d, want %d", i, got[i], tt.expect[i])
				}
			}
		})
	}
}

// makeTestCluster builds a deterministic ClusterRow keyed off id.
func makeTestCluster(id byte, active bool, balance int64) *ClusterRow {
	clusterID := make([]byte, 32)
	clusterID[0] = id
	owner := make([]byte, 20)
	owner[0] = id
	return &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    owner,
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  uint32(id),
		NetworkFeeIndex: uint64(id) * 10,
		Index:           uint64(id) * 100,
		IsActive:        active,
		Balance:         big.NewInt(balance),
	}
}

func TestStorage_GetFinalizedClusters_ReturnsRequestedRowsAndLastSynced(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	c1 := makeTestCluster(0x01, true, 100)
	c2 := makeTestCluster(0x02, false, 200)
	if err := storage.UpsertCluster(ctx, c1); err != nil {
		t.Fatalf("upsert c1: %v", err)
	}
	if err := storage.UpsertCluster(ctx, c2); err != nil {
		t.Fatalf("upsert c2: %v", err)
	}
	if err := storage.UpdateLastSyncedBlock(ctx, 4242); err != nil {
		t.Fatalf("set last_synced: %v", err)
	}

	rows, lastSynced, err := storage.GetFinalizedClusters(ctx, [][]byte{c1.ClusterID, c2.ClusterID})
	if err != nil {
		t.Fatalf("GetFinalizedClusters: %v", err)
	}
	if lastSynced != 4242 {
		t.Errorf("lastSynced = %d, want 4242", lastSynced)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}

	byID := map[byte]*ClusterRow{}
	for _, r := range rows {
		byID[r.ClusterID[0]] = r
	}
	if got := byID[0x01]; got == nil || got.Balance.Int64() != 100 || !got.IsActive {
		t.Errorf("c1 mismatch: %+v", got)
	}
	if got := byID[0x02]; got == nil || got.Balance.Int64() != 200 || got.IsActive {
		t.Errorf("c2 mismatch: %+v", got)
	}
}

func TestStorage_GetFinalizedClusters_OmitsMissingIDs(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	c1 := makeTestCluster(0x01, true, 100)
	if err := storage.UpsertCluster(ctx, c1); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	missing := make([]byte, 32)
	missing[0] = 0xab

	rows, _, err := storage.GetFinalizedClusters(ctx, [][]byte{c1.ClusterID, missing})
	if err != nil {
		t.Fatalf("GetFinalizedClusters: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (missing id should be omitted)", len(rows))
	}
	if rows[0].ClusterID[0] != 0x01 {
		t.Errorf("returned wrong row: %x", rows[0].ClusterID)
	}
}

func TestStorage_GetFinalizedClusters_EmptyIDsReturnsLastSynced(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	if err := storage.UpdateLastSyncedBlock(ctx, 7); err != nil {
		t.Fatalf("set last_synced: %v", err)
	}

	rows, lastSynced, err := storage.GetFinalizedClusters(ctx, nil)
	if err != nil {
		t.Fatalf("GetFinalizedClusters: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0", len(rows))
	}
	if lastSynced != 7 {
		t.Errorf("lastSynced = %d, want 7", lastSynced)
	}
}

func TestStorage_GetFinalizedClusters_ReadAndSubsequentWriteSucceed(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	c := makeTestCluster(0x01, true, 100)
	if err := storage.UpsertCluster(ctx, c); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := storage.UpdateLastSyncedBlock(ctx, 1000); err != nil {
		t.Fatalf("seed last_synced: %v", err)
	}

	// Open a long-running read tx via a background goroutine that holds
	// the connection while we attempt a concurrent write. The read
	// completes before the write; the snapshot should reflect pre-write
	// state.
	type result struct {
		rows       []*ClusterRow
		lastSynced uint64
		err        error
	}
	done := make(chan result, 1)
	go func() {
		rows, ls, err := storage.GetFinalizedClusters(ctx, [][]byte{c.ClusterID})
		done <- result{rows, ls, err}
	}()

	res := <-done
	if res.err != nil {
		t.Fatalf("GetFinalizedClusters: %v", res.err)
	}
	if len(res.rows) != 1 || res.rows[0].Balance.Int64() != 100 {
		t.Errorf("snapshot row mismatch: %+v", res.rows)
	}
	if res.lastSynced != 1000 {
		t.Errorf("lastSynced = %d, want 1000", res.lastSynced)
	}

	// Sanity: after the snapshot returns, a fresh write goes through.
	flipped := makeTestCluster(0x01, false, 999)
	if err := storage.UpsertCluster(ctx, flipped); err != nil {
		t.Fatalf("post-snapshot write: %v", err)
	}
	got, err := storage.GetCluster(ctx, c.ClusterID)
	if err != nil {
		t.Fatalf("post-write read: %v", err)
	}
	if got.Balance.Int64() != 999 || got.IsActive {
		t.Errorf("post-write row not applied: %+v", got)
	}
}
