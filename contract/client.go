package contract

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"ssv-oracle/pkg/logger"
	"ssv-oracle/txmanager"
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
	txManager       *txmanager.TxManager
	chainID         *big.Int
}

// NewClient creates a new Ethereum client. Chain ID is auto-detected.
// wsRPCURL is optional - if provided, enables event subscriptions.
func NewClient(rpcURL string, wsRPCURL string, contractAddress string, signer wallet.Signer, txPolicy *txmanager.TxPolicy) (*Client, error) {
	if signer == nil {
		return nil, fmt.Errorf("signer cannot be nil")
	}

	if !common.IsHexAddress(contractAddress) {
		return nil, fmt.Errorf("invalid contract address: %s", contractAddress)
	}

	txmanager.SetErrorSelectors(ErrorSelectors)

	ethClient, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum node: %w", err)
	}

	chainID, err := ethClient.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	txMgr, err := txmanager.New(ethClient, signer, chainID, txPolicy)
	if err != nil {
		ethClient.Close()
		return nil, fmt.Errorf("failed to create tx manager: %w", err)
	}

	client := &Client{
		ethClient:       ethClient,
		contractAddress: common.HexToAddress(contractAddress),
		txManager:       txMgr,
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

// CommitRoot submits a merkle root to the SSV Network contract.
func (c *Client) CommitRoot(ctx context.Context, merkleRoot [32]byte, blockNum uint64, roundID uint64, targetEpoch uint64) (*types.Receipt, error) {
	data, err := SSVNetworkABI.Pack("commitRoot", merkleRoot, blockNum)
	if err != nil {
		return nil, fmt.Errorf("failed to pack function call: %w", err)
	}

	return c.txManager.SendTransaction(ctx, &txmanager.TxOpts{
		To:   c.contractAddress,
		Data: data,
	})
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
) (*types.Receipt, error) {
	data, err := SSVNetworkABI.Pack("updateClusterBalance", blockNum, owner, operatorIds, cluster, effectiveBalance, merkleProof)
	if err != nil {
		return nil, fmt.Errorf("failed to pack function call: %w", err)
	}

	return c.txManager.SendTransaction(ctx, &txmanager.TxOpts{
		To:   c.contractAddress,
		Data: data,
	})
}

// GetClusterEffectiveBalance returns the effective balance for a cluster.
// TODO: Implement when contract adds getClusterEffectiveBalance function.
func (c *Client) GetClusterEffectiveBalance(ctx context.Context, clusterID [32]byte) (uint64, error) {
	return 0, nil
}

// Close closes all client connections.
func (c *Client) Close() {
	if c.ethClient != nil {
		c.ethClient.Close()
	}
	if c.wsClient != nil {
		c.wsClient.Close()
	}
}
