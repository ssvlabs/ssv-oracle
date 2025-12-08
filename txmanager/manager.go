package txmanager

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"ssv-oracle/pkg/logger"
	"ssv-oracle/wallet"
)

var (
	ErrMaxGasReached       = errors.New("max gas price reached, tx cancelled")
	ErrMaxRetriesExhausted = errors.New("max retries exhausted")
)

const (
	receiptPollInterval = 2 * time.Second
	simpleTransferGas   = 21000
)

// errorSelectors maps custom error selectors to human-readable names.
// Set via SetErrorSelectors, typically from contract.ErrorSelectors.
var errorSelectors map[string]string

// SetErrorSelectors sets the error selectors map for decoding revert reasons.
// Should be called at startup with contract.ErrorSelectors.
func SetErrorSelectors(selectors map[string]string) {
	errorSelectors = selectors
}

// RevertError represents a call or transaction that reverted.
type RevertError struct {
	Reason    string // The decoded revert reason
	Simulated bool   // True if failed during simulation (no tx sent)
	TxHash    string // Transaction hash (empty if simulated)
}

func (e *RevertError) Error() string {
	if e.Simulated {
		return fmt.Sprintf("call reverted: %s", e.Reason)
	}
	if e.TxHash != "" {
		return fmt.Sprintf("tx %s reverted: %s", e.TxHash[:10], e.Reason)
	}
	return fmt.Sprintf("reverted: %s", e.Reason)
}

// IsRevertError checks if an error is a RevertError and returns it.
func IsRevertError(err error) (*RevertError, bool) {
	var revertErr *RevertError
	if errors.As(err, &revertErr) {
		return revertErr, true
	}
	return nil, false
}

// TxOpts specifies parameters for a transaction.
type TxOpts struct {
	To       common.Address
	Data     []byte
	Value    *big.Int
	GasLimit uint64 // 0 = estimate
}

// TxManager handles transaction lifecycle: submission, monitoring, gas bumping, and cancellation.
type TxManager struct {
	client       *ethclient.Client
	signer       wallet.Signer
	chainID      *big.Int
	policy       *TxPolicy
	maxFeePerGas *big.Int
}

// New creates a TxManager with the given policy.
func New(client *ethclient.Client, signer wallet.Signer, chainID *big.Int, policy *TxPolicy) (*TxManager, error) {
	if policy == nil {
		return nil, fmt.Errorf("tx policy is required")
	}

	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid tx policy: %w", err)
	}

	maxFee, err := policy.ParseMaxFeePerGas()
	if err != nil {
		return nil, fmt.Errorf("failed to parse max_fee_per_gas: %w", err)
	}

	return &TxManager{
		client:       client,
		signer:       signer,
		chainID:      chainID,
		policy:       policy,
		maxFeePerGas: maxFee,
	}, nil
}

