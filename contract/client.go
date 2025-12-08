package contract

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"ssv-oracle/pkg/logger"
	"ssv-oracle/wallet"
)

// Cluster represents the SSV Cluster struct as used in the contract.
type Cluster struct {
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	Active          bool
	Balance         *big.Int
}

// Client interacts with the SSV Network contract.
type Client struct {
	ethClient       *ethclient.Client
	wsClient        *ethclient.Client // for event subscriptions
	contractAddress common.Address
	signer          wallet.Signer
	chainID         *big.Int
}

// NewClient creates a new Ethereum client. Chain ID is auto-detected.
// wsRPCURL is optional - if provided, enables event subscriptions.
func NewClient(rpcURL string, wsRPCURL string, contractAddress string, signer wallet.Signer) (*Client, error) {
	if signer == nil {
		return nil, fmt.Errorf("signer cannot be nil")
	}

	ethClient, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum node: %w", err)
	}

	chainID, err := ethClient.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	client := &Client{
		ethClient:       ethClient,
		contractAddress: common.HexToAddress(contractAddress),
		signer:          signer,
		chainID:         chainID,
	}

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

func (c *Client) CommitRoot(ctx context.Context, merkleRoot [32]byte, blockNum uint64, roundID uint64, targetEpoch uint64) (*types.Transaction, error) {
	from := c.signer.Address()
	nonce, err := c.ethClient.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	gasTipCap, err := c.ethClient.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas tip cap: %w", err)
	}

	header, err := c.ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest header: %w", err)
	}

	gasFeeCap := new(big.Int).Add(
		new(big.Int).Mul(header.BaseFee, big.NewInt(2)),
		gasTipCap,
	)

	data, err := SSVNetworkABI.Pack("commitRoot", merkleRoot, blockNum)
	if err != nil {
		return nil, fmt.Errorf("failed to pack function call: %w", err)
	}

	gasLimit, err := c.ethClient.EstimateGas(ctx, ethereum.CallMsg{
		From: from,
		To:   &c.contractAddress,
		Data: data,
	})
	if err != nil {
		logger.Warnw("Gas estimation failed, using default", "gas", 200000, "error", err)
		gasLimit = 200000
	}

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

	signedTx, err := c.signer.Sign(tx, c.chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	if err = c.ethClient.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	return signedTx, nil
}

func (c *Client) WaitForReceipt(ctx context.Context, tx *types.Transaction) (*types.Receipt, error) {
	receipt, err := bind.WaitMined(ctx, c.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for transaction: %w", err)
	}
	return receipt, nil
}

// UpdateClusterBalance is NOT thread-safe - callers must ensure sequential execution.
func (c *Client) UpdateClusterBalance(
	ctx context.Context,
	blockNum uint64,
	owner common.Address,
	operatorIds []uint64,
	cluster Cluster,
	effectiveBalance *big.Int,
	merkleProof [][32]byte,
) (*types.Transaction, error) {
	from := c.signer.Address()
	nonce, err := c.ethClient.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	gasTipCap, err := c.ethClient.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas tip cap: %w", err)
	}

	header, err := c.ethClient.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest header: %w", err)
	}

	gasFeeCap := new(big.Int).Add(
		new(big.Int).Mul(header.BaseFee, big.NewInt(2)),
		gasTipCap,
	)

	data, err := SSVNetworkABI.Pack("updateClusterBalance", blockNum, owner, operatorIds, cluster, effectiveBalance, merkleProof)
	if err != nil {
		return nil, fmt.Errorf("failed to pack function call: %w", err)
	}

	gasLimit, err := c.ethClient.EstimateGas(ctx, ethereum.CallMsg{
		From: from,
		To:   &c.contractAddress,
		Data: data,
	})
	if err != nil {
		logger.Warnw("Gas estimation failed, using default", "gas", 300000, "error", err)
		gasLimit = 300000
	}

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

	signedTx, err := c.signer.Sign(tx, c.chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	if err = c.ethClient.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	return signedTx, nil
}

func (c *Client) GetClusterEffectiveBalance(ctx context.Context, clusterID [32]byte) (uint64, error) {
	// TODO: Implement when contract adds getClusterEffectiveBalance function
	return 0, nil
}

func (c *Client) Close() {
	if c.ethClient != nil {
		c.ethClient.Close()
	}
	if c.wsClient != nil {
		c.wsClient.Close()
	}
}
