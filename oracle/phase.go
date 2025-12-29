package oracle

import "errors"

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
			return errors.New("commit_phases: interval must be > 0")
		}
		if i > 0 {
			prev := s[i-1]
			if p.StartEpoch <= prev.StartEpoch {
				return errors.New("commit_phases: phases must be sorted by start_epoch ascending")
			}
			// Ensure phase transition aligns: StartEpoch must be a target of previous phase
			if (p.StartEpoch-prev.StartEpoch)%prev.Interval != 0 {
				return errors.New("commit_phases: start_epoch must align with previous phase interval")
			}
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

// NextTarget returns the next commit target after the given epoch.
// If epoch is before the schedule starts, returns the first target.
func (s CommitSchedule) NextTarget(epoch uint64) uint64 {
	phase := s.PhaseAt(epoch)

	if epoch < phase.StartEpoch {
		return phase.StartEpoch
	}

	round := (epoch - phase.StartEpoch) / phase.Interval
	return phase.TargetAt(round + 1)
}
