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

// getTestConnString returns a PostgreSQL connection string for testing.
// IMPORTANT: Uses a separate test database to avoid polluting production data.
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
		dbname = "ssv_oracle_test" // Use separate test database!
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

	// Test basic query
	ctx := context.Background()
	lastBlock, err := storage.GetLastSyncedBlock(ctx)
	if err != nil {
		t.Fatalf("Failed to get last synced block: %v", err)
	}

	// Should be 0 on fresh database
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

	// Update sync progress
	err = storage.UpdateLastSyncedBlock(ctx, 12345)
	if err != nil {
		t.Fatalf("Failed to update last synced block: %v", err)
	}

	// Read it back
	lastBlock, err := storage.GetLastSyncedBlock(ctx)
	if err != nil {
		t.Fatalf("Failed to get last synced block: %v", err)
	}

	if lastBlock != 12345 {
		t.Errorf("Expected last synced block 12345, got %d", lastBlock)
	}
}

func TestPostgresStorage_ValidatorEvent(t *testing.T) {
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

	// Valid 48-byte BLS public key
	pubkey := make([]byte, 48)
	pubkey[0] = 0xaa
	pubkey[1] = 0xbb

	event := &ValidatorEvent{
		ClusterID:       clusterID,
		ValidatorPubkey: pubkey,
		Slot:            3200, // epoch 100 * 32
		LogIndex:        0,
		IsActive:        true,
	}

	// Insert validator event
	err = storage.InsertValidatorEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert validator event: %v", err)
	}

	// Insert again (should be idempotent)
	err = storage.InsertValidatorEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert duplicate validator event: %v", err)
	}
}

func TestPostgresStorage_ValidatorBalance(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	clusterID := make([]byte, 32)
	clusterID[0] = 0x02
	clusterID[1] = 0x03

	// Valid 48-byte BLS public key
	pubkey := make([]byte, 48)
	pubkey[0] = 0xcc
	pubkey[1] = 0xdd

	balance := &ValidatorBalance{
		ClusterID:        clusterID,
		ValidatorPubkey:  pubkey,
		Epoch:            100,
		EffectiveBalance: 32000000000, // 32 ETH in Gwei
	}

	// Insert validator balance
	err = storage.InsertValidatorBalance(ctx, balance)
	if err != nil {
		t.Fatalf("Failed to insert validator balance: %v", err)
	}

	// Update balance (same epoch)
	balance.EffectiveBalance = 31000000000
	err = storage.InsertValidatorBalance(ctx, balance)
	if err != nil {
		t.Fatalf("Failed to update validator balance: %v", err)
	}
}

func TestPostgresStorage_ClusterEvent(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	clusterID := make([]byte, 32)
	clusterID[0] = 0x03
	clusterID[1] = 0x04

	event := &ClusterEvent{
		ClusterID: clusterID,
		Slot:      6400, // epoch 200 * 32
		LogIndex:  1,
		IsActive:  false, // Liquidated
	}

	// Insert cluster event
	err = storage.InsertClusterEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert cluster event: %v", err)
	}

	// Insert again (should be idempotent)
	err = storage.InsertClusterEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert duplicate cluster event: %v", err)
	}
}

