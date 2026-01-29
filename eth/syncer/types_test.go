package syncer

import (
	"encoding/hex"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestComputeClusterID(t *testing.T) {
	// Test with example data
	owner := common.HexToAddress("0x1234567890123456789012345678901234567890")
	operatorIDs := []uint64{1, 2, 3, 4}

	clusterID := computeClusterID(owner, operatorIDs)

	// Verify it produces a 32-byte hash
	if len(clusterID) != 32 {
		t.Errorf("Expected 32 bytes, got %d", len(clusterID))
	}

	// Verify it's deterministic (same inputs = same output)
	clusterID2 := computeClusterID(owner, operatorIDs)
	if clusterID != clusterID2 {
		t.Error("computeClusterID is not deterministic")
	}

	// Verify different operators produce different IDs
	differentOps := []uint64{1, 2, 3, 5}
	differentID := computeClusterID(owner, differentOps)
	if clusterID == differentID {
		t.Error("Different operator IDs should produce different cluster IDs")
	}

	// Verify different owner produces different ID
	differentOwner := common.HexToAddress("0x9876543210987654321098765432109876543210")
	differentOwnerID := computeClusterID(differentOwner, operatorIDs)
	if clusterID == differentOwnerID {
		t.Error("Different owners should produce different cluster IDs")
	}

	t.Logf("cluster ID: 0x%s", hex.EncodeToString(clusterID[:]))
}

func TestComputeClusterID_SortingInvariant(t *testing.T) {
	owner := common.HexToAddress("0x1234567890123456789012345678901234567890")

	// Operator IDs in different order should produce the SAME cluster ID (sorted internally)
	ops1 := []uint64{1, 2, 3, 4}
	ops2 := []uint64{4, 3, 2, 1}
	ops3 := []uint64{2, 4, 1, 3}

	id1 := computeClusterID(owner, ops1)
	id2 := computeClusterID(owner, ops2)
	id3 := computeClusterID(owner, ops3)

	if id1 != id2 {
		t.Error("Same operators in different order should produce same cluster ID")
	}
	if id1 != id3 {
		t.Error("Same operators in different order should produce same cluster ID")
	}

	t.Logf("cluster ID (sorted): 0x%s", hex.EncodeToString(id1[:]))
}
