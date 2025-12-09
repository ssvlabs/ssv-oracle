package oracle

import "testing"

func TestCommitPhase_TargetEpoch(t *testing.T) {
	phase := CommitPhase{StartEpoch: 100, Interval: 10}

	tests := []struct {
		round    uint64
		expected uint64
	}{
		{0, 100},
		{1, 110},
		{5, 150},
		{10, 200},
	}

	for _, tt := range tests {
		got := phase.TargetEpoch(tt.round)
		if got != tt.expected {
			t.Errorf("TargetEpoch(%d) = %d, want %d", tt.round, got, tt.expected)
		}
	}
}

func TestValidatePhases(t *testing.T) {
	tests := []struct {
		name    string
		phases  []CommitPhase
		wantErr bool
	}{
		{
			name:    "valid single phase",
			phases:  []CommitPhase{{StartEpoch: 100, Interval: 10}},
			wantErr: false,
		},
		{
			name:    "valid multiple phases",
			phases:  []CommitPhase{{StartEpoch: 100, Interval: 10}, {StartEpoch: 200, Interval: 5}},
			wantErr: false,
		},
		{
			name:    "empty phases",
			phases:  []CommitPhase{},
			wantErr: true,
		},
		{
			name:    "zero interval",
			phases:  []CommitPhase{{StartEpoch: 100, Interval: 0}},
			wantErr: true,
		},
		{
			name:    "unsorted phases",
			phases:  []CommitPhase{{StartEpoch: 200, Interval: 10}, {StartEpoch: 100, Interval: 5}},
			wantErr: true,
		},
		{
			name:    "duplicate start epochs",
			phases:  []CommitPhase{{StartEpoch: 100, Interval: 10}, {StartEpoch: 100, Interval: 5}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePhases(tt.phases)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePhases() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetPhaseForEpoch(t *testing.T) {
	phases := []CommitPhase{
		{StartEpoch: 100, Interval: 10},
		{StartEpoch: 200, Interval: 5},
	}

	tests := []struct {
		name          string
		epoch         uint64
		expectedStart uint64
	}{
		{"before first phase", 50, 100},
		{"exactly at first phase start", 100, 100},
		{"between phases", 150, 100},
		{"exactly at second phase start", 200, 200},
		{"after second phase", 300, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := GetPhaseForEpoch(phases, tt.epoch)
			if phase.StartEpoch != tt.expectedStart {
				t.Errorf("GetPhaseForEpoch(%d) = phase starting at %d, want %d",
					tt.epoch, phase.StartEpoch, tt.expectedStart)
			}
		})
	}
}

func TestNextTargetEpoch(t *testing.T) {
	phases := []CommitPhase{
		{StartEpoch: 100, Interval: 10},
	}

	tests := []struct {
		name           string
		finalizedEpoch uint64
		expected       uint64
	}{
		{"before start", 50, 100},
		{"at start", 100, 100},
		{"just after start", 101, 110},
		{"after first target finalized", 111, 120},
		{"mid interval", 115, 120},
		{"at second target", 120, 120},
		{"after second target", 121, 130},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTargetEpoch(phases, tt.finalizedEpoch)
			if got != tt.expected {
				t.Errorf("NextTargetEpoch(%d) = %d, want %d",
					tt.finalizedEpoch, got, tt.expected)
			}
		})
	}
}

func TestNextTargetEpoch_PhaseTransition(t *testing.T) {
	phases := []CommitPhase{
		{StartEpoch: 100, Interval: 10},
		{StartEpoch: 150, Interval: 5},
	}

	// Phase 1: start=100, interval=10 -> targets: 100, 110, 120, 130, 140
	// Phase 2: start=150, interval=5  -> targets: 150, 155, 160, 165, ...
	tests := []struct {
		name           string
		finalizedEpoch uint64
		expected       uint64
	}{
		{"well before transition", 115, 120},
		{"approaching transition", 135, 140},
		{"at transition boundary", 145, 150},       // Jump to new phase
		{"just after transition start", 151, 155},  // In new phase, next target is 155
		{"at first new phase target", 155, 155},    // Target 155, waiting for finalization
		{"after first new phase target", 156, 160}, // Can commit 155, next is 160
		{"in new phase rhythm", 161, 165},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTargetEpoch(phases, tt.finalizedEpoch)
			if got != tt.expected {
				t.Errorf("NextTargetEpoch(%d) = %d, want %d",
					tt.finalizedEpoch, got, tt.expected)
			}
		})
	}
}

func TestRoundInPhase(t *testing.T) {
	phase := CommitPhase{StartEpoch: 100, Interval: 10}

	tests := []struct {
		name        string
		targetEpoch uint64
		expected    uint64
	}{
		{"at phase start", 100, 0},
		{"first round", 110, 1},
		{"second round", 120, 2},
		{"fifth round", 150, 5},
		{"before phase start", 50, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RoundInPhase(phase, tt.targetEpoch)
			if got != tt.expected {
				t.Errorf("RoundInPhase(%d) = %d, want %d", tt.targetEpoch, got, tt.expected)
			}
		})
	}
}
