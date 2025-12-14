package oracle

import "testing"

func TestCommitPhase_TargetAt(t *testing.T) {
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
		got := phase.TargetAt(tt.round)
		if got != tt.expected {
			t.Errorf("TargetAt(%d) = %d, want %d", tt.round, got, tt.expected)
		}
	}
}

func TestCommitSchedule_Validate(t *testing.T) {
	tests := []struct {
		name     string
		schedule CommitSchedule
		wantErr  bool
	}{
		{
			name:     "valid single phase",
			schedule: CommitSchedule{{StartEpoch: 100, Interval: 10}},
			wantErr:  false,
		},
		{
			name:     "valid multiple phases",
			schedule: CommitSchedule{{StartEpoch: 100, Interval: 10}, {StartEpoch: 200, Interval: 5}},
			wantErr:  false,
		},
		{
			name:     "empty schedule",
			schedule: CommitSchedule{},
			wantErr:  true,
		},
		{
			name:     "zero interval",
			schedule: CommitSchedule{{StartEpoch: 100, Interval: 0}},
			wantErr:  true,
		},
		{
			name:     "unsorted phases",
			schedule: CommitSchedule{{StartEpoch: 200, Interval: 10}, {StartEpoch: 100, Interval: 5}},
			wantErr:  true,
		},
		{
			name:     "duplicate start epochs",
			schedule: CommitSchedule{{StartEpoch: 100, Interval: 10}, {StartEpoch: 100, Interval: 5}},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.schedule.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCommitSchedule_PhaseAt(t *testing.T) {
	schedule := CommitSchedule{
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
			phase := schedule.PhaseAt(tt.epoch)
			if phase.StartEpoch != tt.expectedStart {
				t.Errorf("PhaseAt(%d) = phase starting at %d, want %d",
					tt.epoch, phase.StartEpoch, tt.expectedStart)
			}
		})
	}
}

func TestCommitSchedule_LatestTarget(t *testing.T) {
	schedule := CommitSchedule{
		{StartEpoch: 100, Interval: 10},
	}

	tests := []struct {
		name     string
		epoch    uint64
		expected uint64
	}{
		{"before start", 50, 0},
		{"at start", 100, 100},
		{"just after start", 101, 100},
		{"mid interval", 105, 100},
		{"at second target", 110, 110},
		{"after second target", 115, 110},
		{"at third target", 120, 120},
		{"after third target", 125, 120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schedule.LatestTarget(tt.epoch)
			if got != tt.expected {
				t.Errorf("LatestTarget(%d) = %d, want %d",
					tt.epoch, got, tt.expected)
			}
		})
	}
}

func TestCommitSchedule_LatestTarget_PhaseTransition(t *testing.T) {
	schedule := CommitSchedule{
		{StartEpoch: 100, Interval: 10},
		{StartEpoch: 150, Interval: 5},
	}

	// Phase 1: start=100, interval=10 -> targets: 100, 110, 120, 130, 140
	// Phase 2: start=150, interval=5  -> targets: 150, 155, 160, 165, ...
	tests := []struct {
		name     string
		epoch    uint64
		expected uint64
	}{
		{"in phase 1", 115, 110},
		{"at phase 1 last target", 140, 140},
		{"between phases", 145, 140},
		{"at phase 2 start", 150, 150},
		{"in phase 2", 153, 150},
		{"at phase 2 second target", 155, 155},
		{"after phase 2 second target", 158, 155},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schedule.LatestTarget(tt.epoch)
			if got != tt.expected {
				t.Errorf("LatestTarget(%d) = %d, want %d",
					tt.epoch, got, tt.expected)
			}
		})
	}
}

func TestCommitSchedule_RoundAt(t *testing.T) {
	schedule := CommitSchedule{
		{StartEpoch: 100, Interval: 10},
	}

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
			got := schedule.RoundAt(tt.targetEpoch)
			if got != tt.expected {
				t.Errorf("RoundAt(%d) = %d, want %d", tt.targetEpoch, got, tt.expected)
			}
		})
	}
}

func TestCommitSchedule_RoundAt_PhaseTransition(t *testing.T) {
	schedule := CommitSchedule{
		{StartEpoch: 100, Interval: 10},
		{StartEpoch: 150, Interval: 5},
	}

	tests := []struct {
		name        string
		targetEpoch uint64
		expected    uint64
	}{
		{"phase 1 round 0", 100, 0},
		{"phase 1 round 4", 140, 4},
		{"phase 2 round 0", 150, 0},
		{"phase 2 round 2", 160, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schedule.RoundAt(tt.targetEpoch)
			if got != tt.expected {
				t.Errorf("RoundAt(%d) = %d, want %d", tt.targetEpoch, got, tt.expected)
			}
		})
	}
}
