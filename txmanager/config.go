package txmanager

import (
	"fmt"
	"math/big"
	"time"
)

// TxPolicy configures transaction submission behavior.
type TxPolicy struct {
	GasBufferPercent     int           `yaml:"gas_buffer_percent"`
	MaxFeePerGas         string        `yaml:"max_fee_per_gas"` // e.g., "100 gwei"
	PendingTimeoutBlocks int           `yaml:"pending_timeout_blocks"`
	GasBumpPercent       int           `yaml:"gas_bump_percent"`
	MaxRetries           int           `yaml:"max_retries"`
	RetryDelay           time.Duration `yaml:"retry_delay"`
}

// ParseMaxFeePerGas parses MaxFeePerGas string (e.g., "100 gwei") into wei.
func (p *TxPolicy) ParseMaxFeePerGas() (*big.Int, error) {
	if p.MaxFeePerGas == "" {
		return nil, nil // No cap
	}

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
	switch unit {
	case "wei":
		multiplier = big.NewInt(1)
	case "gwei":
		multiplier = big.NewInt(1e9)
	case "ether", "eth":
		multiplier = big.NewInt(1e18)
	default:
		return nil, fmt.Errorf("unknown unit: %s", unit)
	}

	// Convert float to big.Int with proper precision
	valueWei := new(big.Float).SetFloat64(value)
	valueWei.Mul(valueWei, new(big.Float).SetInt(multiplier))

	result := new(big.Int)
	valueWei.Int(result)
	return result, nil
}

// Validate checks that all required fields are set and within valid ranges.
func (p *TxPolicy) Validate() error {
	// Check required fields are set (no defaults - must be explicit in config)
	if p.GasBufferPercent == 0 && p.MaxFeePerGas == "" && p.PendingTimeoutBlocks == 0 {
		return fmt.Errorf("tx_policy section is required in config")
	}

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

	if _, err := p.ParseMaxFeePerGas(); err != nil {
		return err
	}

	return nil
}
