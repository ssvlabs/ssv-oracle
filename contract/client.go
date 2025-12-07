package contract

import (
	"context"
	_ "embed"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"ssv-oracle/pkg/logger"
)

//go:embed Oracle.abi
var oracleABI string

// Cluster represents the SSV Cluster struct as used in the contract.
type Cluster struct {
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	Active          bool
	Balance         *big.Int
}

// Client is an Ethereum client for interacting with the Oracle contract.
type Client struct {
	ethClient       *ethclient.Client
	contractAddress common.Address
	contractABI     abi.ABI
	privateKey      []byte
	chainID         *big.Int
	mockMode        bool // PoC: mock mode until contract is ready
}

// NewClient creates a new Ethereum client.
// Chain ID is auto-detected from the RPC endpoint.
func NewClient(rpcURL string, contractAddress string, privateKeyHex string) (*Client, error) {
	// Connect to Ethereum node
	ethClient, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum node: %w", err)
	}

	// Auto-detect chain ID
	chainID, err := ethClient.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	// Parse contract address
	contractAddr := common.HexToAddress(contractAddress)

	// Parse ABI
	contractABI, err := abi.JSON(strings.NewReader(oracleABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract ABI: %w", err)
	}

	// Parse private key (handle optional 0x prefix)
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return &Client{
		ethClient:       ethClient,
		contractAddress: contractAddr,
		contractABI:     contractABI,
		privateKey:      crypto.FromECDSA(privateKey),
		chainID:         chainID,
		mockMode:        false,
	}, nil
}

// NewMockClient creates a mock Ethereum client for PoC testing (no real contract needed).
func NewMockClient() *Client {
	return &Client{
		mockMode: true,
	}
}

// CommitRoot submits a Merkle root commitment to the oracle contract.
// Returns the signed transaction (nil in mock mode) for use with WaitForReceipt.
// roundID and targetEpoch are passed for storage purposes only (oracle calculates these locally).
func (c *Client) CommitRoot(ctx context.Context, merkleRoot [32]byte, blockNum uint64, roundID uint64, targetEpoch uint64) (*types.Transaction, error) {
	// Mock mode: return nil transaction (no real tx to send)
	// Oracle will store the commit after WaitForReceipt
	if c.mockMode {
		return nil, nil
	}

	// Real mode: send actual transaction
	privateKey, err := crypto.ToECDSA(c.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert private key: %w", err)
	}

	from := crypto.PubkeyToAddress(privateKey.PublicKey)
	nonce, err := c.ethClient.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	// Get EIP-1559 gas parameters
	gasTipCap, err := c.ethClient.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas tip cap: %w", err)
	}

	header, err := c.ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest header: %w", err)
	}

	// GasFeeCap = 2 * baseFee + gasTipCap (standard formula)
	gasFeeCap := new(big.Int).Add(
		new(big.Int).Mul(header.BaseFee, big.NewInt(2)),
		gasTipCap,
	)

	// Encode function call (contract only receives merkleRoot and blockNum)
	data, err := c.contractABI.Pack("commitRoot", merkleRoot, blockNum)
	if err != nil {
		return nil, fmt.Errorf("failed to pack function call: %w", err)
	}

	// Estimate gas
	gasLimit, err := c.ethClient.EstimateGas(ctx, ethereum.CallMsg{
		From: from,
		To:   &c.contractAddress,
		Data: data,
	})
	if err != nil {
		logger.Warnw("Gas estimation failed for commitRoot, using default",
			"defaultGas", 200000,
			"error", err)
		gasLimit = 200000
	}

	// Create EIP-1559 transaction
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   c.chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &c.contractAddress,
		Value:     big.NewInt(0),
		Data:      data,
	})

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(c.chainID), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	err = c.ethClient.SendTransaction(ctx, signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	return signedTx, nil
}

