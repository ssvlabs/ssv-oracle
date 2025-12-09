//go:build integration

package ethsync

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"
)

func getTestConnString() string {
	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("TEST_DB_PORT")
	if port == "" {
		port = "5432"
	}
	dbname := os.Getenv("TEST_DB_NAME")
	if dbname == "" {
		dbname = "ssv_oracle_test"
	}
	user := os.Getenv("TEST_DB_USER")
	if user == "" {
		user = "oracle"
	}
	password := os.Getenv("TEST_DB_PASSWORD")
	if password == "" {
		password = "oracle123"
	}

	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
}

func TestPostgresStorage_Connection(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()
	lastBlock, err := storage.GetLastSyncedBlock(ctx)
	if err != nil {
		t.Fatalf("Failed to get last synced block: %v", err)
	}

	if lastBlock != 0 {
		t.Logf("Last synced block: %d (expected 0 for fresh DB)", lastBlock)
	}
}

func TestPostgresStorage_SyncProgress(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	err = storage.UpdateLastSyncedBlock(ctx, 12345)
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

func TestPostgresStorage_Cluster(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

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
		LastUpdatedSlot: 100,
	}

	err = storage.UpsertCluster(ctx, cluster)
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

	cluster.ValidatorCount = 3
	cluster.IsActive = false
	cluster.LastUpdatedSlot = 101

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

func TestPostgresStorage_Validator(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

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
		LastUpdatedSlot: 0,
	}
	err = storage.UpsertCluster(ctx, cluster)
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

func TestPostgresStorage_Transaction(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

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
		LastUpdatedSlot: 0,
	}
	err = tx.UpsertCluster(ctx, cluster)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to upsert in tx: %v", err)
	}

	pubkey := make([]byte, 48)
	pubkey[0] = 0xcc
	pubkey[1] = 0xdd

	err = tx.InsertValidator(ctx, clusterID, pubkey)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to insert validator in tx: %v", err)
	}

	err = tx.Commit()
	if err != nil {
		t.Fatalf("Failed to commit tx: %v", err)
	}
}

func TestPostgresStorage_InsertEvent(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	event := &ContractEvent{
		EventType:        "ValidatorAdded",
		Slot:             12345,
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

	err = storage.InsertEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	err = storage.InsertEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert duplicate event: %v", err)
	}
}

func TestPostgresStorage_OracleCommit(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	// Use unique values to avoid conflicts with other test runs
	roundID := uint64(time.Now().UnixNano() % 1000000)
	targetEpoch := uint64(100)
	merkleRoot := make([]byte, 32)
	merkleRoot[0] = 0xff
	referenceBlock := uint64(time.Now().UnixNano() % 1000000)

	clusterBalances := []ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32000000000},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64000000000},
	}
	clusterBalances[0].ClusterID[0] = 0x01
	clusterBalances[1].ClusterID[0] = 0x02

	// Insert pending commit (no tx hash yet)
	err = storage.InsertPendingCommit(ctx, roundID, targetEpoch, merkleRoot, referenceBlock, clusterBalances)
	if err != nil {
		t.Fatalf("Failed to insert pending commit: %v", err)
	}

	// Get commit by reference block
	commit, err := storage.GetCommitByBlock(ctx, referenceBlock)
	if err != nil {
		t.Fatalf("Failed to get commit by block: %v", err)
	}
	if commit == nil {
		t.Fatal("Expected commit, got nil")
	}
	if commit.RoundID != roundID {
		t.Errorf("Expected round ID %d, got %d", roundID, commit.RoundID)
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

	// Update commit status to confirmed
	txHash := make([]byte, 32)
	txHash[0] = 0xee
	err = storage.UpdateCommitStatus(ctx, roundID, CommitStatusConfirmed, txHash)
	if err != nil {
		t.Fatalf("Failed to update commit status: %v", err)
	}

	// Verify status was updated
	commit, err = storage.GetCommitByBlock(ctx, referenceBlock)
	if err != nil {
		t.Fatalf("Failed to get commit after status update: %v", err)
	}
	if commit.Status != CommitStatusConfirmed {
		t.Errorf("Expected status %s, got %s", CommitStatusConfirmed, commit.Status)
	}
	if commit.TxHash[0] != 0xee {
		t.Errorf("Expected tx hash to be set")
	}

	// Test idempotency - inserting same roundID again should not error
	err = storage.InsertPendingCommit(ctx, roundID, targetEpoch, merkleRoot, referenceBlock, clusterBalances)
	if err != nil {
		t.Fatalf("InsertPendingCommit should be idempotent, got error: %v", err)
	}
}
