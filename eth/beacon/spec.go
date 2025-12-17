package beacon

import "time"

// Spec holds beacon chain specification parameters.
type Spec struct {
	GenesisTime   time.Time
	SlotsPerEpoch uint64
	SlotDuration  time.Duration
}

// CurrentSlot returns the current slot based on wall clock time.
func (s *Spec) CurrentSlot() uint64 {
	elapsed := time.Since(s.GenesisTime)
	if elapsed < 0 {
		return 0
	}
	return uint64(elapsed / s.SlotDuration)
}

// CurrentEpoch returns the current epoch based on wall clock time.
func (s *Spec) CurrentEpoch() uint64 {
	return s.CurrentSlot() / s.SlotsPerEpoch
}

// SlotInEpoch returns the slot position within the current epoch (1 to SlotsPerEpoch).
func (s *Spec) SlotInEpoch() uint64 {
	return s.CurrentSlot()%s.SlotsPerEpoch + 1
}