// WaitForReceipt waits for a transaction to be mined and returns the receipt.
// Pass nil for tx in mock mode (returns fake successful receipt).
func (c *Client) WaitForReceipt(ctx context.Context, tx *types.Transaction) (*types.Receipt, error) {
	// Mock mode or nil transaction: return fake successful receipt
	if c.mockMode || tx == nil {
		return &types.Receipt{
			Status:      1,                // Success
			BlockNumber: big.NewInt(1000), // Fake block number
		}, nil
	}

	// Real mode: wait for transaction to be mined
	receipt, err := bind.WaitMined(ctx, c.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for transaction: %w", err)
	}

	return receipt, nil
}

// UpdateClusterBalance calls the contract to update a cluster's effective balance.
// In mock mode, logs the call instead of sending a transaction.
//
// NOTE: This function is NOT thread-safe. It fetches the nonce independently for each call,
// which can cause nonce collisions if called concurrently. Callers must ensure sequential
// execution or implement external nonce management for concurrent use.
func (c *Client) UpdateClusterBalance(
	ctx context.Context,
	blockNum uint64,
	owner common.Address,
	operatorIds []uint64,
	cluster Cluster,
	effectiveBalance uint64,
	proof [][32]byte,
) (*types.Transaction, error) {
	// Mock mode: log the call instead of sending real transaction
	if c.mockMode {
		// Return nil transaction in mock mode (similar to CommitRoot)
		return nil, nil
	}

	// Real mode: send actual transaction
	privateKey, err := crypto.ToECDSA(c.privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to convert private key: %w", err)
	}

	from := crypto.PubkeyToAddress(privateKey.PublicKey)
	nonce, err := c.ethClient.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	// Get EIP-1559 gas parameters
	gasTipCap, err := c.ethClient.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas tip cap: %w", err)
	}

	header, err := c.ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest header: %w", err)
	}

	// GasFeeCap = 2 * baseFee + gasTipCap (standard formula)
	gasFeeCap := new(big.Int).Add(
		new(big.Int).Mul(header.BaseFee, big.NewInt(2)),
		gasTipCap,
	)

	// Encode function call
	data, err := c.contractABI.Pack("updateClusterBalance", blockNum, owner, operatorIds, cluster, effectiveBalance, proof)
	if err != nil {
		return nil, fmt.Errorf("failed to pack function call: %w", err)
	}

	// Estimate gas
	gasLimit, err := c.ethClient.EstimateGas(ctx, ethereum.CallMsg{
		From: from,
		To:   &c.contractAddress,
		Data: data,
	})
	if err != nil {
		logger.Warnw("Gas estimation failed for updateClusterBalance, using default",
			"defaultGas", 300000,
			"error", err)
		gasLimit = 300000
	}

	// Create EIP-1559 transaction
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   c.chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &c.contractAddress,
		Value:     big.NewInt(0),
		Data:      data,
	})

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(c.chainID), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	err = c.ethClient.SendTransaction(ctx, signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	return signedTx, nil
}

// GetClusterEffectiveBalance reads the current effective balance for a cluster from the contract.
// In mock mode, returns 0 (always triggers update).
func (c *Client) GetClusterEffectiveBalance(ctx context.Context, clusterID [32]byte) (uint64, error) {
	// Mock mode: return 0 to always trigger updates
	if c.mockMode {
		return 0, nil
	}

	// Real mode: call contract view method
	data, err := c.contractABI.Pack("getClusterEffectiveBalance", clusterID)
	if err != nil {
		return 0, fmt.Errorf("failed to pack function call: %w", err)
	}

	result, err := c.ethClient.CallContract(ctx, ethereum.CallMsg{
		To:   &c.contractAddress,
		Data: data,
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to call contract: %w", err)
	}

	var balance uint64
	if err := c.contractABI.UnpackIntoInterface(&balance, "getClusterEffectiveBalance", result); err != nil {
		return 0, fmt.Errorf("failed to unpack result: %w", err)
	}

	return balance, nil
}

// Close closes the Ethereum client connection.
func (c *Client) Close() {
	if c.ethClient != nil {
		c.ethClient.Close()
	}
}