func TestPostgresStorage_ClusterState(t *testing.T) {
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

	cluster := &ClusterState{
		ClusterID:       clusterID,
		OwnerAddress:    ownerAddress,
		OperatorIDs:     []uint64{1, 2, 3, 4},
		ValidatorCount:  2,
		NetworkFeeIndex: 12345,
		Index:           67890,
		IsActive:        true,
		Balance:         big.NewInt(1000000000000000000), // 1 SSV token
		LastUpdatedSlot: 100,
	}

	// Upsert cluster state
	err = storage.UpsertClusterState(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to upsert cluster state: %v", err)
	}

	// Update the cluster
	cluster.ValidatorCount = 3
	cluster.IsActive = false
	cluster.LastUpdatedSlot = 101

	err = storage.UpsertClusterState(ctx, cluster)
	if err != nil {
		t.Fatalf("Failed to update cluster state: %v", err)
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

	// Start transaction
	tx, err := storage.BeginTx(ctx)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	clusterID := make([]byte, 32)
	clusterID[0] = 0xaa
	clusterID[1] = 0xbb

	// Valid 48-byte BLS public key
	pubkey := make([]byte, 48)
	pubkey[0] = 0xcc
	pubkey[1] = 0xdd

	event := &ValidatorEvent{
		ClusterID:       clusterID,
		ValidatorPubkey: pubkey,
		Slot:            6400, // epoch 200 * 32
		LogIndex:        0,
		IsActive:        true,
	}

	// Insert in transaction
	err = tx.InsertValidatorEvent(ctx, event)
	if err != nil {
		tx.Rollback()
		t.Fatalf("Failed to insert in tx: %v", err)
	}

	// Commit
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

	// Insert same event again (should be idempotent)
	err = storage.InsertEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert duplicate event: %v", err)
	}
}

func TestPostgresStorage_GetActiveValidatorsWithClusters(t *testing.T) {
	connString := getTestConnString()
	storage, err := NewPostgresStorage(connString)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer storage.Close()

	ctx := context.Background()

	// Clear previous test data for this specific cluster
	clusterID := make([]byte, 32)
	clusterID[0] = 0xf0
	clusterID[1] = 0xf1
	clusterID[2] = 0xf2

	// Valid 48-byte BLS public key
	pubkey := make([]byte, 48)
	pubkey[0] = 0xe0
	pubkey[1] = 0xe1
	pubkey[2] = 0xe2

	// Use epoch 1000, slot = 1000 * 32 = 32000
	addSlot := uint64(32000)
	addEpoch := uint64(1000)

	// Insert validator event (Added) at slot 32000 (epoch 1000)
	event := &ValidatorEvent{
		ClusterID:       clusterID,
		ValidatorPubkey: pubkey,
		Slot:            addSlot,
		LogIndex:        0,
		IsActive:        true,
	}

	err = storage.InsertValidatorEvent(ctx, event)
	if err != nil {
		t.Fatalf("Failed to insert validator event: %v", err)
	}

	// Get active validators at epoch 1000 (using 32 slots per epoch for test)
	slotsPerEpoch := uint64(32)
	validators, err := storage.GetActiveValidatorsWithClusters(ctx, addEpoch, slotsPerEpoch)
	if err != nil {
		t.Fatalf("Failed to get active validators: %v", err)
	}

	// Should contain our validator
	found := false
	for _, v := range validators {
		if v.ClusterID[0] == 0xf0 && v.ValidatorPubkey[0] == 0xe0 {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find our validator in active validators")
	}

	// Now remove the validator at slot 32032 (epoch 1001)
	removeSlot := uint64(32032)
	removeEpoch := uint64(1001)
	removeEvent := &ValidatorEvent{
		ClusterID:       clusterID,
		ValidatorPubkey: pubkey,
		Slot:            removeSlot,
		LogIndex:        0,
		IsActive:        false,
	}

	err = storage.InsertValidatorEvent(ctx, removeEvent)
	if err != nil {
		t.Fatalf("Failed to insert remove event: %v", err)
	}

	// Get active validators at epoch 1001
	validators, err = storage.GetActiveValidatorsWithClusters(ctx, removeEpoch, slotsPerEpoch)
	if err != nil {
		t.Fatalf("Failed to get active validators after removal: %v", err)
	}

	// Should NOT contain our validator
	found = false
	for _, v := range validators {
		if v.ClusterID[0] == 0xf0 && v.ValidatorPubkey[0] == 0xe0 {
			found = true
			break
		}
	}

	if found {
		t.Error("Should NOT find removed validator in active validators")
	}
}
