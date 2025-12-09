package contract

import (
	"testing"
)

func TestSSVNetworkABI_Loaded(t *testing.T) {
	if ssvNetworkABIJSON == "" {
		t.Fatal("SSVNetwork ABI not loaded")
	}

	// Verify ABI was parsed successfully (happens in init())
	if len(SSVNetworkABI.Methods) == 0 {
		t.Fatal("SSVNetwork ABI has no methods")
	}

	t.Logf("SSVNetwork ABI loaded successfully (%d bytes, %d methods)", len(ssvNetworkABIJSON), len(SSVNetworkABI.Methods))
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
