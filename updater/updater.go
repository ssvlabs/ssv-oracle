package updater

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/contract"
	"ssv-oracle/eth/syncer"
	"ssv-oracle/logger"
	"ssv-oracle/merkle"
	"ssv-oracle/storage"
	"ssv-oracle/txmanager"
)

const retryDelay = 5 * time.Second

// Config holds Updater configuration.
type Config struct {
	Storage        *storage.Storage
	ContractClient *contract.Client
	Syncer         *syncer.EventSyncer
}

// Updater listens for RootCommitted events and updates cluster balances on-chain.
type Updater struct {
	storage        updaterStorage
	contractClient *contract.Client
	syncer         *syncer.EventSyncer

	lastProcessedBlock uint64 // Deduplication: skip events for already-processed blocks
}

type updaterStorage interface {
	GetCluster(ctx context.Context, clusterID []byte) (*storage.ClusterRow, error)
	GetCommitByBlock(ctx context.Context, blockNum uint64) (*storage.OracleCommit, error)
}

type processStats struct {
	updated int
	skipped int
	failed  int
}

// New creates a new Updater instance.
func New(cfg *Config) *Updater {
	return &Updater{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		syncer:         cfg.Syncer,
	}
}

// Run starts the updater main loop, listening for RootCommitted events.
func (u *Updater) Run(ctx context.Context) error {
	for {
		err := u.subscribeAndProcess(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			logger.Errorw("Subscription failed, reconnecting", "error", err)
		}

		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// subscribeAndProcess subscribes to events and processes them until error or context cancellation.
func (u *Updater) subscribeAndProcess(ctx context.Context) error {
	events, errChan, err := u.contractClient.SubscribeRootCommitted(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	logger.Info("Subscribed to RootCommitted events")

	for {
		select {
		case <-ctx.Done():
			logger.Info("Updater stopping")
			return ctx.Err()

		case err, ok := <-errChan:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("error channel closed unexpectedly")
			}
			if err != nil {
				return fmt.Errorf("subscription error: %w", err)
			}

		case event, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("event channel closed unexpectedly")
			}
			u.handleEvent(ctx, event)
		}
	}
}

func (u *Updater) handleEvent(ctx context.Context, event *contract.RootCommittedEvent) {
	log := logger.With("blockNum", event.BlockNum)

	// Deduplicate by block number
	if event.BlockNum <= u.lastProcessedBlock {
		log.Debugw("Skipping duplicate RootCommitted event",
			"lastProcessed", u.lastProcessedBlock)
		return
	}

	log.Infow("Received RootCommitted",
		"merkleRoot", fmt.Sprintf("0x%x", event.MerkleRoot),
		"txHash", event.TxHash.Hex())

	commit, err := u.storage.GetCommitByBlock(ctx, event.BlockNum)
	if err != nil {
		log.Errorw("Failed to lookup commit", "error", err)
		return
	}
	if commit == nil {
		log.Warnw("Skipping RootCommitted event - not from this oracle",
			"merkleRoot", fmt.Sprintf("0x%x", event.MerkleRoot))
		return
	}

	log.Infow("Found commit",
		"targetEpoch", commit.TargetEpoch,
		"round", commit.RoundID)

	if err := u.processCommit(ctx, commit); err != nil {
		log.Errorw("Failed to process commit", "error", err)
		return
	}

	u.lastProcessedBlock = event.BlockNum
}

func (u *Updater) processCommit(ctx context.Context, commit *storage.OracleCommit) error {
	log := logger.With("blockNum", commit.ReferenceBlock, "targetEpoch", commit.TargetEpoch)
	log.Infow("Processing root",
		"committedRoot", fmt.Sprintf("0x%x", commit.MerkleRoot))

	if len(commit.ClusterBalances) == 0 {
		log.Info("No clusters to update")
		return nil
	}

	tree := u.buildMerkleTree(commit.ClusterBalances)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", tree.Root),
		"clusters", len(commit.ClusterBalances))

	if !bytes.Equal(tree.Root[:], commit.MerkleRoot) {
		return fmt.Errorf("root mismatch: computed=0x%x, committed=0x%x",
			tree.Root, commit.MerkleRoot)
	}

	log.Info("Root validated, processing clusters")

	stats, staleLeaves := u.processAllClusters(ctx, commit.ReferenceBlock, tree)

	if len(staleLeaves) > 0 {
		log.Infow("Syncing for stale clusters", "count", len(staleLeaves))
		if err := u.syncer.SyncClustersToHead(ctx); err != nil {
			log.Errorw("Failed to sync", "error", err)
		} else {
			for _, leaf := range staleLeaves {
				if ctx.Err() != nil {
					break
				}
				clusterID := fmt.Sprintf("%x", leaf.ClusterID)
				ok, err := u.processCluster(ctx, commit.ReferenceBlock, leaf, tree)
				if err != nil {
					stats.failed++
					log.Warnw("Cluster still failing", "clusterID", clusterID, "error", err)
					continue
				}
				if ok {
					stats.updated++
				} else {
					stats.skipped++
				}
			}
		}
	}

	u.logStats(log, stats)

	return nil
}

