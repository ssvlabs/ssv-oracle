package updater

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"time"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"

	"github.com/ethereum/go-ethereum/common"
)

const (
	subscribeRetryDelay = 10 * time.Second
	reconnectDelay      = 5 * time.Second
)

// storage defines the interface the updater needs for persistence.
type storage interface {
	GetCluster(ctx context.Context, clusterID []byte) (*ethsync.ClusterRow, error)
	GetCommitByBlock(ctx context.Context, blockNum uint64) (*ethsync.OracleCommit, error)
}

// Updater listens for RootCommitted events and updates cluster balances on-chain.
type Updater struct {
	storage        storage
	contractClient *contract.Client
}

// Config holds Updater configuration.
type Config struct {
	Storage        *ethsync.PostgresStorage
	ContractClient *contract.Client
}

// New creates a new Updater instance.
func New(cfg *Config) *Updater {
	return &Updater{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
	}
}

// Run starts the updater main loop, listening for RootCommitted events.
func (u *Updater) Run(ctx context.Context) error {
	for {
		events, errChan, err := u.contractClient.SubscribeRootCommitted(ctx, nil)
		if err != nil {
			logger.Errorw("Failed to subscribe, retrying", "error", err)
			select {
			case <-time.After(subscribeRetryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		logger.Info("Subscribed to RootCommitted events")

	innerLoop:
		for {
			select {
			case <-ctx.Done():
				logger.Info("Updater stopping")
				return ctx.Err()

			case err := <-errChan:
				logger.Errorw("Subscription error, reconnecting", "error", err)
				break innerLoop

			case event, ok := <-events:
				if !ok {
					logger.Warn("Event channel closed, reconnecting")
					break innerLoop
				}

				log := logger.With("blockNum", event.BlockNum)
				log.Infow("Received RootCommitted",
					"merkleRoot", fmt.Sprintf("0x%x", event.MerkleRoot))

				commit, err := u.storage.GetCommitByBlock(ctx, event.BlockNum)
				if err != nil {
					log.Errorw("Failed to lookup commit", "error", err)
					continue
				}
				if commit == nil {
					log.Warn("Commit not found - event from unknown source?")
					continue
				}

				log.Infow("Found commit",
					"targetEpoch", commit.TargetEpoch,
					"round", commit.RoundID)

				if err := u.processCommit(ctx, commit); err != nil {
					log.Errorw("Failed to process commit", "error", err)
				}
			}
		}

		logger.Info("Reconnecting")
		select {
		case <-time.After(reconnectDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// processCommit rebuilds the merkle tree from stored balances, validates the root, and submits proofs.
func (u *Updater) processCommit(ctx context.Context, commit *ethsync.OracleCommit) error {
	log := logger.With("blockNum", commit.ReferenceBlock, "targetEpoch", commit.TargetEpoch)
	log.Infow("Processing root",
		"committedRoot", fmt.Sprintf("0x%x", commit.MerkleRoot))

	if len(commit.ClusterBalances) == 0 {
		log.Info("No clusters to update")
		return nil
	}

	log.Infow("Found clusters", "count", len(commit.ClusterBalances))

	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range commit.ClusterBalances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}

	tree := merkle.BuildMerkleTreeWithProofs(clusterMap)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", tree.Root))

	if !bytes.Equal(tree.Root[:], commit.MerkleRoot) {
		return fmt.Errorf("root mismatch: computed=0x%x, committed=0x%x",
			tree.Root, commit.MerkleRoot)
	}

	log.Infow("Root validated, processing clusters", "count", len(commit.ClusterBalances))

	updated := 0
	errors := 0

	for _, leaf := range tree.Leaves {
		if err := u.processCluster(ctx, commit.ReferenceBlock, leaf, tree); err != nil {
			logger.Warnw("Failed to process cluster",
				"clusterID", fmt.Sprintf("%x", leaf.ClusterID),
				"error", err)
			errors++
			continue
		}
		updated++
	}

	log.Infow("Commit complete",
		"updated", updated,
		"errors", errors)

	return nil
}

func (u *Updater) processCluster(ctx context.Context, blockNum uint64, leaf merkle.Leaf, tree *merkle.MerkleTree) error {
	clusterID := fmt.Sprintf("%x", leaf.ClusterID)

	cluster, err := u.storage.GetCluster(ctx, leaf.ClusterID[:])
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	if cluster == nil {
		logger.Warnw("Cluster not found", "clusterID", clusterID)
		return nil
	}

	proof, err := tree.GetProof(leaf.ClusterID)
	if err != nil {
		return fmt.Errorf("failed to get proof: %w", err)
	}

	currentBalance, err := u.contractClient.GetClusterEffectiveBalance(ctx, leaf.ClusterID)
	if err != nil {
		logger.Warnw("Failed to check current balance, skipping",
			"clusterID", clusterID,
			"error", err)
		return nil
	}
	if currentBalance == leaf.EffectiveBalance {
		logger.Debugw("Balance unchanged, skipping",
			"clusterID", clusterID,
			"balance", currentBalance)
		return nil
	}

	owner := common.BytesToAddress(cluster.OwnerAddress)
	contractCluster := contract.Cluster{
		ValidatorCount:  cluster.ValidatorCount,
		NetworkFeeIndex: cluster.NetworkFeeIndex,
		Index:           cluster.Index,
		Active:          cluster.IsActive,
		Balance:         cluster.Balance,
	}

	effectiveBalanceBig := new(big.Int).SetUint64(leaf.EffectiveBalance)

	// TxManager handles gas estimation, bumping, retries, and cancellation
	receipt, err := u.contractClient.UpdateClusterBalance(
		ctx,
		blockNum,
		owner,
		cluster.OperatorIDs,
		contractCluster,
		effectiveBalanceBig,
		proof,
	)
	if err != nil {
		return fmt.Errorf("UpdateClusterBalance: %w", err)
	}

	logger.Infow("Tx confirmed",
		"clusterID", clusterID,
		"txHash", receipt.TxHash.Hex(),
		"effectiveBalance", leaf.EffectiveBalance,
		"block", receipt.BlockNumber.Uint64())

	return nil
}
