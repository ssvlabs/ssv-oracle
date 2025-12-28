package eth

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

const (
	defaultMaxRetries = 3
	defaultBaseDelay  = 2 * time.Second
	defaultMaxDelay   = 15 * time.Second
)

// RetryConfig holds retry behavior configuration.
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultRetryConfig returns sensible defaults for RPC retry.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: defaultMaxRetries,
		BaseDelay:  defaultBaseDelay,
		MaxDelay:   defaultMaxDelay,
	}
}

// WithRetry executes fn with exponential backoff and jitter.
// Returns nil on success, or the last error after all attempts exhausted.
// MaxRetries specifies the number of retries after the initial attempt,
// so MaxRetries=3 means 4 total attempts.
func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	if cfg.MaxRetries < 0 {
		return fn()
	}

	var err error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if attempt < cfg.MaxRetries {
			delay := cfg.BaseDelay * time.Duration(1<<attempt)
			if delay > cfg.MaxDelay {
				delay = cfg.MaxDelay
			}
			// Add jitter: 0-25% of delay to prevent thundering herd
			if delay > 0 {
				jitter := time.Duration(rand.Int63n(max(int64(delay)/4, 1)))
				delay += jitter
			}

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("after %d attempts: %w", cfg.MaxRetries+1, err)
}
