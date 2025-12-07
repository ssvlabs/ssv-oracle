package oracle

import (
	"errors"
	"fmt"
)

type CommitPhase struct {
	StartEpoch uint64 `yaml:"start_epoch"`
	Interval   uint64 `yaml:"interval"`
}

func (p CommitPhase) TargetEpoch(round uint64) uint64 {
	return p.StartEpoch + (round * p.Interval)
}

func ValidatePhases(phases []CommitPhase) error {
	if len(phases) == 0 {
		return errors.New("commit_phases: at least one phase required")
	}

	for i, p := range phases {
		if p.Interval == 0 {
			return fmt.Errorf("commit_phases[%d]: interval must be > 0", i)
		}
		if i > 0 && phases[i].StartEpoch <= phases[i-1].StartEpoch {
			return errors.New("commit_phases: phases must be sorted by start_epoch ascending")
		}
	}

	return nil
}

func GetPhaseForEpoch(phases []CommitPhase, epoch uint64) CommitPhase {
	for i := len(phases) - 1; i >= 0; i-- {
		if epoch >= phases[i].StartEpoch {
			return phases[i]
		}
	}
	return phases[0]
}

func NextTargetEpoch(phases []CommitPhase, finalizedEpoch uint64) uint64 {
	phase := GetPhaseForEpoch(phases, finalizedEpoch)

	if finalizedEpoch < phase.StartEpoch {
		return phase.StartEpoch
	}

	var nextTarget uint64
	if finalizedEpoch == phase.StartEpoch {
		nextTarget = phase.StartEpoch
	} else {
		maxFinalizedRound := (finalizedEpoch - phase.StartEpoch - 1) / phase.Interval
		nextTarget = phase.TargetEpoch(maxFinalizedRound + 1)
	}

	for _, p := range phases {
		if p.StartEpoch > phase.StartEpoch && p.StartEpoch <= nextTarget {
			if p.StartEpoch > finalizedEpoch {
				return p.StartEpoch
			}
		}
	}

	return nextTarget
}

func RoundInPhase(phase CommitPhase, targetEpoch uint64) uint64 {
	if targetEpoch < phase.StartEpoch {
		return 0
	}
	return (targetEpoch - phase.StartEpoch) / phase.Interval
}
