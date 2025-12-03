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

// TestSpec tests the Spec struct methods for slot/epoch calculations
func TestSpec(t *testing.T) {
	// Use Mainnet genesis time: Dec 1, 2020 12:00:23 UTC
	genesisTime := time.Date(2020, 12, 1, 12, 0, 23, 0, time.UTC)
	spec := newTestSpec(genesisTime)

	t.Run("SlotAt", func(t *testing.T) {
		// Slot 0 at genesis
		slot := spec.SlotAt(genesisTime)
		if slot != 0 {
			t.Errorf("Expected slot 0 at genesis, got %d", slot)
		}

		// Slot 1 at genesis + 12 seconds
		slot = spec.SlotAt(genesisTime.Add(12 * time.Second))
		if slot != 1 {
			t.Errorf("Expected slot 1, got %d", slot)
		}

		// Slot 32 at genesis + 384 seconds (1 epoch)
		slot = spec.SlotAt(genesisTime.Add(384 * time.Second))
		if slot != 32 {
			t.Errorf("Expected slot 32, got %d", slot)
		}

		// Before genesis should return 0
		slot = spec.SlotAt(genesisTime.Add(-1 * time.Hour))
		if slot != 0 {
			t.Errorf("Expected slot 0 before genesis, got %d", slot)
		}
	})

	t.Run("EpochAtTimestamp", func(t *testing.T) {
		// Epoch 0 at genesis
		epoch := spec.EpochAtTimestamp(uint64(genesisTime.Unix()))
		if epoch != 0 {
			t.Errorf("Expected epoch 0 at genesis, got %d", epoch)
		}

		// Epoch 1 at genesis + 32 slots (384 seconds)
		epoch = spec.EpochAtTimestamp(uint64(genesisTime.Add(384 * time.Second).Unix()))
		if epoch != 1 {
			t.Errorf("Expected epoch 1, got %d", epoch)
		}

		// Epoch 10 at genesis + 320 slots (3840 seconds)
		epoch = spec.EpochAtTimestamp(uint64(genesisTime.Add(3840 * time.Second).Unix()))
		if epoch != 10 {
			t.Errorf("Expected epoch 10, got %d", epoch)
		}

		// Before genesis should return 0
		epoch = spec.EpochAtTimestamp(uint64(genesisTime.Add(-1 * time.Hour).Unix()))
		if epoch != 0 {
			t.Errorf("Expected epoch 0 before genesis, got %d", epoch)
		}
	})
}
