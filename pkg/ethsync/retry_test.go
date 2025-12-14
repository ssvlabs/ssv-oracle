package ethsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithRetry_Success(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestWithRetry_SuccessAfterRetries(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient error")
		}
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 3, calls)
}

func TestWithRetry_AllAttemptsFail(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0
	testErr := errors.New("persistent error")

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return testErr
	})

	require.Error(t, err)
	require.ErrorIs(t, err, testErr)
	require.Contains(t, err.Error(), "after 3 attempts")
	require.Equal(t, 3, calls)
}

func TestWithRetry_ZeroMaxRetries(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 0, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls, "should execute once even with MaxRetries=0")
}

func TestWithRetry_NegativeMaxRetries(t *testing.T) {
	cfg := RetryConfig{MaxRetries: -1, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls, "should execute once even with negative MaxRetries")
}

func TestWithRetry_ContextCanceled(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 10, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := WithRetry(ctx, cfg, func() error {
		calls++
		return errors.New("always fail")
	})

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls, "should stop after context canceled")
}

func TestWithRetry_OneAttempt(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0
	testErr := errors.New("fail")

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return testErr
	})

	require.Error(t, err)
	require.ErrorIs(t, err, testErr)
	require.Equal(t, 1, calls, "MaxRetries=1 means one attempt, no retry")
}

func TestWithRetry_PermanentError(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 5, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0
	testErr := errors.New("not found")

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		return Permanent(testErr)
	})

	require.Error(t, err)
	require.True(t, IsPermanent(err))
	require.ErrorIs(t, err, testErr)
	require.Equal(t, 1, calls, "permanent errors should not be retried")
}

func TestWithRetry_PermanentAfterTransient(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 5, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := WithRetry(context.Background(), cfg, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient error")
		}
		return Permanent(errors.New("permanent error"))
	})

	require.Error(t, err)
	require.True(t, IsPermanent(err))
	require.Equal(t, 3, calls, "should retry transient, stop on permanent")
}
