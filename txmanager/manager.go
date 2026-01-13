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

	"github.com/ssvlabs/ssv-oracle/logger"
	"github.com/ssvlabs/ssv-oracle/wallet"
)

var (
	errMaxGasReached        = errors.New("max gas price reached, tx cancelled")
	errMaxAttemptsExhausted = errors.New("max attempts exhausted")
	errNonceTooLow          = errors.New("nonce too low")
	errInsufficientFunds    = errors.New("insufficient funds")
	errBaseFeeExceedsMax    = errors.New("base fee exceeds max_fee_per_gas")
)

// RPC error detection patterns (case-insensitive).
const (
	errPatternNonceTooLow              = "nonce too low"
	errPatternAlreadyKnown             = "already known"
	errPatternInsufficientFunds        = "insufficient funds"
	errPatternInsufficientBalance      = "insufficient balance"
	errPatternExecutionReverted        = "execution reverted:"
	errPatternReplacementUnderpriced   = "replacement transaction underpriced"
	errPatternMaxFeePerGasTooLow       = "max fee per gas less than block base fee"
	errPatternUnderpricedTxPoolVariant = "underpriced"
)

const (
	receiptPollInterval   = 4 * time.Second
	percentBase           = 100
	blockNumberRetryLimit = 3
	minTipCap           = params.GWei // minimum for MEV RPC compatibility
)

// RevertError represents a contract call or transaction that reverted.
type RevertError struct {
	Reason    string
	Simulated bool
	TxHash    string
}

// Error formats the revert reason for logging and wrapping.
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
	UseMEV   bool // Use MEV RPCs for initial submission (updater only)
}

// TxManager handles transaction submission, gas bumping, and cancellation.
type TxManager struct {
	client         *ethclient.Client
	signer         wallet.Signer
	chainID        *big.Int
	policy         *TxPolicy
	errorSelectors map[string]string
	mevClients     map[string]*ethclient.Client // url -> client for MEV RPCs
}

// New creates a TxManager.
// mevRPCs is optional; when empty, MEV protection is disabled.
func New(client *ethclient.Client, signer wallet.Signer, chainID *big.Int, policy *TxPolicy, mevRPCs []string) (*TxManager, error) {
	if policy == nil {
		return nil, fmt.Errorf("tx policy is required")
	}
	policy.ApplyDefaults()
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid tx policy: %w", err)
	}

	mevClients := make(map[string]*ethclient.Client)
	for _, rpcURL := range mevRPCs {
		mevClient, err := ethclient.Dial(rpcURL)
		if err != nil {
			for _, c := range mevClients {
				c.Close()
			}
			return nil, fmt.Errorf("dial MEV RPC %s: %w", rpcURL, err)
		}
		mevClients[rpcURL] = mevClient
	}

	logger.Infow("Transaction manager initialized",
		"policy", policy,
		"mevRPCs", mevRPCs,
	)

	return &TxManager{
		client:         client,
		signer:         signer,
		chainID:        chainID,
		policy:         policy,
		errorSelectors: make(map[string]string),
		mevClients:     mevClients,
	}, nil
}

// SetErrorSelectors configures error selectors for decoding revert reasons.
func (m *TxManager) SetErrorSelectors(selectors map[string]string) {
	m.errorSelectors = selectors
}

// Close closes all MEV RPC connections.
func (m *TxManager) Close() {
	for _, mc := range m.mevClients {
		mc.Close()
	}
}

// IsRevertError returns the RevertError if err wraps one.
func IsRevertError(err error) (*RevertError, bool) {
	var revertErr *RevertError
	if errors.As(err, &revertErr) {
		return revertErr, true
	}
	return nil, false
}

