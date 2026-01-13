package eth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/stretchr/testify/require"

	"github.com/ssvlabs/ssv-oracle/eth"
	"github.com/ssvlabs/ssv-oracle/eth/beacon"
)

func TestWithRetry_Success(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := eth.WithRetry(context.Background(), cfg, func() error {
		calls++
		return nil
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestWithRetry_SuccessAfterRetries(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := eth.WithRetry(context.Background(), cfg, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient error")
		}
		return nil
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 3, calls)
}

func TestWithRetry_AllAttemptsFail(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0
	testErr := errors.New("persistent error")

	err := eth.WithRetry(context.Background(), cfg, func() error {
		calls++
		return testErr
	}, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, testErr)
	require.Contains(t, err.Error(), "after 4 attempts")
	require.Equal(t, 4, calls, "MaxRetries=3 means 4 total attempts (1 initial + 3 retries)")
}

func TestWithRetry_ZeroMaxRetries(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 0, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	err := eth.WithRetry(context.Background(), cfg, func() error {
		calls++
		return nil
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 1, calls, "should execute once even with MaxRetries=0")
}

func TestWithRetry_ContextCanceled(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 10, BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := eth.WithRetry(ctx, cfg, func() error {
		calls++
		return errors.New("always fail")
	}, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, calls, "should stop after context canceled")
}

func TestWithRetry_OneRetry(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0
	testErr := errors.New("fail")

	err := eth.WithRetry(context.Background(), cfg, func() error {
		calls++
		return testErr
	}, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, testErr)
	require.Equal(t, 2, calls, "MaxRetries=1 means 2 total attempts (1 initial + 1 retry)")
}

func TestWithRetry_NonRetriableErrors(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}

	tests := []struct {
		name        string
		statusCode  int
		shouldRetry bool
	}{
		{"400 Bad Request", 400, false},
		{"404 Not Found", 404, false},
		{"414 URI Too Long", 414, false},
		{"429 Too Many Requests", 429, true},
		{"500 Internal Server Error", 500, true},
		{"502 Bad Gateway", 502, true},
		{"503 Service Unavailable", 503, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			apiErr := api.Error{
				Method:     "POST",
				Endpoint:   "/eth/v1/beacon/states/finalized/validators",
				StatusCode: tt.statusCode,
				Data:       []byte(fmt.Sprintf(`{"code":%d,"message":"test error"}`, tt.statusCode)),
			}

			err := eth.WithRetry(context.Background(), cfg, func() error {
				calls++
				return &apiErr
			}, beacon.IsRetriable)

			require.Error(t, err)
			if tt.shouldRetry {
				require.Equal(t, 4, calls, "retriable errors should exhaust all attempts")
				require.Contains(t, err.Error(), "after 4 attempts")
			} else {
				require.Equal(t, 1, calls, "non-retriable errors should fail immediately")
				require.NotContains(t, err.Error(), "after")
			}
		})
	}
}

func TestWithRetry_WrappedNonRetriableError(t *testing.T) {
	cfg := eth.RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}
	calls := 0

	apiErr := &api.Error{
		Method:     "POST",
		Endpoint:   "/eth/v1/beacon/states/finalized/validators",
		StatusCode: 404,
		Data:       []byte(`{"code":404,"message":"State not found"}`),
	}
	wrappedErr := fmt.Errorf("get validators: %w", apiErr)

	err := eth.WithRetry(context.Background(), cfg, func() error {
		calls++
		return wrappedErr
	}, beacon.IsRetriable)

	require.Error(t, err)
	require.Equal(t, 1, calls, "wrapped non-retriable errors should fail immediately")
}
