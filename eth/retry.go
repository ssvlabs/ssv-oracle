package eth

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

const (
	defaultMaxRetries = 3
	defaultBaseDelay  = 2 * time.Second
	defaultMaxDelay   = 15 * time.Second
)

// ErrPermanent wraps an error to indicate it should not be retried.
type ErrPermanent struct {
	Err error
}

func (e *ErrPermanent) Error() string { return e.Err.Error() }
func (e *ErrPermanent) Unwrap() error { return e.Err }

// Permanent marks an error as non-retryable.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &ErrPermanent{Err: err}
}

// IsPermanent returns true if the error should not be retried.
func IsPermanent(err error) bool {
	var permErr *ErrPermanent
	return errors.As(err, &permErr)
}

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
// Errors wrapped with Permanent() are not retried.
func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	if cfg.MaxRetries <= 0 {
		return fn()
	}

	var err error
	for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if IsPermanent(err) {
			return err
		}
		if attempt < cfg.MaxRetries-1 {
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
	return fmt.Errorf("after %d attempts: %w", cfg.MaxRetries, err)
}
