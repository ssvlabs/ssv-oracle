package updater

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"

	"github.com/ethereum/go-ethereum/common"
)

type Updater struct {
	storage        *ethsync.PostgresStorage
	contractClient *contract.Client
	mockMode       bool
	dbConnString   string
}

type Config struct {
	Storage        *ethsync.PostgresStorage
	ContractClient *contract.Client
	MockMode       bool
	DBConnString   string
}

func New(cfg *Config) *Updater {
	return &Updater{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		mockMode:       cfg.MockMode,
		dbConnString:   cfg.DBConnString,
	}
}

func (u *Updater) Run(ctx context.Context) error {
	logger.Info("Updater starting")

	if u.mockMode {
		return u.runMockMode(ctx)
	}
	return u.runRealMode(ctx)
}

func (u *Updater) runMockMode(ctx context.Context) error {
	logger.Info("Running in mock mode (LISTEN/NOTIFY)")

	blockChan, err := u.storage.ListenForCommits(ctx, u.dbConnString)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	logger.Info("Listening for new oracle commits")

	for {
		select {
		case <-ctx.Done():
			logger.Info("Updater stopping")
			return ctx.Err()

		case blockNum, ok := <-blockChan:
			if !ok {
				logger.Warn("Listener channel closed")
				return fmt.Errorf("listener closed")
			}

			log := logger.With("blockNum", blockNum)
			log.Info("Received notification")

			commit, err := u.storage.GetCommitByBlock(ctx, blockNum)
			if err != nil {
				log.Errorw("Failed to get commit", "error", err)
				continue
			}
			if commit == nil {
				log.Warn("Commit not found")
				continue
			}

			if err := u.processCommit(ctx, commit); err != nil {
				log.Errorw("Failed to process commit", "error", err)
			}
		}
	}
}

func (u *Updater) runRealMode(ctx context.Context) error {
	logger.Info("Running in real mode (event subscription)")

	for {
		events, errChan, err := u.contractClient.SubscribeRootCommitted(ctx, nil)
		if err != nil {
			logger.Errorw("Failed to subscribe, retrying in 10s", "error", err)
			select {
			case <-time.After(10 * time.Second):
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
					"merkleRoot", fmt.Sprintf("0x%x", event.MerkleRoot[:8]))

				commit, err := u.storage.GetCommitByBlock(ctx, event.BlockNum)
				if err != nil {
					log.Errorw("Failed to lookup commit", "error", err)
					continue
				}
				if commit == nil {
					log.Error("Commit not found - event from unknown source?")
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

		logger.Info("Pausing 5s before reconnecting")
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// processCommit rebuilds the merkle tree from stored balances, validates the root, and submits proofs.
func (u *Updater) processCommit(ctx context.Context, commit *ethsync.OracleCommit) error {
	log := logger.With("blockNum", commit.ReferenceBlock, "targetEpoch", commit.TargetEpoch)
	log.Infow("Processing commit",
		"committedRoot", fmt.Sprintf("0x%x", commit.MerkleRoot[:8]))

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
		logger.Debugw("Cluster balance",
			"clusterID", fmt.Sprintf("%x", bal.ClusterID[:8]),
			"balance", bal.EffectiveBalance)
	}

	tree := merkle.BuildMerkleTreeWithProofs(clusterMap)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", tree.Root[:8]))

	if !bytes.Equal(tree.Root[:], commit.MerkleRoot) {
		return fmt.Errorf("root mismatch: computed=0x%x, committed=0x%x",
			tree.Root[:8], commit.MerkleRoot[:8])
	}

	log.Infow("Root validated, processing clusters", "count", len(commit.ClusterBalances))

	updated := 0
	errors := 0

	for _, leaf := range tree.Leaves {
		clusterLog := log.With("clusterID", fmt.Sprintf("%x", leaf.ClusterID[:8]))

		if err := u.processCluster(ctx, clusterLog, commit.ReferenceBlock, leaf, tree); err != nil {
			clusterLog.Warnw("Failed to process cluster", "error", err)
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

func (u *Updater) processCluster(ctx context.Context, log *zap.SugaredLogger, blockNum uint64, leaf merkle.Leaf, tree *merkle.MerkleTree) error {
	cluster, err := u.storage.GetCluster(ctx, leaf.ClusterID[:])
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	if cluster == nil {
		log.Warn("Cluster not found")
		return nil
	}

	proof, err := tree.GetProof(leaf.ClusterID)
	if err != nil {
		return fmt.Errorf("failed to get proof: %w", err)
	}

	// Check if balance has changed before updating (saves gas)
	currentBalance, err := u.contractClient.GetClusterEffectiveBalance(ctx, leaf.ClusterID)
	if err != nil {
		log.Warnw("Failed to check current balance, skipping cluster", "error", err)
		return nil
	}
	if currentBalance == leaf.EffectiveBalance {
		log.Debugw("Balance unchanged, skipping", "balance", currentBalance)
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

	tx, err := u.contractClient.UpdateClusterBalance(
		ctx,
		blockNum,
		owner,
		cluster.OperatorIDs,
		contractCluster,
		leaf.EffectiveBalance,
		proof,
	)
	if err != nil {
		return fmt.Errorf("contract call failed: %w", err)
	}

	if u.mockMode {
		log.Debugw("Updated (mock)",
			"effectiveBalance", leaf.EffectiveBalance,
			"proofSize", len(proof))
	} else {
		log.Infow("Submitted tx, waiting for confirmation",
			"txHash", tx.Hash().Hex(),
			"effectiveBalance", leaf.EffectiveBalance)

		receipt, err := u.contractClient.WaitForReceipt(ctx, tx)
		if err != nil {
			return fmt.Errorf("tx failed to mine: %w", err)
		}
		if receipt.Status != 1 {
			return fmt.Errorf("tx reverted")
		}
		log.Infow("Tx confirmed",
			"txHash", tx.Hash().Hex(),
			"block", receipt.BlockNumber.Uint64())
	}

	return nil
}
