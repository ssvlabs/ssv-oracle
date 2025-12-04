package oracle

import (
	"errors"
	"fmt"
)

// TimingPhase defines a timing configuration phase for oracle rounds.
type TimingPhase struct {
	StartEpoch uint64 `yaml:"start_epoch"`
	Interval   uint64 `yaml:"interval"`
}

// TargetEpoch calculates the target epoch for a given round.
func (p TimingPhase) TargetEpoch(round uint64) uint64 {
	return p.StartEpoch + (round * p.Interval)
}

// ValidateTimingPhases validates the oracle timing configuration.
func ValidateTimingPhases(phases []TimingPhase) error {
	if len(phases) == 0 {
		return errors.New("oracle_timing: at least one phase required")
	}

	for i, p := range phases {
		if p.Interval == 0 {
			return fmt.Errorf("oracle_timing[%d]: interval must be > 0", i)
		}
		if i > 0 && phases[i].StartEpoch <= phases[i-1].StartEpoch {
			return errors.New("oracle_timing: phases must be sorted by start_epoch ascending")
		}
	}

	return nil
}

// GetTimingForEpoch returns the applicable timing phase for a given epoch.
// Returns the last phase where start_epoch <= epoch.
// If all phases are in the future, returns the first phase.
func GetTimingForEpoch(phases []TimingPhase, epoch uint64) TimingPhase {
	for i := len(phases) - 1; i >= 0; i-- {
		if epoch >= phases[i].StartEpoch {
			return phases[i]
		}
	}
	return phases[0]
}

// NextTargetEpoch returns the next target epoch to commit after finalizedEpoch.
// Handles phase transitions correctly.
func NextTargetEpoch(phases []TimingPhase, finalizedEpoch uint64) uint64 {
	phase := GetTimingForEpoch(phases, finalizedEpoch)

	// If we haven't reached this phase yet, first target is phase.StartEpoch
	if finalizedEpoch < phase.StartEpoch {
		return phase.StartEpoch
	}

	// Calculate next target in current phase
	// Round N is finalized when finalizedEpoch > startEpoch + N*interval
	// So next round = floor((finalizedEpoch - startEpoch - 1) / interval) + 1
	var nextTarget uint64
	// Handle exact equality separately to prevent uint64 underflow in subtraction
	if finalizedEpoch == phase.StartEpoch {
		nextTarget = phase.StartEpoch
	} else {
		// Safe: finalizedEpoch > phase.StartEpoch (< case handled above)
		maxFinalizedRound := (finalizedEpoch - phase.StartEpoch - 1) / phase.Interval
		nextTarget = phase.TargetEpoch(maxFinalizedRound + 1)
	}

	// Check if next target crosses into a later phase
	for _, p := range phases {
		if p.StartEpoch > phase.StartEpoch && p.StartEpoch <= nextTarget {
			// Later phase starts at or before our calculated target
			// Use the later phase's start epoch if it's closer
			if p.StartEpoch > finalizedEpoch {
				return p.StartEpoch
			}
		}
	}

	return nextTarget
}

// RoundInPhase calculates the round number for a targetEpoch within its phase.
func RoundInPhase(phase TimingPhase, targetEpoch uint64) uint64 {
	if targetEpoch < phase.StartEpoch {
		return 0
	}
	return (targetEpoch - phase.StartEpoch) / phase.Interval
}