// SendTransaction submits a transaction with automatic retries and gas bumping.
// If UseMEV=true and MEV clients configured: broadcast to all MEV RPCs in parallel,
// retry MEV on send errors; after first successful submission, wait PendingTimeoutBlocks
// then switch to eth_rpc for remaining retries.
func (m *TxManager) SendTransaction(ctx context.Context, opts *TxOpts) (*types.Receipt, error) {
	from := m.signer.Address()

	gasLimit, err := m.estimateGas(ctx, opts)
	if err != nil {
		return nil, err
	}

	gasTipCap, gasFeeCap, err := m.suggestGasFees(ctx)
	if err != nil {
		return nil, err
	}

	nonce, err := m.client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("get nonce: %w", err)
	}

	value := opts.Value
	if value == nil {
		value = big.NewInt(0)
	}

	var publishedTxs []*types.Transaction
	currentTip := gasTipCap
	currentFeeCap := gasFeeCap

	// MEV path: use MEV RPCs for initial attempt, switch to eth_rpc after first timeout
	useMEV := opts.UseMEV && len(m.mevClients) > 0

	var log logger.Logger
	for attempt := 1; attempt <= m.policy.MaxAttempts; attempt++ {
		signedTx, err := m.buildAndSignTx(opts, nonce, gasLimit, currentTip, currentFeeCap, value)
		if err != nil {
			return nil, err
		}
		log = logger.With("hash", signedTx.Hash().Hex(),
			"nonce", nonce,
			"useMEV", useMEV)

		var sendErr error
		if useMEV {
			sendErr = m.sendToMEVRPCs(ctx, signedTx)
		} else {
			sendErr = m.client.SendTransaction(ctx, signedTx)
		}

		alreadyKnown := false
		if sendErr != nil {
			switch {
			case isTxAlreadyKnown(sendErr):
				log.Debug("Tx already in mempool")
				alreadyKnown = true

			case isNonceTooLow(sendErr):
				if len(publishedTxs) > 0 {
					if receipt, recErr := m.findMinedReceipt(ctx, opts, publishedTxs); receipt != nil {
						return receipt, recErr
					}
				}
				return nil, fmt.Errorf("%w: %w", errNonceTooLow, sendErr)

			case isInsufficientFunds(sendErr):
				return nil, fmt.Errorf("%w: %w", errInsufficientFunds, sendErr)

			case isUnderpriced(sendErr):
				newTip, newFeeCap, shouldCancel := m.bumpOrResuggest(ctx, currentTip, currentFeeCap)
				if shouldCancel {
					log.Warnw("Max gas reached",
						"maxFeePerGas", m.policy.MaxFeePerGasWei(),
						"currentFeeCap", currentFeeCap,
						"willRetry", false)
					if err := m.cancelTx(ctx, nonce, currentFeeCap); err != nil {
						log.Warnw("Cancel tx failed", "error", err)
					}
					return nil, errMaxGasReached
				}
				log.Warnw("Tx underpriced",
					"attempt", attempt,
					"currentTip", currentTip,
					"currentFeeCap", currentFeeCap,
					"newTip", newTip,
					"newFeeCap", newFeeCap,
					"error", sendErr)
				currentTip, currentFeeCap = newTip, newFeeCap
				continue

			default:
				if attempt < m.policy.MaxAttempts {
					log.Warnw("Tx submission failed",
						"attempt", attempt,
						"maxAttempts", m.policy.MaxAttempts,
						"error", sendErr)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(m.policy.RetryDelay):
					}
					continue
				}
				return nil, fmt.Errorf("send tx: %w", sendErr)
			}
		}

		publishedTxs = append(publishedTxs, signedTx)
		if !alreadyKnown {
			log.Debugw("Tx submitted",
				"gasTipCap", currentTip,
				"gasFeeCap", currentFeeCap,
				"attempt", attempt)
		}

		receipt, err := m.waitForReceipt(ctx, signedTx)
		if err == nil {
			return m.handleReceipt(ctx, opts, signedTx, receipt)
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt >= m.policy.MaxAttempts {
			log.Warnw("Tx receipt wait failed",
				"attempt", attempt,
				"willRetry", false,
				"error", err)
			break
		}

		// MEV had its chance, switch to eth_rpc for remaining retries
		if useMEV {
			log.Warnw("MEV not included, switching to eth_rpc",
				"pendingTimeoutBlocks", m.policy.PendingTimeoutBlocks)
			useMEV = false
		}

		newTip, newFeeCap, shouldCancel := m.bumpOrResuggest(ctx, currentTip, currentFeeCap)
		if shouldCancel {
			log.Warnw("Max gas reached",
				"maxFeePerGas", m.policy.MaxFeePerGasWei(),
				"currentFeeCap", currentFeeCap,
				"willRetry", false)
			if err := m.cancelTx(ctx, nonce, currentFeeCap); err != nil {
				log.Warnw("Cancel tx failed", "error", err)
			}
			return nil, errMaxGasReached
		}

		log.Warnw("Tx pending timeout",
			"attempt", attempt,
			"newFeeCap", newFeeCap)
		currentTip, currentFeeCap = newTip, newFeeCap
	}

	log.Warnw("Max attempts exhausted",
		"attempts", m.policy.MaxAttempts,
		"willRetry", false)
	if err := m.cancelTx(ctx, nonce, currentFeeCap); err != nil {
		log.Warnw("Cancel tx failed", "error", err)
	}

	return nil, errMaxAttemptsExhausted
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
		return nil, fmt.Errorf("sign tx: %w", err)
	}
	return signedTx, nil
}

