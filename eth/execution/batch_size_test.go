package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdaptiveBatchSize_Decrease(t *testing.T) {
	tests := []struct {
		name     string
		min      uint64
		max      uint64
		initial  uint64
		decrease int
		expected uint64
	}{
		{"halve once", 50, 1000, 1000, 1, 500},
		{"halve twice", 50, 1000, 1000, 2, 250},
		{"halve to min", 100, 1000, 1000, 10, 100},
		{"already at min", 100, 1000, 100, 1, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAdaptiveBatchSize(tt.min, tt.max)
			a.current = tt.initial
			for i := 0; i < tt.decrease; i++ {
				a.Decrease()
			}
			require.Equal(t, tt.expected, a.Get())
		})
	}
}

func TestAdaptiveBatchSize_Increase(t *testing.T) {
	tests := []struct {
		name     string
		min      uint64
		max      uint64
		initial  uint64
		increase int
		expected uint64
	}{
		{"increase once from 100", 50, 1000, 100, 1, 110},  // +10%
		{"increase once from 500", 50, 1000, 500, 1, 550},  // +10% = 50
		{"increase once from 900", 50, 1000, 900, 1, 990},  // +10% = 90
		{"increase capped at max", 50, 1000, 950, 1, 1000}, // would be 1045, capped at 1000
		{"already at max", 50, 1000, 1000, 1, 1000},
		{"large value caps increment", 50, 10000, 2000, 1, 2100}, // +100 (capped)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAdaptiveBatchSize(tt.min, tt.max)
			a.current = tt.initial
			for i := 0; i < tt.increase; i++ {
				a.Increase()
			}
			require.Equal(t, tt.expected, a.Get())
		})
	}
}

func TestAdaptiveBatchSize_AIMD(t *testing.T) {
	// Simulate AIMD: start at max, decrease on error, increase on success
	a := NewAdaptiveBatchSize(50, 1000)
	require.Equal(t, uint64(1000), a.Get())

	// Simulate error - halve
	a.Decrease()
	require.Equal(t, uint64(500), a.Get())

	// Simulate 5 successes - gradual increase
	for i := 0; i < 5; i++ {
		a.Increase()
	}
	// 500 -> 550 -> 605 -> 665 -> 731 -> 804
	require.Greater(t, a.Get(), uint64(750))
	require.Less(t, a.Get(), uint64(850))

	// Another error - halve again
	a.Decrease()
	require.Less(t, a.Get(), uint64(450))
}

func TestAdaptiveBatchSize_Defaults(t *testing.T) {
	// Test with zero values - should use defaults
	a := NewAdaptiveBatchSize(0, 0)
	require.Equal(t, uint64(defaultMinBatchSize), a.min)
	require.Equal(t, uint64(defaultMaxBatchSize), a.max)
	require.Equal(t, uint64(defaultMaxBatchSize), a.Get())
}

func TestAdaptiveBatchSize_MinGreaterThanMax(t *testing.T) {
	// If min > max, min should be set to max
	a := NewAdaptiveBatchSize(1000, 500)
	require.Equal(t, uint64(500), a.min)
	require.Equal(t, uint64(500), a.max)
}
