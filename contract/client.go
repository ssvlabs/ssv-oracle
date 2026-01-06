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
	ChainID              *big.Int
	Signer               wallet.Signer
	TxPolicy             *txmanager.TxPolicy
}

// NewClient creates a contract client.
// Assumes addresses are pre-validated by config layer.
func NewClient(cfg *Config) (*Client, error) {
	if cfg.ChainID == nil {
		return nil, fmt.Errorf("chain ID cannot be nil")
	}
	if cfg.Signer == nil {
		return nil, fmt.Errorf("signer cannot be nil")
	}

	ethClient, err := ethclient.Dial(cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial RPC: %w", err)
	}

	txMgr, err := txmanager.New(ethClient, cfg.Signer, cfg.ChainID, cfg.TxPolicy)
	if err != nil {
		ethClient.Close()
		return nil, fmt.Errorf("create tx manager: %w", err)
	}
	txMgr.SetErrorSelectors(errorSelectors)

	client := &Client{
		ethClient:            ethClient,
		contractAddress:      common.HexToAddress(cfg.ContractAddress),
		viewsContractAddress: common.HexToAddress(cfg.ViewsContractAddress),
		txManager:            txMgr,
		chainID:              cfg.ChainID,
	}

	if cfg.WSRPCURL != "" {
		wsClient, err := ethclient.Dial(cfg.WSRPCURL)
		if err != nil {
			ethClient.Close()
			return nil, fmt.Errorf("dial WebSocket: %w", err)
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
func (c *Client) CommitRoot(ctx context.Context, merkleRoot [32]byte, blockNum uint64) (*types.Receipt, error) {
	data, err := SSVNetworkABI.Pack("commitRoot", merkleRoot, blockNum)
	if err != nil {
		return nil, fmt.Errorf("pack commitRoot: %w", err)
	}

	return c.txManager.SendTransaction(ctx, &txmanager.TxOpts{
		To:   c.contractAddress,
		Data: data,
	})
}

// GetCommittedRoot returns the committed merkle root for a block, or zero if not committed.
func (c *Client) GetCommittedRoot(ctx context.Context, blockNum uint64) ([32]byte, error) {
	data, err := ssvNetworkViewsABI.Pack("getCommittedRoot", blockNum)
	if err != nil {
		return [32]byte{}, fmt.Errorf("pack getCommittedRoot: %w", err)
	}

	result, err := c.ethClient.CallContract(ctx, ethereum.CallMsg{
		To:   &c.viewsContractAddress,
		Data: data,
	}, nil)
	if err != nil {
		return [32]byte{}, fmt.Errorf("call getCommittedRoot: %w", err)
	}

	var root [32]byte
	if err := ssvNetworkViewsABI.UnpackIntoInterface(&root, "getCommittedRoot", result); err != nil {
		return [32]byte{}, fmt.Errorf("unpack getCommittedRoot: %w", err)
	}

	return root, nil
}

// GetClusterEffectiveBalance returns the effective balance for a cluster.
func (c *Client) GetClusterEffectiveBalance(ctx context.Context, owner common.Address, operatorIDs []uint64, cluster Cluster) (uint32, error) {
	data, err := ssvNetworkViewsABI.Pack("getEffectiveBalance", owner, operatorIDs, cluster)
	if err != nil {
		return 0, fmt.Errorf("pack getEffectiveBalance: %w", err)
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
		return 0, fmt.Errorf("call getEffectiveBalance: %w", err)
	}

	var effectiveBalance uint32
	if err := ssvNetworkViewsABI.UnpackIntoInterface(&effectiveBalance, "getEffectiveBalance", result); err != nil {
		return 0, fmt.Errorf("unpack getEffectiveBalance: %w", err)
	}

	return effectiveBalance, nil
}

// UpdateClusterBalance updates a cluster's balance using a merkle proof.
// Not thread-safe; callers must ensure sequential execution.
func (c *Client) UpdateClusterBalance(
	ctx context.Context,
	blockNum uint64,
	owner common.Address,
	operatorIds []uint64,
	cluster Cluster,
	effectiveBalance uint32,
	merkleProof [][32]byte,
) (*types.Receipt, error) {
	data, err := SSVNetworkABI.Pack("updateClusterBalance", blockNum, owner, operatorIds, cluster, effectiveBalance, merkleProof)
	if err != nil {
		return nil, fmt.Errorf("pack updateClusterBalance: %w", err)
	}

	return c.txManager.SendTransaction(ctx, &txmanager.TxOpts{
		To:   c.contractAddress,
		Data: data,
	})
}
