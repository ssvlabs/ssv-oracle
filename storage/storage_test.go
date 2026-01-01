package storage

import (
	"context"
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
		RawLog:           []byte(`{"topics":[],"data":"0x"}`),
		RawEvent:         []byte(`{"owner":"0x1234","operatorIds":[1,2,3,4]}`),
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

func TestStorage_UpdateClusterIfExists_Missing(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	// Try to update a cluster that doesn't exist
	cluster := &ClusterRow{
		ClusterID:       []byte("nonexistent-cluster-id-here!"),
		NetworkFeeIndex: 100,
		Index:           200,
		IsActive:        true,
		Balance:         big.NewInt(1000),
	}

	// Should not error - just updates 0 rows
	err := storage.UpdateClusterIfExists(ctx, cluster)
	if err != nil {
		t.Errorf("UpdateClusterIfExists() unexpected error: %v", err)
	}

	// Verify cluster was not created
	got, err := storage.GetCluster(ctx, cluster.ClusterID)
	if err != nil {
		t.Fatalf("GetCluster() error: %v", err)
	}
	if got != nil {
		t.Error("Expected nil cluster, but cluster was created")
	}
}

func TestStorage_UpdateClusterIfExists_Existing(t *testing.T) {
	storage := setupTestStorage(t)
	ctx := context.Background()

	// First create a cluster
	clusterID := []byte("test-cluster-id-32-bytes-long!!")
	owner := []byte("owner-address-20-bytes")
	operatorIDs := []uint64{1, 2, 3, 4}

	original := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    owner,
		OperatorIDs:     operatorIDs,
		ValidatorCount:  1,
		NetworkFeeIndex: 100,
		Index:           200,
		IsActive:        true,
		Balance:         big.NewInt(1000),
	}

	err := storage.UpsertCluster(ctx, original)
	if err != nil {
		t.Fatalf("UpsertCluster() error: %v", err)
	}

	// Now update using UpdateClusterIfExists
	updated := &ClusterRow{
		ClusterID:       clusterID,
		NetworkFeeIndex: 500,
		Index:           600,
		IsActive:        false,
		Balance:         big.NewInt(9999),
	}

	err = storage.UpdateClusterIfExists(ctx, updated)
	if err != nil {
		t.Errorf("UpdateClusterIfExists() error: %v", err)
	}

	// Verify the update
	got, err := storage.GetCluster(ctx, clusterID)
	if err != nil {
		t.Fatalf("GetCluster() error: %v", err)
	}
	if got == nil {
		t.Fatal("Expected cluster, got nil")
	}

	if got.NetworkFeeIndex != 500 {
		t.Errorf("NetworkFeeIndex = %d, want 500", got.NetworkFeeIndex)
	}
	if got.Index != 600 {
		t.Errorf("Index = %d, want 600", got.Index)
	}
	if got.IsActive {
		t.Error("IsActive = true, want false")
	}
	if got.Balance.Cmp(big.NewInt(9999)) != 0 {
		t.Errorf("Balance = %v, want 9999", got.Balance)
	}
	// ValidatorCount should remain unchanged
	if got.ValidatorCount != 1 {
		t.Errorf("ValidatorCount = %d, want 1 (unchanged)", got.ValidatorCount)
	}
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
