package beacon

import (
	"testing"
	"time"
)

// newTestSpec creates a Spec with standard Ethereum values for testing.
func newTestSpec(genesisTime time.Time) *Spec {
	return &Spec{
		GenesisTime:   genesisTime,
		SlotsPerEpoch: 32,
		SlotDuration:  12 * time.Second,
	}
}

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
