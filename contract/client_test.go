package contract

import (
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// Event name constants for ABI validation tests.
// Duplicated here to avoid import cycle with ethsync.
const (
	eventValidatorAdded        = "ValidatorAdded"
	eventValidatorRemoved      = "ValidatorRemoved"
	eventClusterLiquidated     = "ClusterLiquidated"
	eventClusterReactivated    = "ClusterReactivated"
	eventClusterWithdrawn      = "ClusterWithdrawn"
	eventClusterDeposited      = "ClusterDeposited"
	eventClusterMigratedToETH  = "ClusterMigratedToETH"
	eventClusterBalanceUpdated = "ClusterBalanceUpdated"
)

func TestSSVNetworkABI_Loaded(t *testing.T) {
	if ssvNetworkABIJSON == "" {
		t.Fatal("SSVNetwork ABI not loaded")
	}

	if len(SSVNetworkABI.Methods) == 0 {
		t.Fatal("SSVNetwork ABI has no methods")
	}

	t.Logf("SSVNetwork ABI loaded successfully (%d bytes, %d methods)", len(ssvNetworkABIJSON), len(SSVNetworkABI.Methods))
}

func TestSSVNetworkABI_Events(t *testing.T) {
	tests := []struct {
		name            string
		indexedCount    int
		nonIndexedCount int
	}{
		{eventRootCommitted, 2, 0},
		{eventValidatorAdded, 1, 4},
		{eventValidatorRemoved, 1, 3},
		{eventClusterLiquidated, 1, 2},
		{eventClusterReactivated, 1, 2},
		{eventClusterWithdrawn, 1, 3},
		{eventClusterDeposited, 1, 3},
		{eventClusterMigratedToETH, 1, 5},
		{eventClusterBalanceUpdated, 2, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, ok := SSVNetworkABI.Events[tt.name]
			if !ok {
				t.Fatalf("Event %s not found in ABI", tt.name)
			}

			indexed := 0
			nonIndexed := 0
			for _, input := range event.Inputs {
				if input.Indexed {
					indexed++
				} else {
					nonIndexed++
				}
			}

			if indexed != tt.indexedCount {
				t.Errorf("Event %s: expected %d indexed params, got %d", tt.name, tt.indexedCount, indexed)
			}
			if nonIndexed != tt.nonIndexedCount {
				t.Errorf("Event %s: expected %d non-indexed params, got %d", tt.name, tt.nonIndexedCount, nonIndexed)
			}
		})
	}
}

func TestSSVNetworkABI_RootCommittedSignature(t *testing.T) {
	event, ok := SSVNetworkABI.Events[eventRootCommitted]
	if !ok {
		t.Fatalf("%s event not found in ABI", eventRootCommitted)
	}

	// Verify the event signature matches what we expect
	// RootCommitted(bytes32 indexed merkleRoot, uint64 indexed blockNum)
	expectedSig := crypto.Keccak256Hash([]byte("RootCommitted(bytes32,uint64)"))
	if event.ID != expectedSig {
		t.Errorf("RootCommitted signature mismatch:\n  expected: %s\n  got:      %s", expectedSig.Hex(), event.ID.Hex())
	}

	// Verify inputs
	if len(event.Inputs) != 2 {
		t.Fatalf("Expected 2 inputs, got %d", len(event.Inputs))
	}

	// First input: merkleRoot (bytes32, indexed)
	if event.Inputs[0].Name != "merkleRoot" {
		t.Errorf("Expected first input name 'merkleRoot', got '%s'", event.Inputs[0].Name)
	}
	if event.Inputs[0].Type.String() != "bytes32" {
		t.Errorf("Expected first input type 'bytes32', got '%s'", event.Inputs[0].Type.String())
	}
	if !event.Inputs[0].Indexed {
		t.Error("Expected first input to be indexed")
	}

	// Second input: blockNum (uint64, indexed)
	if event.Inputs[1].Name != "blockNum" {
		t.Errorf("Expected second input name 'blockNum', got '%s'", event.Inputs[1].Name)
	}
	if event.Inputs[1].Type.String() != "uint64" {
		t.Errorf("Expected second input type 'uint64', got '%s'", event.Inputs[1].Type.String())
	}
	if !event.Inputs[1].Indexed {
		t.Error("Expected second input to be indexed")
	}
}

func TestCluster(t *testing.T) {
	cluster := &Cluster{
		ValidatorCount:  10,
		NetworkFeeIndex: 100,
		Index:           1,
		Active:          true,
		Balance:         nil, // Will be set dynamically
	}

	if cluster.ValidatorCount != 10 {
		t.Errorf("Expected ValidatorCount=10, got %d", cluster.ValidatorCount)
	}

	t.Logf("Cluster: validatorCount=%d, active=%v", cluster.ValidatorCount, cluster.Active)
}
