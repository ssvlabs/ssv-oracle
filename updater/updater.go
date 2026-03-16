package updater

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ssvlabs/ssv-oracle/contract"
	"github.com/ssvlabs/ssv-oracle/eth/syncer"
	"github.com/ssvlabs/ssv-oracle/logger"
	"github.com/ssvlabs/ssv-oracle/merkle"
	"github.com/ssvlabs/ssv-oracle/storage"
	"github.com/ssvlabs/ssv-oracle/txmanager"
)

const (
	subscriptionRetryDelay = 5 * time.Second
	commitLookupRetryDelay = 10 * time.Second
)

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
			logger.Errorw("Subscription failed", "retryIn", subscriptionRetryDelay.String(), "error", err)
		}

		select {
		case <-time.After(subscriptionRetryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// subscribeAndProcess subscribes to events and processes them until error or context cancellation.
func (u *Updater) subscribeAndProcess(ctx context.Context) error {
	events, errChan, err := u.contractClient.SubscribeRootCommitted(ctx, nil)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
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
	log := logger.With(
		"blockNum", event.BlockNum,
		"merkleRoot", fmt.Sprintf("0x%x", event.MerkleRoot))

	log.Info("RootCommitted event")

	// Deduplicate by block number
	if event.BlockNum <= u.lastProcessedBlock {
		log.Debugw("Duplicate RootCommitted event", "lastProcessedBlock", u.lastProcessedBlock)
		return
	}

	commit, err := u.storage.GetCommitByBlock(ctx, event.BlockNum)
	if err != nil {
		log.Errorw("Failed to lookup commit", "error", err)
		return
	}
	if commit == nil {
		select {
		case <-time.After(commitLookupRetryDelay):
		case <-ctx.Done():
			return
		}
		commit, err = u.storage.GetCommitByBlock(ctx, event.BlockNum)
		if err != nil {
			log.Errorw("Failed to lookup commit on retry", "error", err)
			return
		}
	}

	if commit == nil {
		log.Warnw("Commit not found - likely from another oracle")
		return
	}

	if err := u.processCommit(ctx, commit); err != nil {
		log.Errorw("Failed to process commit", "error", err)
		return
	}

	u.lastProcessedBlock = event.BlockNum
}

func (u *Updater) processCommit(ctx context.Context, commit *storage.OracleCommit) error {
	log := logger.With("blockNum", commit.ReferenceBlock, "targetEpoch", commit.TargetEpoch)
	start := time.Now()

	if len(commit.ClusterBalances) == 0 {
		log.Info("No clusters to update")
		return nil
	}

	tree := buildTree(commit.ClusterBalances)
	log.Debugw("Merkle tree built",
		"root", fmt.Sprintf("0x%x", tree.Root),
		"clusters", len(commit.ClusterBalances))

	if !bytes.Equal(tree.Root[:], commit.MerkleRoot) {
		return fmt.Errorf("root mismatch: computed=0x%x, committed=0x%x",
			tree.Root, commit.MerkleRoot)
	}

	stats, staleLeaves := u.processAllClusters(ctx, commit.ReferenceBlock, tree)

	if len(staleLeaves) > 0 {
		log.Debugw("Stale clusters detected", "count", len(staleLeaves))
		if err := u.syncer.SyncClustersToHead(ctx); err != nil {
			log.Errorw("Failed to sync clusters to head", "error", err)
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

	recordClusterUpdates(ctx, stats)

	log.Infow("Commit complete",
		"updated", stats.updated,
		"skipped", stats.skipped,
		"failed", stats.failed,
		"took", time.Since(start).Round(time.Millisecond).String())

	return nil
}

func buildTree(balances []storage.ClusterBalance) *merkle.Tree {
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

	for _, leaf := range tree.Leaves() {
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
		return false, fmt.Errorf("get cluster: %w", err)
	}
	if cluster == nil {
		logger.Debugw("Cluster not found", "clusterID", clusterID)
		return false, nil
	}

	proof, err := tree.GetProof(leaf.ClusterID)
	if err != nil {
		return false, fmt.Errorf("get proof: %w", err)
	}

	owner := common.BytesToAddress(cluster.OwnerAddress)
	currentBalance, err := u.contractClient.GetClusterEffectiveBalance(ctx, owner, cluster.OperatorIDs, toContractCluster(cluster))
	if err != nil {
		return false, fmt.Errorf("get current balance: %w", err)
	}
	if currentBalance == leaf.EffectiveBalance {
		logger.Debugw("Balance unchanged", "clusterID", clusterID, "balance", currentBalance)
		return false, nil
	}

	logger.Debugw("Effective balance changed",
		"clusterID", clusterID,
		"previousEffectiveBalance", currentBalance,
		"newEffectiveBalance", leaf.EffectiveBalance)

	receipt, err := u.contractClient.UpdateClusterBalance(
		ctx,
		blockNum,
		common.BytesToAddress(cluster.OwnerAddress),
		cluster.OperatorIDs,
		toContractCluster(cluster),
		leaf.EffectiveBalance,
		proof,
	)
	if err != nil {
		return false, fmt.Errorf("update cluster balance: %w", err)
	}

	logger.Debugw("Tx confirmed",
		"clusterID", clusterID,
		"txHash", receipt.TxHash.Hex(),
		"effectiveBalance", leaf.EffectiveBalance,
		"block", receipt.BlockNumber.Uint64())

	return true, nil
}