func (u *Updater) buildMerkleTree(balances []storage.ClusterBalance) *merkle.Tree {
	clusterMap := make(map[[32]byte]uint32)
	for _, bal := range balances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}
	return merkle.NewTree(clusterMap)
}

func (u *Updater) processAllClusters(ctx context.Context, blockNum uint64, tree *merkle.Tree) (processStats, []merkle.Leaf) {
	var stats processStats
	var staleLeaves []merkle.Leaf

	for _, leaf := range tree.Leaves {
		if ctx.Err() != nil {
			break
		}

		ok, err := u.processCluster(ctx, blockNum, leaf, tree)
		if err != nil {
			clusterID := fmt.Sprintf("%x", leaf.ClusterID)

			if revertErr, isRevert := txmanager.IsRevertError(err); isRevert {
				if revertErr.Reason == "IncorrectClusterState" {
					staleLeaves = append(staleLeaves, leaf)
					logger.Debugw("Cluster stale", "clusterID", clusterID, "error", revertErr)
				} else {
					stats.skipped++
					logger.Debugw("Cluster skipped", "clusterID", clusterID, "error", revertErr)
				}
				continue
			}

			stats.failed++
			logger.Errorw("Cluster failed", "clusterID", clusterID, "error", err)
			continue
		}

		if ok {
			stats.updated++
		} else {
			stats.skipped++
		}
	}

	return stats, staleLeaves
}

func (u *Updater) logStats(log logger.Logger, stats processStats) {
	log.Infow("Commit complete",
		"updated", stats.updated,
		"skipped", stats.skipped,
		"failed", stats.failed)
}

func toContractCluster(c *storage.ClusterRow) contract.Cluster {
	return contract.Cluster{
		ValidatorCount:  c.ValidatorCount,
		NetworkFeeIndex: c.NetworkFeeIndex,
		Index:           c.Index,
		Active:          c.IsActive,
		Balance:         c.Balance,
	}
}

func (u *Updater) processCluster(ctx context.Context, blockNum uint64, leaf merkle.Leaf, tree *merkle.Tree) (bool, error) {
	clusterID := fmt.Sprintf("%x", leaf.ClusterID)

	cluster, err := u.storage.GetCluster(ctx, leaf.ClusterID[:])
	if err != nil {
		return false, fmt.Errorf("failed to get cluster: %w", err)
	}
	if cluster == nil {
		logger.Debugw("Cluster not found, skipping", "clusterID", clusterID)
		return false, nil
	}

	proof, err := tree.GetProof(leaf.ClusterID)
	if err != nil {
		return false, fmt.Errorf("failed to get proof: %w", err)
	}

	owner := common.BytesToAddress(cluster.OwnerAddress)
	currentBalance, err := u.contractClient.GetClusterEffectiveBalance(ctx, owner, cluster.OperatorIDs, toContractCluster(cluster))
	if err != nil {
		return false, fmt.Errorf("failed to check current balance: %w", err)
	}
	if currentBalance == leaf.EffectiveBalance {
		logger.Debugw("Balance unchanged, skipping",
			"clusterID", clusterID,
			"balance", currentBalance)
		return false, nil
	}

	receipt, err := u.submitUpdate(ctx, blockNum, cluster, leaf, proof)
	if err != nil {
		return false, err
	}

	logger.Infow("Tx confirmed",
		"clusterID", clusterID,
		"txHash", receipt.TxHash.Hex(),
		"effectiveBalance", leaf.EffectiveBalance,
		"block", receipt.BlockNumber.Uint64())

	return true, nil
}

func (u *Updater) submitUpdate(
	ctx context.Context,
	blockNum uint64,
	cluster *storage.ClusterRow,
	leaf merkle.Leaf,
	proof [][32]byte,
) (*types.Receipt, error) {
	return u.contractClient.UpdateClusterBalance(
		ctx,
		blockNum,
		common.BytesToAddress(cluster.OwnerAddress),
		cluster.OperatorIDs,
		toContractCluster(cluster),
		leaf.EffectiveBalance,
		proof,
	)
}
