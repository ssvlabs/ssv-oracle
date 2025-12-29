package syncer

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestNew(t *testing.T) {
	cfg := Config{
		ExecutionClient: nil, // Would be real client
		Storage:         nil, // Would be real storage
		SSVContract:     common.HexToAddress("0x1234"),
	}

	eventSyncer := New(cfg)

	if eventSyncer == nil {
		t.Fatal("New returned nil")
	}
}

func TestComputeClusterIDFromEvent_ValidatorAdded(t *testing.T) {
	event := &validatorAddedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		PublicKey:   make([]byte, 48),
		Cluster:     cluster{ValidatorCount: 1},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for validatorAddedEvent")
	}

	if len(clusterID) != 32 {
		t.Errorf("Expected 32-byte cluster ID, got %d bytes", len(clusterID))
	}

	// Verify determinism
	clusterID2 := computeClusterIDFromEvent(event)
	if string(clusterID) != string(clusterID2) {
		t.Error("computeClusterIDFromEvent is not deterministic")
	}
}

func TestComputeClusterIDFromEvent_ValidatorRemoved(t *testing.T) {
	event := &validatorRemovedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		PublicKey:   make([]byte, 48),
		Cluster:     cluster{ValidatorCount: 0},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for validatorRemovedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterLiquidated(t *testing.T) {
	event := &clusterLiquidatedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Cluster:     cluster{Active: false},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for clusterLiquidatedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterReactivated(t *testing.T) {
	event := &clusterReactivatedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Cluster:     cluster{Active: true},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for clusterReactivatedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterWithdrawn(t *testing.T) {
	event := &clusterWithdrawnEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Value:       big.NewInt(1000),
		Cluster:     cluster{Balance: big.NewInt(0)},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for clusterWithdrawnEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterDeposited(t *testing.T) {
	event := &clusterDepositedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Value:       big.NewInt(1000),
		Cluster:     cluster{Balance: big.NewInt(1000)},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for clusterDepositedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterMigratedToETH(t *testing.T) {
	event := &clusterMigratedToETHEvent{
		Owner:        common.HexToAddress("0xabc123"),
		OperatorIDs:  []uint64{1, 2, 3, 4},
		ETHDeposited: big.NewInt(1000),
		SSVRefunded:  big.NewInt(500),
		Cluster:      cluster{Balance: big.NewInt(1000)},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for clusterMigratedToETHEvent")
	}
}

func TestClusterBalanceUpdatedEvent_ImplementsClusterEvent(t *testing.T) {
	owner := common.HexToAddress("0x1234567890123456789012345678901234567890")
	operatorIDs := []uint64{1, 2, 3, 4}

	event := &clusterBalanceUpdatedEvent{
		Owner:            owner,
		OperatorIDs:      operatorIDs,
		BlockNum:         12345,
		EffectiveBalance: 32,
		Cluster:          cluster{},
	}

	// clusterBalanceUpdatedEvent now implements clusterEvent
	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for clusterBalanceUpdatedEvent")
	}

	// Verify computed cluster ID matches expected
	expectedID := computeClusterID(owner, operatorIDs)
	if !bytes.Equal(clusterID, expectedID[:]) {
		t.Errorf("ClusterID = %x, want %x", clusterID, expectedID)
	}
}

func TestComputeClusterIDFromEvent_UnknownType(t *testing.T) {
	unknownEvent := struct{ foo string }{foo: "bar"}

	clusterID := computeClusterIDFromEvent(unknownEvent)
	if clusterID != nil {
		t.Error("Expected nil cluster ID for unknown event type")
	}
}

func TestClusterKey_AllEventTypes(t *testing.T) {
	owner := common.HexToAddress("0xabc123")
	operatorIDs := []uint64{1, 2, 3, 4}

	tests := []struct {
		name  string
		event clusterEvent
	}{
		{"ValidatorAdded", &validatorAddedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ValidatorRemoved", &validatorRemovedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterLiquidated", &clusterLiquidatedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterReactivated", &clusterReactivatedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterWithdrawn", &clusterWithdrawnEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterDeposited", &clusterDepositedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterMigratedToETH", &clusterMigratedToETHEvent{Owner: owner, OperatorIDs: operatorIDs}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOwner, gotOps := tt.event.clusterKey()
			if gotOwner != owner {
				t.Errorf("clusterKey() owner = %v, want %v", gotOwner, owner)
			}
			if len(gotOps) != len(operatorIDs) {
				t.Errorf("clusterKey() operatorIDs length = %d, want %d", len(gotOps), len(operatorIDs))
			}
			for i, op := range gotOps {
				if op != operatorIDs[i] {
					t.Errorf("clusterKey() operatorIDs[%d] = %d, want %d", i, op, operatorIDs[i])
				}
			}
		})
	}
}

func TestSameClusterIDForSameInputs(t *testing.T) {
	owner := common.HexToAddress("0xabc123")
	operatorIDs := []uint64{1, 2, 3, 4}

	// Different event types with same owner/operators should produce same cluster ID
	event1 := &validatorAddedEvent{Owner: owner, OperatorIDs: operatorIDs}
	event2 := &clusterLiquidatedEvent{Owner: owner, OperatorIDs: operatorIDs}
	event3 := &clusterDepositedEvent{Owner: owner, OperatorIDs: operatorIDs}

	id1 := computeClusterIDFromEvent(event1)
	id2 := computeClusterIDFromEvent(event2)
	id3 := computeClusterIDFromEvent(event3)

	if string(id1) != string(id2) {
		t.Error("validatorAddedEvent and clusterLiquidatedEvent should produce same cluster ID")
	}

	if string(id1) != string(id3) {
		t.Error("validatorAddedEvent and clusterDepositedEvent should produce same cluster ID")
	}
}

func TestDifferentClusterIDForDifferentOwners(t *testing.T) {
	operatorIDs := []uint64{1, 2, 3, 4}

	event1 := &validatorAddedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: operatorIDs,
	}
	event2 := &validatorAddedEvent{
		Owner:       common.HexToAddress("0xdef456"),
		OperatorIDs: operatorIDs,
	}

	id1 := computeClusterIDFromEvent(event1)
	id2 := computeClusterIDFromEvent(event2)

	if string(id1) == string(id2) {
		t.Error("Different owners should produce different cluster IDs")
	}
}

func TestDifferentClusterIDForDifferentOperators(t *testing.T) {
	owner := common.HexToAddress("0xabc123")

	event1 := &validatorAddedEvent{
		Owner:       owner,
		OperatorIDs: []uint64{1, 2, 3, 4},
	}
	event2 := &validatorAddedEvent{
		Owner:       owner,
		OperatorIDs: []uint64{1, 2, 3, 5}, // Different operator
	}

	id1 := computeClusterIDFromEvent(event1)
	id2 := computeClusterIDFromEvent(event2)

	if string(id1) == string(id2) {
		t.Error("Different operator IDs should produce different cluster IDs")
	}
}
