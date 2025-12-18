package contract

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"ssv-oracle/logger"
	"ssv-oracle/txmanager"
	"ssv-oracle/wallet"
)

// Cluster represents the SSV cluster struct as used in the contract.
type Cluster struct {
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	Active          bool
	Balance         *big.Int
}

// Client interacts with the SSV Network contract.
type Client struct {
	ethClient            *ethclient.Client
	wsClient             *ethclient.Client
	contractAddress      common.Address
	viewsContractAddress common.Address
	txManager            *txmanager.TxManager
	chainID              *big.Int
}

// Config holds configuration for creating a Client.
type Config struct {
	RPCURL               string
	WSRPCURL             string
	ContractAddress      string
	ViewsContractAddress string
	Signer               wallet.Signer
	TxPolicy             *txmanager.TxPolicy
}

// NewClient creates a contract client with auto-detected chain ID.
func NewClient(ctx context.Context, cfg *Config) (*Client, error) {
	if cfg.Signer == nil {
		return nil, fmt.Errorf("signer cannot be nil")
	}

	ethClient, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum node: %w", err)
	}

	chainID, err := ethClient.ChainID(ctx)
	if err != nil {
		ethClient.Close()
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	txMgr, err := txmanager.New(ethClient, cfg.Signer, chainID, cfg.TxPolicy)
	if err != nil {
		ethClient.Close()
		return nil, fmt.Errorf("failed to create tx manager: %w", err)
	}
	txMgr.SetErrorSelectors(ErrorSelectors)

	client := &Client{
		ethClient:            ethClient,
		contractAddress:      common.HexToAddress(cfg.ContractAddress),
		viewsContractAddress: common.HexToAddress(cfg.ViewsContractAddress),
		txManager:            txMgr,
		chainID:              chainID,
	}

	if cfg.WSRPCURL != "" {
		wsClient, err := ethclient.Dial(cfg.WSRPCURL)
		if err != nil {
			ethClient.Close()
			return nil, fmt.Errorf("failed to connect to WebSocket endpoint: %w", err)
		}
		client.wsClient = wsClient
		logger.Infow("WebSocket client connected", "url", cfg.WSRPCURL)
	}

	return client, nil
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

// CommitRoot submits a merkle root to the SSV Network contract.
func (c *Client) CommitRoot(ctx context.Context, merkleRoot [32]byte, blockNum, roundID, targetEpoch uint64) (*types.Receipt, error) {
	data, err := SSVNetworkABI.Pack("commitRoot", merkleRoot, blockNum)
	if err != nil {
		return nil, fmt.Errorf("failed to pack commitRoot: %w", err)
	}

	return c.txManager.SendTransaction(ctx, &txmanager.TxOpts{
		To:   c.contractAddress,
		Data: data,
	})
}

// GetClusterEffectiveBalance returns the effective balance for a cluster by calling
// the getBalance function on the SSVNetworkViews contract.
func (c *Client) GetClusterEffectiveBalance(ctx context.Context, owner common.Address, operatorIDs []uint64, cluster Cluster) (uint64, error) {
	data, err := SSVNetworkViewsABI.Pack("getBalance", owner, operatorIDs, cluster)
	if err != nil {
		return 0, fmt.Errorf("failed to pack getBalance: %w", err)
	}

	result, err := c.ethClient.CallContract(ctx, ethereum.CallMsg{
		To:   &c.viewsContractAddress,
		Data: data,
	}, nil)
	if err != nil {
		if txmanager.IsContractRevert(err) {
			reason := c.txManager.ExtractRevertReason(err)
			return 0, &txmanager.RevertError{Reason: reason, Simulated: true}
		}
		return 0, fmt.Errorf("failed to call getBalance: %w", err)
	}

	// Unpack returns (balance, ebBalance)
	var output struct {
		Balance   *big.Int
		EbBalance *big.Int
	}
	if err := SSVNetworkViewsABI.UnpackIntoInterface(&output, "getBalance", result); err != nil {
		return 0, fmt.Errorf("failed to unpack getBalance result: %w", err)
	}

	return output.EbBalance.Uint64(), nil
}

// UpdateClusterBalance updates a cluster's balance using a merkle proof.
// Not thread-safe; callers must ensure sequential execution.
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
		return nil, fmt.Errorf("failed to pack updateClusterBalance: %w", err)
	}

	return c.txManager.SendTransaction(ctx, &txmanager.TxOpts{
		To:   c.contractAddress,
		Data: data,
	})
}
