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
	"github.com/ethereum/go-ethereum/params"

	"ssv-oracle/logger"
	"ssv-oracle/wallet"
)

// Sentinel errors.
var (
	ErrMaxGasReached       = errors.New("max gas price reached, tx cancelled")
	ErrMaxRetriesExhausted = errors.New("max retries exhausted")
	ErrNonceTooLow         = errors.New("nonce too low")
)

const receiptPollInterval = 4 * time.Second

// FailureReason categorizes transaction failures.
type FailureReason string

const (
	FailureRevert    FailureReason = "revert"
	FailureNonce     FailureReason = "nonce"
	FailureGas       FailureReason = "gas"
	FailureTimeout   FailureReason = "timeout"
	FailureTransient FailureReason = "transient"
)

// RevertError represents a contract call or transaction that reverted.
type RevertError struct {
	Reason    string
	Simulated bool
	TxHash    string
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

// TxOpts specifies transaction parameters.
type TxOpts struct {
	To       common.Address
	Data     []byte
	Value    *big.Int
	GasLimit uint64
}

// TxManager handles transaction submission, gas bumping, and cancellation.
type TxManager struct {
	client       *ethclient.Client
	signer       wallet.Signer
	chainID      *big.Int
	policy       *TxPolicy
	maxFeePerGas *big.Int
}

// errorSelectors maps 4-byte selectors to custom error names for revert decoding.
var errorSelectors map[string]string

// New creates a TxManager.
func New(client *ethclient.Client, signer wallet.Signer, chainID *big.Int, policy *TxPolicy) (*TxManager, error) {
	if policy == nil {
		return nil, fmt.Errorf("tx policy is required")
	}
	policy.ApplyDefaults()
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid tx policy: %w", err)
	}

	maxFee, err := policy.ParseMaxFeePerGas()
	if err != nil {
		return nil, fmt.Errorf("failed to parse max_fee_per_gas: %w", err)
	}

	logger.Infow("Transaction policy",
		"gasBufferPercent", policy.GasBufferPercent,
		"maxFeePerGas", policy.MaxFeePerGas,
		"pendingTimeoutBlocks", policy.PendingTimeoutBlocks,
		"gasBumpPercent", policy.GasBumpPercent,
		"maxRetries", policy.MaxRetries,
		"retryDelay", policy.RetryDelay,
	)

	return &TxManager{
		client:       client,
		signer:       signer,
		chainID:      chainID,
		policy:       policy,
		maxFeePerGas: maxFee,
	}, nil
}

// ClassifyError returns (reason, retryable) for a transaction error.
func ClassifyError(err error) (FailureReason, bool) {
	if err == nil {
		return "", false
	}
	if _, ok := IsRevertError(err); ok {
		return FailureRevert, false
	}
	if errors.Is(err, ErrNonceTooLow) {
		return FailureNonce, false
	}
	if errors.Is(err, ErrMaxGasReached) {
		return FailureGas, true
	}
	if errors.Is(err, ErrMaxRetriesExhausted) {
		return FailureTimeout, true
	}
	return FailureTransient, true
}

// IsRevertError returns the RevertError if err wraps one.
func IsRevertError(err error) (*RevertError, bool) {
	var revertErr *RevertError
	if errors.As(err, &revertErr) {
		return revertErr, true
	}
	return nil, false
}

// SetErrorSelectors configures error selectors for decoding revert reasons.
func SetErrorSelectors(selectors map[string]string) {
	errorSelectors = selectors
}

