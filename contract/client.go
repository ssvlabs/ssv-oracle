package contract

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"ssv-oracle/pkg/logger"
)

// Cluster represents the SSV Cluster struct as used in the contract.
type Cluster struct {
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	Active          bool
	Balance         *big.Int
}

// Client is an Ethereum client for interacting with the SSV Network contract.
type Client struct {
	ethClient       *ethclient.Client // HTTP client for transactions
	wsClient        *ethclient.Client // WebSocket client for subscriptions (optional)
	contractAddress common.Address
	privateKey      []byte
	chainID         *big.Int
}

// NewClient creates a new Ethereum client.
// Chain ID is auto-detected from the RPC endpoint.
// wsRPCURL is optional - if provided, enables event subscriptions.
func NewClient(rpcURL string, wsRPCURL string, contractAddress string, privateKeyHex string) (*Client, error) {
	// Connect to Ethereum node (HTTP)
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

	// Parse private key (handle optional 0x prefix)
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	client := &Client{
		ethClient:       ethClient,
		contractAddress: contractAddr,
		privateKey:      crypto.FromECDSA(privateKey),
		chainID:         chainID,
	}

	// Connect to WebSocket endpoint if provided (for subscriptions)
	if wsRPCURL != "" {
		wsClient, err := ethclient.Dial(wsRPCURL)
		if err != nil {
			ethClient.Close()
			return nil, fmt.Errorf("failed to connect to WebSocket endpoint: %w", err)
		}
		client.wsClient = wsClient
		logger.Infow("WebSocket client connected", "url", wsRPCURL)
	}

	return client, nil
}

// CommitRoot submits a Merkle root commitment to the SSV Network contract.
// roundID and targetEpoch are passed for storage purposes only (oracle calculates these locally).
func (c *Client) CommitRoot(ctx context.Context, merkleRoot [32]byte, blockNum uint64, roundID uint64, targetEpoch uint64) (*types.Transaction, error) {
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
	data, err := SSVNetworkABI.Pack("commitRoot", merkleRoot, blockNum)
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
func (c *Client) WaitForReceipt(ctx context.Context, tx *types.Transaction) (*types.Receipt, error) {
	receipt, err := bind.WaitMined(ctx, c.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for transaction: %w", err)
	}

	return receipt, nil
}

// UpdateClusterBalance calls the contract to update a cluster's effective balance.
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
	effectiveBalance *big.Int,
	merkleProof [][32]byte,
) (*types.Transaction, error) {
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
	data, err := SSVNetworkABI.Pack("updateClusterBalance", blockNum, owner, operatorIds, cluster, effectiveBalance, merkleProof)
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
// TODO: Currently returns 0 (always triggers update) because getClusterEffectiveBalance
// is not yet available in the contract ABI. Implement when contract team adds the function.
func (c *Client) GetClusterEffectiveBalance(ctx context.Context, clusterID [32]byte) (uint64, error) {
	// Always return 0 to trigger updates - contract function not available yet
	return 0, nil
}

// Close closes the Ethereum client connections.
func (c *Client) Close() {
	if c.ethClient != nil {
		c.ethClient.Close()
	}
	if c.wsClient != nil {
		c.wsClient.Close()
	}
}