// SendTransaction submits a transaction and handles retries, gas bumping, and cancellation.
func (m *TxManager) SendTransaction(ctx context.Context, opts *TxOpts) (*types.Receipt, error) {
	from := m.signer.Address()

	gasLimit := opts.GasLimit
	if gasLimit == 0 {
		callMsg := ethereum.CallMsg{
			From:  from,
			To:    &opts.To,
			Data:  opts.Data,
			Value: opts.Value,
		}
		estimated, err := m.client.EstimateGas(ctx, callMsg)
		if err != nil {
			reason := extractRevertReason(err)
			return nil, &RevertError{Reason: reason, Simulated: true}
		}
		gasLimit = estimated * uint64(100+m.policy.GasBufferPercent) / 100
		logger.Debugw("Gas estimated", "estimated", estimated, "withBuffer", gasLimit)
	}

	gasTipCap, gasFeeCap, err := m.getGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	nonce, err := m.client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	value := opts.Value
	if value == nil {
		value = big.NewInt(0)
	}

	var lastTx *types.Transaction
	var lastGasFeeCap *big.Int = gasFeeCap
	var lastGasTipCap *big.Int = gasTipCap

	for attempt := 1; attempt <= m.policy.MaxRetries; attempt++ {
		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   m.chainID,
			Nonce:     nonce,
			GasTipCap: lastGasTipCap,
			GasFeeCap: lastGasFeeCap,
			Gas:       gasLimit,
			To:        &opts.To,
			Value:     value,
			Data:      opts.Data,
		})

		signedTx, err := m.signer.Sign(tx, m.chainID)
		if err != nil {
			return nil, fmt.Errorf("failed to sign tx: %w", err)
		}

		if err := m.client.SendTransaction(ctx, signedTx); err != nil {
			// If replacement underpriced, bump and retry
			if attempt < m.policy.MaxRetries {
				logger.Warnw("Failed to submit tx, retrying", "attempt", attempt, "error", err)
				time.Sleep(m.policy.RetryDelay)
				continue
			}
			return nil, fmt.Errorf("failed to send tx: %w", err)
		}

		lastTx = signedTx
		logger.Infow("Tx submitted",
			"hash", signedTx.Hash().Hex(),
			"nonce", nonce,
			"gasTipCap", lastGasTipCap,
			"gasFeeCap", lastGasFeeCap,
			"attempt", attempt)

		receipt, err := m.waitForReceipt(ctx, signedTx)
		if err == nil {
			if receipt.Status == 1 {
				logger.Infow("Tx confirmed",
					"hash", signedTx.Hash().Hex(),
					"block", receipt.BlockNumber.Uint64(),
					"gasUsed", receipt.GasUsed)
				return receipt, nil
			}
			// Reverted - try to get reason
			reason := m.getRevertReason(ctx, opts, receipt.BlockNumber)
			logger.Warnw("Tx reverted",
				"hash", signedTx.Hash().Hex(),
				"block", receipt.BlockNumber.Uint64(),
				"reason", reason)
			return receipt, &RevertError{Reason: reason, TxHash: signedTx.Hash().Hex()}
		}

		if attempt >= m.policy.MaxRetries {
			break
		}

		logger.Warnw("Tx pending timeout, bumping gas",
			"hash", signedTx.Hash().Hex(),
			"attempt", attempt,
			"timeoutBlocks", m.policy.PendingTimeoutBlocks)

		bumpFactor := int64(100 + m.policy.GasBumpPercent)
		newTip := new(big.Int).Mul(lastGasTipCap, big.NewInt(bumpFactor))
		newTip.Div(newTip, big.NewInt(100))
		newFeeCap := new(big.Int).Mul(lastGasFeeCap, big.NewInt(bumpFactor))
		newFeeCap.Div(newFeeCap, big.NewInt(100))

		if m.maxFeePerGas != nil && newFeeCap.Cmp(m.maxFeePerGas) > 0 {
			logger.Warnw("Transaction stuck at max gas, attempting cancel",
				"nonce", nonce,
				"maxFeePerGas", m.maxFeePerGas,
				"currentFeeCap", lastGasFeeCap,
				"attempt", attempt)

			if err := m.cancelTx(ctx, nonce, lastGasFeeCap); err != nil {
				logger.Errorw("Failed to cancel tx", "error", err)
			}
			return nil, ErrMaxGasReached
		}

		logger.Infow("Bumping gas",
			"oldTip", lastGasTipCap,
			"newTip", newTip,
			"oldFeeCap", lastGasFeeCap,
			"newFeeCap", newFeeCap)

		lastGasTipCap = newTip
		lastGasFeeCap = newFeeCap
	}

	if lastTx != nil {
		logger.Warnw("Max retries exhausted, cancelling tx",
			"hash", lastTx.Hash().Hex(),
			"nonce", nonce)

		if err := m.cancelTx(ctx, nonce, lastGasFeeCap); err != nil {
			logger.Errorw("Failed to cancel tx", "error", err)
		}
	}

	return nil, ErrMaxRetriesExhausted
}

func (m *TxManager) getGasPrice(ctx context.Context) (*big.Int, *big.Int, error) {
	gasTipCap, err := m.client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get gas tip cap: %w", err)
	}

	header, err := m.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get latest header: %w", err)
	}

	// gasFeeCap = 2*baseFee + gasTipCap (EIP-1559 standard formula)
	gasFeeCap := new(big.Int).Add(
		new(big.Int).Mul(header.BaseFee, big.NewInt(2)),
		gasTipCap,
	)

	if m.maxFeePerGas != nil && gasFeeCap.Cmp(m.maxFeePerGas) > 0 {
		gasFeeCap = new(big.Int).Set(m.maxFeePerGas)
		// Adjust tip if needed
		if gasTipCap.Cmp(gasFeeCap) > 0 {
			gasTipCap = new(big.Int).Set(gasFeeCap)
		}
	}

	return gasTipCap, gasFeeCap, nil
}

