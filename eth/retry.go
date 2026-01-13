package eth

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/ssvlabs/ssv-oracle/logger"
)

const (
	defaultMaxRetries = 3
	defaultBaseDelay  = 2 * time.Second
	defaultMaxDelay   = 15 * time.Second
)

// RetryConfig holds retry behavior configuration.
type RetryConfig struct {
	MaxRetries uint
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
// MaxRetries is the number of retries after the initial attempt.
// If isRetriable is nil, all errors are retried.
func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error, isRetriable func(error) bool) error {
	var err error
	for attempt := uint(0); attempt <= cfg.MaxRetries; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if isRetriable != nil && !isRetriable(err) {
			return err
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

			logger.Debugw("Retrying after error",
				"attempt", attempt+1,
				"maxAttempts", cfg.MaxRetries+1,
				"delay", delay.Round(time.Millisecond).String(),
				"error", err)

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("after %d attempts: %w", cfg.MaxRetries+1, err)
}