// sendToMEVRPCs broadcasts tx to all MEV RPCs in parallel.
// Returns nil if any MEV RPC accepts (or already knows) the tx.
// Falls back to eth_rpc if all MEV RPCs reject.
func (m *TxManager) sendToMEVRPCs(ctx context.Context, tx *types.Transaction) error {
	if len(m.mevClients) == 0 {
		return m.client.SendTransaction(ctx, tx)
	}

	type result struct {
		url string
		err error
	}
	results := make(chan result, len(m.mevClients))

	for url, client := range m.mevClients {
		go func(u string, c *ethclient.Client) {
			err := c.SendTransaction(ctx, tx)
			results <- result{u, err}
		}(url, client)
	}

	var lastErr error
	successCount := 0
	for range m.mevClients {
		r := <-results
		if r.err == nil {
			successCount++
			logger.Debugw("MEV RPC accepted tx", "url", r.url, "hash", tx.Hash().Hex())
		} else if isTxAlreadyKnown(r.err) {
			successCount++
			logger.Debugw("MEV RPC already knows tx", "url", r.url, "hash", tx.Hash().Hex())
		} else {
			lastErr = r.err
			logger.Warnw("MEV RPC rejected tx", "url", r.url, "error", r.err)
		}
	}

	if successCount > 0 {
		return nil
	}

	logger.Warnw("All MEV RPCs failed, falling back to eth_rpc", "lastError", lastErr)
	return m.client.SendTransaction(ctx, tx)
}

func (m *TxManager) applyBumpPercent(value *big.Int) *big.Int {
	bumpFactor := big.NewInt(int64(percentBase + m.policy.GasBumpPercent))
	result := new(big.Int).Mul(value, bumpFactor)
	return result.Div(result, big.NewInt(percentBase))
}

// bumpOrResuggest returns the higher of bumped fees or freshly suggested fees,
// capped at max_fee_per_gas. Returns shouldCancel=true if fees would exceed cap.
func (m *TxManager) bumpOrResuggest(ctx context.Context, currentTip, currentFeeCap *big.Int) (newTip, newFeeCap *big.Int, shouldCancel bool) {
	maxFee := m.policy.MaxFeePerGasWei()
	bumpedTip := m.applyBumpPercent(currentTip)
	bumpedFeeCap := m.applyBumpPercent(currentFeeCap)

	// Check cap before proceeding
	if bumpedFeeCap.Cmp(maxFee) > 0 {
		return nil, nil, true
	}

	freshTip, freshFeeCap, err := m.suggestGasFees(ctx)
	if err != nil {
		if errors.Is(err, errBaseFeeExceedsMax) {
			return nil, nil, true
		}
		return bumpedTip, bumpedFeeCap, false
	}

	if freshTip.Cmp(bumpedTip) > 0 {
		bumpedTip = freshTip
	}
	if freshFeeCap.Cmp(bumpedFeeCap) > 0 {
		bumpedFeeCap = freshFeeCap
	}

	return bumpedTip, bumpedFeeCap, false
}