func (m *TxManager) waitForReceipt(ctx context.Context, tx *types.Transaction) (*types.Receipt, error) {
	startBlock, err := m.client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get block number: %w", err)
	}

	ticker := time.NewTicker(receiptPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			receipt, err := m.client.TransactionReceipt(ctx, tx.Hash())
			if err == nil {
				return receipt, nil
			}
			logger.Debugw("Receipt not found", "hash", tx.Hash().Hex(), "error", err)

			currentBlock, err := m.client.BlockNumber(ctx)
			if err != nil {
				logger.Warnw("Failed to get block number", "error", err)
				continue
			}

			if currentBlock-startBlock >= uint64(m.policy.PendingTimeoutBlocks) {
				return nil, fmt.Errorf("pending timeout after %d blocks", m.policy.PendingTimeoutBlocks)
			}
		}
	}
}

func (m *TxManager) cancelTx(ctx context.Context, nonce uint64, prevGasFeeCap *big.Int) error {
	from := m.signer.Address()

	// Use higher gas price than previous tx to replace it
	bumpFactor := int64(100 + m.policy.GasBumpPercent)
	gasFeeCap := new(big.Int).Mul(prevGasFeeCap, big.NewInt(bumpFactor))
	gasFeeCap.Div(gasFeeCap, big.NewInt(100))

	// Use max if we have it and it's higher
	if m.maxFeePerGas != nil && gasFeeCap.Cmp(m.maxFeePerGas) < 0 {
		gasFeeCap = new(big.Int).Set(m.maxFeePerGas)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   m.chainID,
		Nonce:     nonce,
		GasTipCap: gasFeeCap,
		GasFeeCap: gasFeeCap,
		Gas:       simpleTransferGas,
		To:        &from,
		Value:     big.NewInt(0),
	})

	signedTx, err := m.signer.Sign(tx, m.chainID)
	if err != nil {
		return fmt.Errorf("failed to sign cancel tx: %w", err)
	}

	if err := m.client.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("failed to send cancel tx: %w", err)
	}

	logger.Infow("Cancel tx submitted",
		"hash", signedTx.Hash().Hex(),
		"nonce", nonce,
		"gasFeeCap", gasFeeCap)

	receipt, err := m.waitForReceipt(ctx, signedTx)
	if err != nil {
		return fmt.Errorf("cancel tx not confirmed: %w", err)
	}

	logger.Infow("Nonce freed",
		"nonce", nonce,
		"cancelTxHash", signedTx.Hash().Hex(),
		"block", receipt.BlockNumber.Uint64())

	return nil
}

// getRevertReason replays the call at the given block to extract the revert reason.
func (m *TxManager) getRevertReason(ctx context.Context, opts *TxOpts, blockNum *big.Int) string {
	from := m.signer.Address()

	callMsg := ethereum.CallMsg{
		From:  from,
		To:    &opts.To,
		Data:  opts.Data,
		Value: opts.Value,
	}

	_, err := m.client.CallContract(ctx, callMsg, blockNum)
	if err == nil {
		return "unknown (call succeeded on replay)"
	}

	return extractRevertReason(err)
}

// extractRevertReason extracts a human-readable reason from a revert error.
func extractRevertReason(err error) string {
	// Use go-ethereum's built-in RevertErrorData to extract raw revert data
	revertData, ok := ethclient.RevertErrorData(err)
	if ok && len(revertData) > 0 {
		// Use abi.UnpackRevert for standard Error(string) decoding
		if reason, unpackErr := abi.UnpackRevert(revertData); unpackErr == nil {
			return reason
		}
		// Try to decode as custom error from ABI
		if len(revertData) >= 4 {
			selector := hex.EncodeToString(revertData[:4])
			if name, found := errorSelectors[selector]; found {
				return name
			}
		}
		// Unknown custom error - return hex
		return fmt.Sprintf("0x%x", revertData)
	}

	// Fallback: check if error message contains the reason directly
	// (some clients include it in the error string)
	errStr := err.Error()
	if strings.Contains(errStr, "execution reverted:") {
		parts := strings.SplitN(errStr, "execution reverted:", 2)
		if len(parts) == 2 {
			reason := strings.TrimSpace(parts[1])
			if reason != "" {
				// Check if it's a hex selector we can decode
				reason = decodeErrorSelector(reason)
				return reason
			}
		}
	}

	return errStr
}

// decodeErrorSelector tries to decode a hex error selector to a known error name.
func decodeErrorSelector(reason string) string {
	// Check if it looks like a hex selector (0x followed by hex chars)
	if strings.HasPrefix(reason, "0x") && len(reason) >= 10 {
		selector := reason[2:10] // First 4 bytes after 0x
		if name, found := errorSelectors[selector]; found {
			return name
		}
	}
	return reason
}
