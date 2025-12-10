package ethsync

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestNewEventSyncer(t *testing.T) {
	spec := &Spec{
		GenesisTime:   time.Now(),
		SlotDuration:  12 * time.Second,
		SlotsPerEpoch: 32,
	}

	cfg := EventSyncerConfig{
		ExecutionClient: nil, // Would be real client
		Storage:         nil, // Would be real storage
		SSVContract:     common.HexToAddress("0x1234"),
		Spec:            spec,
	}

	syncer, err := NewEventSyncer(cfg)
	if err != nil {
		t.Fatalf("NewEventSyncer failed: %v", err)
	}

	if syncer == nil {
		t.Fatal("NewEventSyncer returned nil")
	}
}

func TestNewEventSyncer_NilSpec(t *testing.T) {
	cfg := EventSyncerConfig{
		ExecutionClient: nil,
		Storage:         nil,
		SSVContract:     common.HexToAddress("0x1234"),
		Spec:            nil, // Missing spec
	}

	_, err := NewEventSyncer(cfg)
	if err == nil {
		t.Fatal("Expected error when spec is nil")
	}
}

func TestComputeClusterIDFromEvent_ValidatorAdded(t *testing.T) {
	event := &ValidatorAddedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		PublicKey:   make([]byte, 48),
		Cluster:     Cluster{ValidatorCount: 1},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for ValidatorAddedEvent")
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
	event := &ValidatorRemovedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		PublicKey:   make([]byte, 48),
		Cluster:     Cluster{ValidatorCount: 0},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for ValidatorRemovedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterLiquidated(t *testing.T) {
	event := &ClusterLiquidatedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Cluster:     Cluster{Active: false},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for ClusterLiquidatedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterReactivated(t *testing.T) {
	event := &ClusterReactivatedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Cluster:     Cluster{Active: true},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for ClusterReactivatedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterWithdrawn(t *testing.T) {
	event := &ClusterWithdrawnEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Value:       big.NewInt(1000),
		Cluster:     Cluster{Balance: big.NewInt(0)},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for ClusterWithdrawnEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterDeposited(t *testing.T) {
	event := &ClusterDepositedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		Value:       big.NewInt(1000),
		Cluster:     Cluster{Balance: big.NewInt(1000)},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for ClusterDepositedEvent")
	}
}

func TestComputeClusterIDFromEvent_ClusterMigratedToETH(t *testing.T) {
	event := &ClusterMigratedToETHEvent{
		Owner:        common.HexToAddress("0xabc123"),
		OperatorIDs:  []uint64{1, 2, 3, 4},
		ETHDeposited: big.NewInt(1000),
		SSVRefunded:  big.NewInt(500),
		Cluster:      Cluster{Balance: big.NewInt(1000)},
	}

	clusterID := computeClusterIDFromEvent(event)
	if clusterID == nil {
		t.Fatal("computeClusterIDFromEvent returned nil for ClusterMigratedToETHEvent")
	}
}

func TestClusterBalanceUpdatedEvent_HasClusterIDDirectly(t *testing.T) {
	var clusterID [32]byte
	copy(clusterID[:], []byte("test-cluster-id-12345678901234"))

	event := &ClusterBalanceUpdatedEvent{
		ClusterID:        clusterID,
		BlockNum:         12345,
		EffectiveBalance: big.NewInt(32000000000),
		VUnits:           1,
		Cluster:          Cluster{},
	}

	// ClusterBalanceUpdatedEvent doesn't implement clusterEvent
	gotClusterID := computeClusterIDFromEvent(event)
	if gotClusterID != nil {
		t.Error("Expected nil from computeClusterIDFromEvent for ClusterBalanceUpdatedEvent")
	}

	if event.ClusterID != clusterID {
		t.Errorf("ClusterID = %x, want %x", event.ClusterID, clusterID)
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
		{"ValidatorAdded", &ValidatorAddedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ValidatorRemoved", &ValidatorRemovedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterLiquidated", &ClusterLiquidatedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterReactivated", &ClusterReactivatedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterWithdrawn", &ClusterWithdrawnEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterDeposited", &ClusterDepositedEvent{Owner: owner, OperatorIDs: operatorIDs}},
		{"ClusterMigratedToETH", &ClusterMigratedToETHEvent{Owner: owner, OperatorIDs: operatorIDs}},
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
	event1 := &ValidatorAddedEvent{Owner: owner, OperatorIDs: operatorIDs}
	event2 := &ClusterLiquidatedEvent{Owner: owner, OperatorIDs: operatorIDs}
	event3 := &ClusterDepositedEvent{Owner: owner, OperatorIDs: operatorIDs}

	id1 := computeClusterIDFromEvent(event1)
	id2 := computeClusterIDFromEvent(event2)
	id3 := computeClusterIDFromEvent(event3)

	if string(id1) != string(id2) {
		t.Error("ValidatorAddedEvent and ClusterLiquidatedEvent should produce same cluster ID")
	}

	if string(id1) != string(id3) {
		t.Error("ValidatorAddedEvent and ClusterDepositedEvent should produce same cluster ID")
	}
}

func TestDifferentClusterIDForDifferentOwners(t *testing.T) {
	operatorIDs := []uint64{1, 2, 3, 4}

	event1 := &ValidatorAddedEvent{
		Owner:       common.HexToAddress("0xabc123"),
		OperatorIDs: operatorIDs,
	}
	event2 := &ValidatorAddedEvent{
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

	event1 := &ValidatorAddedEvent{
		Owner:       owner,
		OperatorIDs: []uint64{1, 2, 3, 4},
	}
	event2 := &ValidatorAddedEvent{
		Owner:       owner,
		OperatorIDs: []uint64{1, 2, 3, 5}, // Different operator
	}

	id1 := computeClusterIDFromEvent(event1)
	id2 := computeClusterIDFromEvent(event2)

	if string(id1) == string(id2) {
		t.Error("Different operator IDs should produce different cluster IDs")
	}
}
