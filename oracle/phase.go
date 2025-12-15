package oracle

import (
	"errors"
	"fmt"
)

// CommitPhase defines a commit schedule with start epoch and interval.
type CommitPhase struct {
	StartEpoch uint64 `yaml:"start_epoch"`
	Interval   uint64 `yaml:"interval"`
}

// TargetAt returns the target epoch for a given round in this phase.
func (p CommitPhase) TargetAt(round uint64) uint64 {
	return p.StartEpoch + (round * p.Interval)
}

// CommitSchedule represents the complete commit schedule across all phases.
type CommitSchedule []CommitPhase

// Validate checks the schedule configuration.
func (s CommitSchedule) Validate() error {
	if len(s) == 0 {
		return errors.New("commit_phases: at least one phase required")
	}

	for i, p := range s {
		if p.Interval == 0 {
			return fmt.Errorf("commit_phases[%d]: interval must be > 0", i)
		}
		if i > 0 && s[i].StartEpoch <= s[i-1].StartEpoch {
			return errors.New("commit_phases: phases must be sorted by start_epoch ascending")
		}
	}

	return nil
}

// PhaseAt returns the active phase for a given epoch.
func (s CommitSchedule) PhaseAt(epoch uint64) CommitPhase {
	for i := len(s) - 1; i >= 0; i-- {
		if epoch >= s[i].StartEpoch {
			return s[i]
		}
	}
	return s[0]
}

// LatestTarget returns the latest commit target at or before the given epoch.
// Returns 0 if no target exists yet.
func (s CommitSchedule) LatestTarget(epoch uint64) uint64 {
	phase := s.PhaseAt(epoch)

	// Before phase starts, no target exists yet
	if epoch < phase.StartEpoch {
		return 0
	}

	// Find the latest target at or before epoch
	round := (epoch - phase.StartEpoch) / phase.Interval
	return phase.TargetAt(round)
}

// RoundAt returns the round number for a target epoch.
// Panics if targetEpoch is before schedule start (programming error).
func (s CommitSchedule) RoundAt(targetEpoch uint64) uint64 {
	phase := s.PhaseAt(targetEpoch)
	if targetEpoch < phase.StartEpoch {
		panic(fmt.Sprintf("RoundAt: epoch %d is before schedule start %d", targetEpoch, phase.StartEpoch))
	}
	return (targetEpoch - phase.StartEpoch) / phase.Interval
}
