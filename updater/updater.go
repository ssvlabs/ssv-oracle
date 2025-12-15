package updater

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
	"ssv-oracle/txmanager"
)

const retryDelay = 5 * time.Second

// Syncer defines the interface for head sync operations.
type Syncer interface {
	SyncClustersToHead(ctx context.Context) error
}

// Config holds Updater configuration.
type Config struct {
	Storage        *ethsync.Storage
	ContractClient *contract.Client
	Syncer         Syncer
}

// Updater listens for RootCommitted events and updates cluster balances on-chain.
type Updater struct {
	storage        storage
	contractClient *contract.Client
	syncer         Syncer
}

type storage interface {
	GetCluster(ctx context.Context, clusterID []byte) (*ethsync.ClusterRow, error)
	GetCommitByBlock(ctx context.Context, blockNum uint64) (*ethsync.OracleCommit, error)
}

type processStats struct {
	updated  int
	skipped  int
	failures map[txmanager.FailureReason]int
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
		delay, err := u.subscribeAndProcess(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Errorw("Subscription failed, reconnecting", "error", err, "retryDelay", delay)
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// subscribeAndProcess subscribes to events and processes them.
// Returns the delay to use before retrying.
func (u *Updater) subscribeAndProcess(ctx context.Context) (time.Duration, error) {
	events, errChan, err := u.contractClient.SubscribeRootCommitted(ctx, nil)
	if err != nil {
		return retryDelay, fmt.Errorf("failed to subscribe: %w", err)
	}

	logger.Info("Subscribed to RootCommitted events")

	for {
		select {
		case <-ctx.Done():
			logger.Info("Updater stopping")
			return 0, ctx.Err()

		case err, ok := <-errChan:
			if !ok {
				return retryDelay, fmt.Errorf("error channel closed")
			}
			return retryDelay, fmt.Errorf("subscription error: %w", err)

		case event, ok := <-events:
			if !ok {
				return retryDelay, fmt.Errorf("event channel closed")
			}
			u.handleEvent(ctx, event)
		}
	}
}

func (u *Updater) handleEvent(ctx context.Context, event *contract.RootCommittedEvent) {
	log := logger.With("blockNum", event.BlockNum)
	log.Infow("Received RootCommitted",
		"merkleRoot", fmt.Sprintf("0x%x", event.MerkleRoot))

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
	}
}

func (u *Updater) processCommit(ctx context.Context, commit *ethsync.OracleCommit) error {
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

	// View calls validate cluster state for free - only sync if needed
	stats, hadStaleData := u.processAllClusters(ctx, commit.ReferenceBlock, tree)
	if hadStaleData {
		log.Info("Stale cluster data detected, syncing to head and retrying")
		if err := u.syncer.SyncClustersToHead(ctx); err != nil {
			log.Errorw("Failed to sync clusters to head", "error", err)
		} else {
			var stillStale bool
			stats, stillStale = u.processAllClusters(ctx, commit.ReferenceBlock, tree)
			if stillStale {
				log.Warn("Cluster data still stale after sync - on-chain state may have changed")
			}
		}
	}

	u.logStats(log, stats)

	return nil
}

func (u *Updater) buildMerkleTree(balances []ethsync.ClusterBalance) *merkle.MerkleTree {
	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range balances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}
	return merkle.BuildMerkleTreeWithProofs(clusterMap)
}

// isStaleClusterError checks for IncorrectClusterState revert from the contract,
// which indicates local cluster data doesn't match on-chain state.
func isStaleClusterError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "IncorrectClusterState")
}

func (u *Updater) processAllClusters(ctx context.Context, blockNum uint64, tree *merkle.MerkleTree) (processStats, bool) {
	stats := processStats{failures: make(map[txmanager.FailureReason]int)}
	hadStaleData := false

	for _, leaf := range tree.Leaves {
		if ctx.Err() != nil {
			break
		}
		ok, err := u.processCluster(ctx, blockNum, leaf, tree)
		if err != nil {
			if isStaleClusterError(err) {
				hadStaleData = true
				logger.Debugw("Cluster data stale",
					"clusterID", fmt.Sprintf("%x", leaf.ClusterID))
				continue
			}

			reason, retryable := txmanager.ClassifyError(err)
			stats.failures[reason]++

			logFunc := logger.Warnw
			if !retryable {
				logFunc = logger.Errorw
			}
			logFunc("Failed to process cluster",
				"clusterID", fmt.Sprintf("%x", leaf.ClusterID),
				"reason", reason,
				"retryable", retryable,
				"error", err)
			continue
		}
		if ok {
			stats.updated++
		} else {
			stats.skipped++
		}
	}

	return stats, hadStaleData
}

func (u *Updater) logStats(log logger.Logger, stats processStats) {
	fields := []any{
		"updated", stats.updated,
		"skipped", stats.skipped,
	}
	for reason, count := range stats.failures {
		fields = append(fields, string(reason), count)
	}
	log.Infow("Commit complete", fields...)
}

func toContractCluster(c *ethsync.ClusterRow) contract.Cluster {
	return contract.Cluster{
		ValidatorCount:  c.ValidatorCount,
		NetworkFeeIndex: c.NetworkFeeIndex,
		Index:           c.Index,
		Active:          c.IsActive,
		Balance:         c.Balance,
	}
}

func (u *Updater) processCluster(ctx context.Context, blockNum uint64, leaf merkle.Leaf, tree *merkle.MerkleTree) (bool, error) {
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
	cluster *ethsync.ClusterRow,
	leaf merkle.Leaf,
	proof [][32]byte,
) (*types.Receipt, error) {
	return u.contractClient.UpdateClusterBalance(
		ctx,
		blockNum,
		common.BytesToAddress(cluster.OwnerAddress),
		cluster.OperatorIDs,
		toContractCluster(cluster),
		new(big.Int).SetUint64(leaf.EffectiveBalance),
		proof,
	)
}
