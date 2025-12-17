package txmanager

import (
	"testing"
	"time"
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
				MaxFeePerGas:         "100 gwei",
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
				MaxFeePerGas:         "100 gwei",
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
				MaxFeePerGas:         "100 gwei",
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
				MaxFeePerGas:         "100 gwei",
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
				MaxFeePerGas:         "100 gwei",
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
				MaxFeePerGas:         "100 gwei",
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
				MaxFeePerGas:         "100 gwei",
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           3,
				RetryDelay:           -1 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "invalid max fee format",
			policy: &TxPolicy{
				GasBufferPercent:     20,
				MaxFeePerGas:         "invalid",
				PendingTimeoutBlocks: 10,
				GasBumpPercent:       10,
				MaxRetries:           3,
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

func TestDefaultTxPolicy(t *testing.T) {
	policy := DefaultTxPolicy()
	if err := policy.Validate(); err != nil {
		t.Errorf("DefaultTxPolicy() should be valid, got error: %v", err)
	}

	if policy.GasBufferPercent != DefaultGasBufferPercent {
		t.Errorf("GasBufferPercent = %d, want %d", policy.GasBufferPercent, DefaultGasBufferPercent)
	}
	if policy.MaxFeePerGas != DefaultMaxFeePerGas {
		t.Errorf("MaxFeePerGas = %s, want %s", policy.MaxFeePerGas, DefaultMaxFeePerGas)
	}
	if policy.PendingTimeoutBlocks != DefaultPendingTimeoutBlocks {
		t.Errorf("PendingTimeoutBlocks = %d, want %d", policy.PendingTimeoutBlocks, DefaultPendingTimeoutBlocks)
	}
	if policy.GasBumpPercent != DefaultGasBumpPercent {
		t.Errorf("GasBumpPercent = %d, want %d", policy.GasBumpPercent, DefaultGasBumpPercent)
	}
	if policy.MaxRetries != DefaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", policy.MaxRetries, DefaultMaxRetries)
	}
	if policy.RetryDelay != DefaultRetryDelay {
		t.Errorf("RetryDelay = %v, want %v", policy.RetryDelay, DefaultRetryDelay)
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
		MaxFeePerGas:     "200 gwei",
		MaxRetries:       5,
	}
	policy2.ApplyDefaults()

	if policy2.GasBufferPercent != 50 {
		t.Errorf("GasBufferPercent should remain 50, got %d", policy2.GasBufferPercent)
	}
	if policy2.MaxFeePerGas != "200 gwei" {
		t.Errorf("MaxFeePerGas should remain '200 gwei', got %s", policy2.MaxFeePerGas)
	}
	if policy2.MaxRetries != 5 {
		t.Errorf("MaxRetries should remain 5, got %d", policy2.MaxRetries)
	}
	// Other fields should get defaults
	if policy2.PendingTimeoutBlocks != DefaultPendingTimeoutBlocks {
		t.Errorf("PendingTimeoutBlocks should be default, got %d", policy2.PendingTimeoutBlocks)
	}
}

func TestTxPolicy_ParseMaxFeePerGas(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int64 // in wei
		wantErr  bool
		wantNil  bool
	}{
		{"100 gwei", "100 gwei", 100_000_000_000, false, false},
		{"1 gwei", "1 gwei", 1_000_000_000, false, false},
		{"0.5 gwei", "0.5 gwei", 500_000_000, false, false},
		{"1 wei", "1 wei", 1, false, false},
		{"plain number as wei", "1000000000", 1_000_000_000, false, false},
		{"empty string errors", "", 0, true, false},
		{"invalid format", "invalid", 0, true, false},
		{"unknown unit", "100 foo", 0, true, false},
		{"1 ether", "1 ether", 1_000_000_000_000_000_000, false, false},
		{"1 eth", "1 eth", 1_000_000_000_000_000_000, false, false},
		{"case insensitive GWEI", "100 GWEI", 100_000_000_000, false, false},
		{"case insensitive Gwei", "50 Gwei", 50_000_000_000, false, false},
		{"case insensitive WEI", "1000 WEI", 1000, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &TxPolicy{MaxFeePerGas: tt.input}
			got, err := policy.ParseMaxFeePerGas()
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMaxFeePerGas() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantNil {
				if got != nil {
					t.Errorf("ParseMaxFeePerGas() = %v, want nil", got)
				}
				return
			}
			if !tt.wantErr && got != nil && got.Int64() != tt.expected {
				t.Errorf("ParseMaxFeePerGas() = %d, want %d", got.Int64(), tt.expected)
			}
		})
	}
}
