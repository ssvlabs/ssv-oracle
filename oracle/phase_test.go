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
		{
			name:     "misaligned phases",
			schedule: CommitSchedule{{StartEpoch: 100, Interval: 10}, {StartEpoch: 125, Interval: 5}},
			wantErr:  true,
		},
		{
			name:     "aligned phases",
			schedule: CommitSchedule{{StartEpoch: 100, Interval: 10}, {StartEpoch: 150, Interval: 5}},
			wantErr:  false,
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

func TestCommitSchedule_NextTarget(t *testing.T) {
	schedule := CommitSchedule{
		{StartEpoch: 100, Interval: 10},
	}

	tests := []struct {
		name     string
		epoch    uint64
		expected uint64
	}{
		{"before schedule", 50, 100},
		{"at schedule start", 100, 110},
		{"just after start", 101, 110},
		{"at first target", 110, 120},
		{"between targets", 115, 120},
		{"at second target", 120, 130},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schedule.NextTarget(tt.epoch)
			if got != tt.expected {
				t.Errorf("NextTarget(%d) = %d, want %d",
					tt.epoch, got, tt.expected)
			}
		})
	}
}

func TestCommitSchedule_NextTarget_PhaseTransition(t *testing.T) {
	// Phases must be aligned (enforced by Validate), so phase 2 starts at 150 (a target of phase 1)
	schedule := CommitSchedule{
		{StartEpoch: 100, Interval: 10}, // targets: 100, 110, 120, 130, 140, 150
		{StartEpoch: 150, Interval: 5},  // targets: 150, 155, 160, 165...
	}

	tests := []struct {
		name     string
		epoch    uint64
		expected uint64
	}{
		{"phase 1 - at 140", 140, 150},            // next is 150 (also start of phase 2)
		{"phase 1 - at 145", 145, 150},            // next is 150
		{"phase 2 - at 150", 150, 155},            // now in phase 2, next is 155
		{"phase 2 - at 152", 152, 155},            // still 155
		{"phase 2 - at 155", 155, 160},            // next is 160
		{"phase 1 - before transition", 139, 140}, // still in phase 1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := schedule.NextTarget(tt.epoch)
			if got != tt.expected {
				t.Errorf("NextTarget(%d) = %d, want %d", tt.epoch, got, tt.expected)
			}
		})
	}
}
