package txmanager

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

func TestTxPolicy_Validate(t *testing.T) {
	tests := []struct {
		name    string
		policy  *TxPolicy
		wantErr bool
	}{
		{
			name: "valid policy",
			policy: &TxPolicy{
				GasBufferPercent:     20,
				MaxFeePerGasGwei:     100,
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           3,
				RetryDelay:           5 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "gas bump too low",
			policy: &TxPolicy{
				GasBufferPercent:     20,
				MaxFeePerGasGwei:     100,
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       5, // Must be >= 10 for EIP-1559
				MaxRetries:           3,
				RetryDelay:           5 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "zero retries",
			policy: &TxPolicy{
				GasBufferPercent:     20,
				MaxFeePerGasGwei:     100,
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           0,
				RetryDelay:           5 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "missing max fee",
			policy: &TxPolicy{
				GasBufferPercent:     20,
				MaxFeePerGasGwei:     0, // Required
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           3,
			},
			wantErr: true,
		},
		{
			name: "gas buffer too high",
			policy: &TxPolicy{
				GasBufferPercent:     150, // Max is 100
				MaxFeePerGasGwei:     100,
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           3,
			},
			wantErr: true,
		},
		{
			name: "negative gas buffer",
			policy: &TxPolicy{
				GasBufferPercent:     -10,
				MaxFeePerGasGwei:     100,
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           3,
			},
			wantErr: true,
		},
		{
			name: "zero pending timeout",
			policy: &TxPolicy{
				GasBufferPercent:     20,
				MaxFeePerGasGwei:     100,
				PendingTimeoutBlocks: 0,
				GasBumpPercent:       10,
				MaxRetries:           3,
			},
			wantErr: true,
		},
		{
			name: "negative retry delay",
			policy: &TxPolicy{
				GasBufferPercent:     20,
				MaxFeePerGasGwei:     100,
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           3,
				RetryDelay:           -1 * time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTxPolicy_ApplyDefaults(t *testing.T) {
	// Empty policy should get all defaults
	policy := &TxPolicy{}
	policy.ApplyDefaults()

	if err := policy.Validate(); err != nil {
		t.Errorf("ApplyDefaults() should make policy valid, got error: %v", err)
	}

	// Explicit values should not be overwritten
	policy2 := &TxPolicy{
		GasBufferPercent: 50,
		MaxFeePerGasGwei: 200,
		MaxRetries:       5,
	}
	policy2.ApplyDefaults()

	if policy2.GasBufferPercent != 50 {
		t.Errorf("GasBufferPercent should remain 50, got %d", policy2.GasBufferPercent)
	}
	if policy2.MaxFeePerGasGwei != 200 {
		t.Errorf("MaxFeePerGasGwei should remain 200, got %d", policy2.MaxFeePerGasGwei)
	}
	if policy2.MaxRetries != 5 {
		t.Errorf("MaxRetries should remain 5, got %d", policy2.MaxRetries)
	}
	// Other fields should get defaults
	if policy2.PendingTimeoutBlocks != defaultPendingTimeoutBlocks {
		t.Errorf("PendingTimeoutBlocks should be default, got %d", policy2.PendingTimeoutBlocks)
	}
}

func TestTxPolicy_MaxFeePerGasWei(t *testing.T) {
	tests := []struct {
		name     string
		gwei     uint64
		expected int64 // in wei
	}{
		{"100 gwei", 100, 100 * params.GWei},
		{"1 gwei", 1, params.GWei},
		{"50 gwei", 50, 50 * params.GWei},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &TxPolicy{MaxFeePerGasGwei: tt.gwei}
			got := policy.MaxFeePerGasWei()
			require.Equal(t, tt.expected, got.Int64())
		})
	}
}
