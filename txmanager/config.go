package txmanager

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	defaultGasBufferPercent     = 20
	defaultMaxFeePerGas         = "100 gwei"
	defaultPendingTimeoutBlocks = 10
	defaultGasBumpPercent       = 10
	defaultMaxRetries           = 3
	defaultRetryDelay           = 5 * time.Second
)

// TxPolicy configures transaction submission behavior.
// Zero values are replaced with defaults via ApplyDefaults().
type TxPolicy struct {
	GasBufferPercent     int           `yaml:"gas_buffer_percent"`     // Extra % added to gas estimates (0-100)
	MaxFeePerGas         string        `yaml:"max_fee_per_gas"`        // Hard cap on gas price, e.g. "100 gwei"
	PendingTimeoutBlocks int           `yaml:"pending_timeout_blocks"` // Blocks before bumping gas on pending tx
	GasBumpPercent       int           `yaml:"gas_bump_percent"`       // Gas price bump per attempt (min 10%)
	MaxRetries           int           `yaml:"max_retries"`            // Max submission attempts
	RetryDelay           time.Duration `yaml:"retry_delay"`            // Delay after RPC error before retry
}

// ApplyDefaults fills in zero values with sensible defaults.
func (p *TxPolicy) ApplyDefaults() {
	if p.GasBufferPercent == 0 {
		p.GasBufferPercent = defaultGasBufferPercent
	}
	if p.MaxFeePerGas == "" {
		p.MaxFeePerGas = defaultMaxFeePerGas
	}
	if p.PendingTimeoutBlocks == 0 {
		p.PendingTimeoutBlocks = defaultPendingTimeoutBlocks
	}
	if p.GasBumpPercent == 0 {
		p.GasBumpPercent = defaultGasBumpPercent
	}
	if p.MaxRetries == 0 {
		p.MaxRetries = defaultMaxRetries
	}
	if p.RetryDelay == 0 {
		p.RetryDelay = defaultRetryDelay
	}
}

// ParseMaxFeePerGas parses MaxFeePerGas string (e.g., "100 gwei") into wei.
func (p *TxPolicy) ParseMaxFeePerGas() (*big.Int, error) {
	var value float64
	var unit string
	_, err := fmt.Sscanf(p.MaxFeePerGas, "%f %s", &value, &unit)
	if err != nil {
		// Try parsing as plain number (wei)
		fee := new(big.Int)
		if _, ok := fee.SetString(p.MaxFeePerGas, 10); ok {
			return fee, nil
		}
		return nil, fmt.Errorf("invalid max_fee_per_gas format: %s", p.MaxFeePerGas)
	}

	var multiplier *big.Int
	switch strings.ToLower(unit) {
	case "wei":
		multiplier = big.NewInt(1)
	case "gwei":
		multiplier = big.NewInt(1e9)
	case "ether", "eth":
		multiplier = big.NewInt(1e18)
	default:
		return nil, fmt.Errorf("unknown unit: %s", unit)
	}

	valueWei := new(big.Float).SetFloat64(value)
	valueWei.Mul(valueWei, new(big.Float).SetInt(multiplier))

	result := new(big.Int)
	valueWei.Int(result)
	return result, nil
}

// Validate checks that all fields are within valid ranges.
// Call ApplyDefaults() before Validate() if you want defaults applied.
func (p *TxPolicy) Validate() error {
	if p.GasBufferPercent < 0 || p.GasBufferPercent > 100 {
		return fmt.Errorf("gas_buffer_percent must be 0-100, got %d", p.GasBufferPercent)
	}
	if p.GasBumpPercent < 10 {
		return fmt.Errorf("gas_bump_percent must be at least 10 (EIP-1559 requirement), got %d", p.GasBumpPercent)
	}
	if p.PendingTimeoutBlocks < 1 {
		return fmt.Errorf("pending_timeout_blocks must be at least 1, got %d", p.PendingTimeoutBlocks)
	}
	if p.MaxRetries < 1 {
		return fmt.Errorf("max_retries must be at least 1, got %d", p.MaxRetries)
	}
	if p.RetryDelay < 0 {
		return fmt.Errorf("retry_delay cannot be negative")
	}
	if p.MaxFeePerGas == "" {
		return fmt.Errorf("max_fee_per_gas is required")
	}

	fee, err := p.ParseMaxFeePerGas()
	if err != nil {
		return err
	}

	minFee := big.NewInt(1e9) // 1 gwei
	if fee.Cmp(minFee) < 0 {
		return fmt.Errorf("max_fee_per_gas must be at least 1 gwei, got %s", p.MaxFeePerGas)
	}

	return nil
}