// SendTransaction submits a transaction with automatic retries and gas bumping.
func (m *TxManager) SendTransaction(ctx context.Context, opts *TxOpts) (*types.Receipt, error) {
	from := m.signer.Address()

	gasLimit, err := m.estimateGas(ctx, opts)
	if err != nil {
		return nil, err
	}

	gasTipCap, gasFeeCap, err := m.suggestGasFees(ctx)
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
	currentTip := gasTipCap
	currentFeeCap := gasFeeCap

	// MaxRetries is the number of retries after initial attempt, so MaxRetries=3 means 4 total attempts
	for attempt := 0; attempt <= m.policy.MaxRetries; attempt++ {
		signedTx, err := m.buildAndSignTx(opts, nonce, gasLimit, currentTip, currentFeeCap, value)
		if err != nil {
			return nil, err
		}
		lastTx = signedTx

		if err := m.client.SendTransaction(ctx, signedTx); err != nil {
			if isTxAlreadyKnown(err) {
				logger.Infow("Tx already in mempool",
					"hash", signedTx.Hash().Hex(),
					"nonce", nonce)
				goto waitForReceipt
			}
			if isNonceTooLow(err) {
				return nil, fmt.Errorf("%w: %w", ErrNonceTooLow, err)
			}
			if attempt < m.policy.MaxRetries {
				logger.Warnw("Tx submission failed, retrying",
					"attempt", attempt+1,
					"error", err)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(m.policy.RetryDelay):
				}
				continue
			}
			return nil, fmt.Errorf("failed to send tx: %w", err)
		}

		logger.Infow("Tx submitted",
			"hash", signedTx.Hash().Hex(),
			"nonce", nonce,
			"gasTipCap", currentTip,
			"gasFeeCap", currentFeeCap,
			"attempt", attempt+1)

	waitForReceipt:
		receipt, err := m.waitForReceipt(ctx, signedTx)
		if err == nil {
			return m.handleReceipt(ctx, opts, signedTx, receipt)
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt >= m.policy.MaxRetries {
			logger.Warnw("Tx receipt wait failed on final attempt",
				"hash", signedTx.Hash().Hex(),
				"error", err)
			break
		}

		newTip, newFeeCap, shouldCancel := m.bumpGas(currentTip, currentFeeCap)
		if shouldCancel {
			logger.Warnw("Max gas reached, cancelling",
				"nonce", nonce,
				"maxFeePerGas", m.maxFeePerGas,
				"currentFeeCap", currentFeeCap)
			if err := m.cancelTx(ctx, nonce, currentFeeCap); err != nil {
				logger.Warnw("Failed to cancel tx", "nonce", nonce, "error", err)
			}
			return nil, ErrMaxGasReached
		}

		logger.Warnw("Tx pending timeout, bumping gas",
			"hash", signedTx.Hash().Hex(),
			"attempt", attempt+1,
			"oldFeeCap", currentFeeCap,
			"newFeeCap", newFeeCap)

		currentTip = newTip
		currentFeeCap = newFeeCap
	}

	logger.Warnw("Max retries exhausted, cancelling",
		"hash", lastTx.Hash().Hex(),
		"nonce", nonce)
	if err := m.cancelTx(ctx, nonce, currentFeeCap); err != nil {
		logger.Warnw("Failed to cancel tx", "nonce", nonce, "error", err)
	}

	return nil, ErrMaxRetriesExhausted
}

// buildAndSignTx creates and signs a transaction.
func (m *TxManager) buildAndSignTx(opts *TxOpts, nonce, gasLimit uint64, gasTipCap, gasFeeCap, value *big.Int) (*types.Transaction, error) {
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   m.chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &opts.To,
		Value:     value,
		Data:      opts.Data,
	})
	signedTx, err := m.signer.Sign(tx, m.chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to sign tx: %w", err)
	}
	return signedTx, nil
}

// bumpGas increases gas fees by the configured percentage.
// Returns (newTip, newFeeCap, shouldCancel).
func (m *TxManager) bumpGas(currentTip, currentFeeCap *big.Int) (*big.Int, *big.Int, bool) {
	bumpFactor := big.NewInt(int64(100 + m.policy.GasBumpPercent))
	hundred := big.NewInt(100)

	newTip := new(big.Int).Mul(currentTip, bumpFactor)
	newTip.Div(newTip, hundred)

	newFeeCap := new(big.Int).Mul(currentFeeCap, bumpFactor)
	newFeeCap.Div(newFeeCap, hundred)

	if newFeeCap.Cmp(m.maxFeePerGas) > 0 {
		return nil, nil, true
	}

	return newTip, newFeeCap, false
}

// cancelTx sends a zero-value self-transfer to free the nonce.
func (m *TxManager) cancelTx(ctx context.Context, nonce uint64, prevGasFeeCap *big.Int) error {
	from := m.signer.Address()

	bumpFactor := big.NewInt(int64(100 + m.policy.GasBumpPercent))
	gasFeeCap := new(big.Int).Mul(prevGasFeeCap, bumpFactor)
	gasFeeCap.Div(gasFeeCap, big.NewInt(100))

	// Cap at maxFeePerGas to respect configured limit.
	if gasFeeCap.Cmp(m.maxFeePerGas) > 0 {
		gasFeeCap = new(big.Int).Set(m.maxFeePerGas)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   m.chainID,
		Nonce:     nonce,
		GasTipCap: gasFeeCap,
		GasFeeCap: gasFeeCap,
		Gas:       params.TxGas,
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
		"nonce", nonce)

	receipt, err := m.waitForReceipt(ctx, signedTx)
	if err != nil {
		return fmt.Errorf("cancel tx not confirmed: %w", err)
	}

	logger.Infow("Nonce freed",
		"nonce", nonce,
		"block", receipt.BlockNumber.Uint64())

	return nil
}

