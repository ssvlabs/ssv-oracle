package txmanager

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/params"
)

const (
	defaultGasBufferPercent     = 60
	defaultMaxFeePerGasGwei     = 420
	defaultPendingTimeoutBlocks = 10
	defaultGasBumpPercent       = 10
	defaultMaxAttempts          = 3
	defaultRetryDelay           = 5 * time.Second
)

// TxPolicy configures transaction submission behavior.
// Zero values are replaced with defaults via ApplyDefaults().
type TxPolicy struct {
	GasBufferPercent     int           `yaml:"gas_buffer_percent"`     // Extra % added to gas estimates (0-100)
	MaxFeePerGasGwei     uint64        `yaml:"max_fee_per_gas_gwei"`   // Hard cap on gas price in Gwei
	PendingTimeoutBlocks int           `yaml:"pending_timeout_blocks"` // Blocks before bumping gas on pending tx
	GasBumpPercent       int           `yaml:"gas_bump_percent"`       // Gas price bump per attempt (min 10%)
	MaxAttempts          int           `yaml:"max_attempts"`           // Total submission attempts
	RetryDelay           time.Duration `yaml:"retry_delay"`            // Delay after RPC error before retry
}

// ApplyDefaults fills in zero values with sensible defaults.
func (p *TxPolicy) ApplyDefaults() {
	if p.GasBufferPercent == 0 {
		p.GasBufferPercent = defaultGasBufferPercent
	}
	if p.MaxFeePerGasGwei == 0 {
		p.MaxFeePerGasGwei = defaultMaxFeePerGasGwei
	}
	if p.PendingTimeoutBlocks == 0 {
		p.PendingTimeoutBlocks = defaultPendingTimeoutBlocks
	}
	if p.GasBumpPercent == 0 {
		p.GasBumpPercent = defaultGasBumpPercent
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = defaultMaxAttempts
	}
	if p.RetryDelay == 0 {
		p.RetryDelay = defaultRetryDelay
	}
}

// MaxFeePerGasWei returns MaxFeePerGasGwei converted to Wei.
func (p *TxPolicy) MaxFeePerGasWei() *big.Int {
	gwei := new(big.Int).SetUint64(p.MaxFeePerGasGwei)
	return gwei.Mul(gwei, big.NewInt(params.GWei))
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
	if p.MaxAttempts < 1 {
		return fmt.Errorf("max_attempts must be at least 1, got %d", p.MaxAttempts)
	}
	if p.RetryDelay < 0 {
		return fmt.Errorf("retry_delay cannot be negative")
	}
	if p.MaxFeePerGasGwei < 1 {
		return fmt.Errorf("max_fee_per_gas_gwei must be at least 1, got %d", p.MaxFeePerGasGwei)
	}
	return nil
}
