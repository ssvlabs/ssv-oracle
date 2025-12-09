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
	"ssv-oracle/txmanager"

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
	skipped := 0
	failures := make(map[txmanager.FailureReason]int)

	for _, leaf := range tree.Leaves {
		ok, err := u.processCluster(ctx, commit.ReferenceBlock, leaf, tree)
		if err != nil {
			reason, retryable := txmanager.ClassifyError(err)
			failures[reason]++

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
			updated++
		} else {
			skipped++
		}
	}

	logFields := []interface{}{
		"updated", updated,
		"skipped", skipped,
	}
	for reason, count := range failures {
		logFields = append(logFields, string(reason), count)
	}
	log.Infow("Commit complete", logFields...)

	return nil
}

// processCluster updates a single cluster's effective balance on-chain.
// Returns (true, nil) if update was submitted successfully.
// Returns (false, nil) if update was skipped (balance unchanged or cluster not found).
// Returns (false, err) if update failed.
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

	currentBalance, err := u.contractClient.GetClusterEffectiveBalance(ctx, leaf.ClusterID)
	if err != nil {
		return false, fmt.Errorf("failed to check current balance: %w", err)
	}
	if currentBalance == leaf.EffectiveBalance {
		logger.Debugw("Balance unchanged, skipping",
			"clusterID", clusterID,
			"balance", currentBalance)
		return false, nil
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
		return false, err
	}

	logger.Infow("Tx confirmed",
		"clusterID", clusterID,
		"txHash", receipt.TxHash.Hex(),
		"effectiveBalance", leaf.EffectiveBalance,
		"block", receipt.BlockNumber.Uint64())

	return true, nil
}