// estimateGas estimates gas for the transaction, returning a RevertError if simulation fails.
func (m *TxManager) estimateGas(ctx context.Context, opts *TxOpts) (uint64, error) {
	if opts.GasLimit != 0 {
		return opts.GasLimit, nil
	}

	from := m.signer.Address()
	callMsg := ethereum.CallMsg{
		From:  from,
		To:    &opts.To,
		Data:  opts.Data,
		Value: opts.Value,
	}

	estimated, err := m.client.EstimateGas(ctx, callMsg)
	if err != nil {
		reason := extractRevertReason(err)
		return 0, &RevertError{Reason: reason, Simulated: true}
	}

	gasLimit := estimated * uint64(100+m.policy.GasBufferPercent) / 100
	logger.Debugw("Gas estimated",
		"estimated", estimated,
		"withBuffer", gasLimit)

	return gasLimit, nil
}

// handleReceipt processes a mined transaction receipt.
func (m *TxManager) handleReceipt(ctx context.Context, opts *TxOpts, tx *types.Transaction, receipt *types.Receipt) (*types.Receipt, error) {
	if receipt.Status == 1 {
		logger.Infow("Tx confirmed",
			"hash", tx.Hash().Hex(),
			"block", receipt.BlockNumber.Uint64(),
			"gasUsed", receipt.GasUsed)
		return receipt, nil
	}

	reason := m.replayForRevertReason(ctx, opts, receipt.BlockNumber)
	logger.Warnw("Tx reverted",
		"hash", tx.Hash().Hex(),
		"block", receipt.BlockNumber.Uint64(),
		"reason", reason)
	return receipt, &RevertError{Reason: reason, TxHash: tx.Hash().Hex()}
}

// replayForRevertReason re-executes the call to extract the revert reason.
func (m *TxManager) replayForRevertReason(ctx context.Context, opts *TxOpts, blockNum *big.Int) string {
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

// suggestGasFees returns suggested tip and fee cap, capped at maxFeePerGas.
func (m *TxManager) suggestGasFees(ctx context.Context) (*big.Int, *big.Int, error) {
	gasTipCap, err := m.client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get gas tip cap: %w", err)
	}

	header, err := m.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get latest header: %w", err)
	}

	// EIP-1559: gasFeeCap = 2*baseFee + gasTipCap
	gasFeeCap := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
	gasFeeCap.Add(gasFeeCap, gasTipCap)

	if gasFeeCap.Cmp(m.maxFeePerGas) > 0 {
		gasFeeCap = new(big.Int).Set(m.maxFeePerGas)
		if gasTipCap.Cmp(gasFeeCap) > 0 {
			gasTipCap = new(big.Int).Set(gasFeeCap)
		}
	}

	return gasTipCap, gasFeeCap, nil
}

// waitForReceipt polls for a transaction receipt until confirmed or timeout.
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

// decodeErrorSelector attempts to decode a hex selector to a known error name.
func decodeErrorSelector(reason string) string {
	if strings.HasPrefix(reason, "0x") && len(reason) >= 10 {
		selector := reason[2:10]
		if name, found := errorSelectors[selector]; found {
			return name
		}
	}
	return reason
}

// extractRevertReason extracts a human-readable reason from a revert error.
func extractRevertReason(err error) string {
	revertData, ok := ethclient.RevertErrorData(err)
	if ok && len(revertData) > 0 {
		if reason, unpackErr := abi.UnpackRevert(revertData); unpackErr == nil {
			return reason
		}
		if len(revertData) >= 4 {
			selector := hex.EncodeToString(revertData[:4])
			if name, found := errorSelectors[selector]; found {
				return name
			}
		}
		return fmt.Sprintf("0x%x", revertData)
	}

	// Some RPC clients include reason in error string
	errStr := err.Error()
	if strings.Contains(errStr, "execution reverted:") {
		parts := strings.SplitN(errStr, "execution reverted:", 2)
		if len(parts) == 2 {
			reason := strings.TrimSpace(parts[1])
			if reason != "" {
				return decodeErrorSelector(reason)
			}
		}
	}

	return errStr
}

// isTxAlreadyKnown returns true if the error indicates the tx is already in mempool.
func isTxAlreadyKnown(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already known")
}

// isNonceTooLow returns true if the error indicates the nonce was already used.
func isNonceTooLow(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "nonce too low")
}