// cancelTx sends a zero-value self-transfer to free the nonce.
func (m *TxManager) cancelTx(ctx context.Context, nonce uint64, prevGasFeeCap *big.Int) error {
	from := m.signer.Address()
	maxFee := m.policy.MaxFeePerGasWei()

	gasFeeCap := m.applyBumpPercent(prevGasFeeCap)
	if gasFeeCap.Cmp(maxFee) > 0 {
		gasFeeCap = new(big.Int).Set(maxFee)
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
		return fmt.Errorf("sign cancel tx: %w", err)
	}

	if err := m.client.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("send cancel tx: %w", err)
	}

	logger.Debugw("Cancel tx submitted",
		"hash", signedTx.Hash().Hex(),
		"nonce", nonce)

	receipt, err := m.waitForReceipt(ctx, signedTx)
	if err != nil {
		return fmt.Errorf("cancel tx not confirmed: %w", err)
	}

	logger.Debugw("Cancel tx confirmed",
		"nonce", nonce,
		"block", receipt.BlockNumber.Uint64())

	return nil
}

// estimateGas estimates gas for the transaction.
// Returns RevertError for contract reverts, regular error for network/RPC failures.
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
		if IsContractRevert(err) {
			reason := m.ExtractRevertReason(err)
			return 0, &RevertError{Reason: reason, Simulated: true}
		}
		return 0, fmt.Errorf("estimate gas: %w", err)
	}

	gasLimit := estimated * uint64(percentBase+m.policy.GasBufferPercent) / percentBase
	logger.Debugw("Gas estimated",
		"estimated", estimated,
		"withBuffer", gasLimit)

	return gasLimit, nil
}

// handleReceipt processes a mined transaction receipt.
func (m *TxManager) handleReceipt(ctx context.Context, opts *TxOpts, tx *types.Transaction, receipt *types.Receipt) (*types.Receipt, error) {
	if receipt.Status == types.ReceiptStatusSuccessful {
		logger.Debugw("Tx confirmed",
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
	return m.ExtractRevertReason(err)
}

// suggestGasFees returns suggested tip and fee cap, capped at maxFeePerGas.
// Enforces minimum tip cap for MEV RPC compatibility.
// Returns errBaseFeeExceedsMax if current base fee exceeds the configured maximum.
func (m *TxManager) suggestGasFees(ctx context.Context) (*big.Int, *big.Int, error) {
	maxFee := m.policy.MaxFeePerGasWei()

	gasTipCap, err := m.client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get gas tip cap: %w", err)
	}

	// Enforce minimum tip (MEV RPCs may drop tx if tip == 0)
	if gasTipCap.Cmp(big.NewInt(minTipCap)) < 0 {
		gasTipCap = big.NewInt(minTipCap)
	}

	header, err := m.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("get latest header: %w", err)
	}

	// Check if base fee already exceeds our cap - tx would be rejected as underpriced
	if header.BaseFee.Cmp(maxFee) > 0 {
		return nil, nil, fmt.Errorf("%w: base fee %s > max %s",
			errBaseFeeExceedsMax, header.BaseFee, maxFee)
	}

	// EIP-1559: gasFeeCap = 2*baseFee + gasTipCap
	gasFeeCap := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
	gasFeeCap.Add(gasFeeCap, gasTipCap)

	if gasFeeCap.Cmp(maxFee) > 0 {
		gasFeeCap = new(big.Int).Set(maxFee)
		if gasTipCap.Cmp(gasFeeCap) > 0 {
			gasTipCap = new(big.Int).Set(gasFeeCap)
		}
	}

	return gasTipCap, gasFeeCap, nil
}

