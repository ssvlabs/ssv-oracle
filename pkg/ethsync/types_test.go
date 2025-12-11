package ethsync

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// newTestSpec creates a Spec with standard Ethereum values for testing.
func newTestSpec(genesisTime time.Time) *Spec {
	return &Spec{
		GenesisTime:   genesisTime,
		SlotsPerEpoch: 32,
		SlotDuration:  12 * time.Second,
	}
}

func TestComputeClusterID(t *testing.T) {
	// Test with example data
	owner := common.HexToAddress("0x1234567890123456789012345678901234567890")
	operatorIDs := []uint64{1, 2, 3, 4}

	clusterID := ComputeClusterID(owner, operatorIDs)

	// Verify it produces a 32-byte hash
	if len(clusterID) != 32 {
		t.Errorf("Expected 32 bytes, got %d", len(clusterID))
	}

	// Verify it's deterministic (same inputs = same output)
	clusterID2 := ComputeClusterID(owner, operatorIDs)
	if clusterID != clusterID2 {
		t.Error("ComputeClusterID is not deterministic")
	}

	// Verify different operators produce different IDs
	differentOps := []uint64{1, 2, 3, 5}
	differentID := ComputeClusterID(owner, differentOps)
	if clusterID == differentID {
		t.Error("Different operator IDs should produce different cluster IDs")
	}

	// Verify different owner produces different ID
	differentOwner := common.HexToAddress("0x9876543210987654321098765432109876543210")
	differentOwnerID := ComputeClusterID(differentOwner, operatorIDs)
	if clusterID == differentOwnerID {
		t.Error("Different owners should produce different cluster IDs")
	}

	t.Logf("Cluster ID: 0x%s", hex.EncodeToString(clusterID[:]))
}

func TestComputeClusterID_SortingInvariant(t *testing.T) {
	owner := common.HexToAddress("0x1234567890123456789012345678901234567890")

	// Operator IDs in different order should produce the SAME cluster ID (sorted internally)
	ops1 := []uint64{1, 2, 3, 4}
	ops2 := []uint64{4, 3, 2, 1}
	ops3 := []uint64{2, 4, 1, 3}

	id1 := ComputeClusterID(owner, ops1)
	id2 := ComputeClusterID(owner, ops2)
	id3 := ComputeClusterID(owner, ops3)

	if id1 != id2 {
		t.Error("Same operators in different order should produce same cluster ID")
	}
	if id1 != id3 {
		t.Error("Same operators in different order should produce same cluster ID")
	}

	t.Logf("Cluster ID (sorted): 0x%s", hex.EncodeToString(id1[:]))
}

// TestSpec_CurrentEpoch tests the CurrentEpoch method
func TestSpec_CurrentEpoch(t *testing.T) {
	// Use a genesis time 10 epochs ago
	epochDuration := 32 * 12 * time.Second // 384 seconds per epoch
	genesisTime := time.Now().Add(-10 * epochDuration)
	spec := newTestSpec(genesisTime)

	epoch := spec.CurrentEpoch()
	if epoch < 10 || epoch > 11 {
		t.Errorf("Expected epoch around 10, got %d", epoch)
	}

	// Test with future genesis (should return 0)
	futureSpec := newTestSpec(time.Now().Add(1 * time.Hour))
	futureEpoch := futureSpec.CurrentEpoch()
	if futureEpoch != 0 {
		t.Errorf("Expected epoch 0 for future genesis, got %d", futureEpoch)
	}
}
