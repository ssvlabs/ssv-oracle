package execution

import "sync"

const (
	defaultMinBatchSize = 200
	defaultMaxBatchSize = 10000
)

// AdaptiveBatchSize adjusts batch size based on RPC responses.
// Uses AIMD (Additive Increase, Multiplicative Decrease) strategy:
// - On success: increase by min(10% of current, 100), minimum +1, up to max
// - On batch-related errors (block range, rate limit, timeout): halve, down to min
type AdaptiveBatchSize struct {
	current uint64
	min     uint64
	max     uint64
	mu      sync.Mutex
}

// NewAdaptiveBatchSize creates a new adaptive batch size starting at max.
func NewAdaptiveBatchSize(min, max uint64) *AdaptiveBatchSize {
	if min == 0 {
		min = defaultMinBatchSize
	}
	if max == 0 {
		max = defaultMaxBatchSize
	}
	if min > max {
		min = max
	}
	return &AdaptiveBatchSize{
		current: max,
		min:     min,
		max:     max,
	}
}

// Get returns the current batch size.
func (a *AdaptiveBatchSize) Get() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

// Decrease halves the batch size on error (multiplicative decrease).
// Returns the new batch size.
func (a *AdaptiveBatchSize) Decrease() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.current = a.current / 2
	if a.current < a.min {
		a.current = a.min
	}
	return a.current
}

// Increase grows the batch size on success (additive increase).
// Adds 10% of current or 100, whichever is smaller.
// Returns the new batch size.
func (a *AdaptiveBatchSize) Increase() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	increment := a.current / 10
	if increment > 100 {
		increment = 100
	}
	if increment < 1 {
		increment = 1
	}
	a.current += increment
	if a.current > a.max {
		a.current = a.max
	}
	return a.current
}