// findMinedReceipt checks if any previously published tx was mined and returns its receipt.
// Used when we get "nonce too low" to recover the actual receipt.
// Returns the receipt and any error (e.g., RevertError if the tx reverted).
func (m *TxManager) findMinedReceipt(ctx context.Context, opts *TxOpts, txs []*types.Transaction) (*types.Receipt, error) {
	for _, tx := range txs {
		receipt, err := m.client.TransactionReceipt(ctx, tx.Hash())
		if err != nil {
			continue
		}
		logger.Infow("Found mined tx after nonce-too-low",
			"hash", tx.Hash().Hex(),
			"block", receipt.BlockNumber.Uint64())

		return m.handleReceipt(ctx, opts, tx, receipt)
	}
	return nil, nil
}

// waitForReceipt polls for a transaction receipt until confirmed or timeout.
func (m *TxManager) waitForReceipt(ctx context.Context, tx *types.Transaction) (*types.Receipt, error) {
	startBlock, err := m.client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("get block number: %w", err)
	}

	ticker := time.NewTicker(receiptPollInterval)
	defer ticker.Stop()

	var lastErr error
	var blockNumFailures int
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			receipt, err := m.client.TransactionReceipt(ctx, tx.Hash())
			if err == nil {
				return receipt, nil
			}
			lastErr = err

			currentBlock, err := m.client.BlockNumber(ctx)
			if err != nil {
				blockNumFailures++
				if blockNumFailures >= blockNumberRetryLimit {
					return nil, fmt.Errorf("get block number: %w", err)
				}
				logger.Warnw("Failed to get block number", "error", err, "failures", blockNumFailures)
				continue
			}
			blockNumFailures = 0

			if currentBlock-startBlock >= uint64(m.policy.PendingTimeoutBlocks) {
				return nil, fmt.Errorf("timed out waiting for receipt after %d blocks: %w", m.policy.PendingTimeoutBlocks, lastErr)
			}
		}
	}
}

// ExtractRevertReason extracts a human-readable revert reason from an error.
func (m *TxManager) ExtractRevertReason(err error) string {
	revertData, ok := ethclient.RevertErrorData(err)
	if ok && len(revertData) > 0 {
		if reason, unpackErr := abi.UnpackRevert(revertData); unpackErr == nil {
			return reason
		}
		if len(revertData) >= 4 {
			selector := hex.EncodeToString(revertData[:4])
			if name, found := m.errorSelectors[selector]; found {
				return name
			}
		}
		return fmt.Sprintf("0x%x", revertData)
	}

	// Some RPC clients include reason in error string
	errStr := err.Error()
	if strings.Contains(errStr, errPatternExecutionReverted) {
		parts := strings.SplitN(errStr, errPatternExecutionReverted, 2)
		if len(parts) == 2 {
			reason := strings.TrimSpace(parts[1])
			if reason != "" {
				return m.decodeErrorSelector(reason)
			}
		}
	}

	return errStr
}

// decodeErrorSelector attempts to decode a hex selector to a known error name.
func (m *TxManager) decodeErrorSelector(reason string) string {
	if strings.HasPrefix(reason, "0x") && len(reason) >= 10 {
		selector := reason[2:10]
		if name, found := m.errorSelectors[selector]; found {
			return name
		}
	}
	return reason
}

// IsContractRevert returns true if the error is a contract revert (not a network error).
func IsContractRevert(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := ethclient.RevertErrorData(err); ok {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), errPatternExecutionReverted)
}

func isTxAlreadyKnown(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), errPatternAlreadyKnown)
}

func isNonceTooLow(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), errPatternNonceTooLow)
}

func isInsufficientFunds(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, errPatternInsufficientFunds) ||
		strings.Contains(s, errPatternInsufficientBalance)
}

func isUnderpriced(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, errPatternReplacementUnderpriced) ||
		strings.Contains(s, errPatternMaxFeePerGasTooLow) ||
		strings.Contains(s, errPatternUnderpricedTxPoolVariant)
}
