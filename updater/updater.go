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

// Updater listens for RootCommitted events and submits merkle proofs to update cluster balances.
type Updater struct {
	storage        *ethsync.PostgresStorage
	contractClient *contract.Client
	spec           *ethsync.Spec
	mockMode       bool
	dbConnString   string // For LISTEN/NOTIFY in mock mode
}

// Config holds updater configuration.
type Config struct {
	Storage        *ethsync.PostgresStorage
	ContractClient *contract.Client
	Spec           *ethsync.Spec
	MockMode       bool
	DBConnString   string // Required for mock mode (LISTEN/NOTIFY)
}

// New creates a new Updater.
func New(cfg *Config) *Updater {
	return &Updater{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		spec:           cfg.Spec,
		mockMode:       cfg.MockMode,
		dbConnString:   cfg.DBConnString,
	}
}

// Run starts the updater.
// In mock mode: listens for PostgreSQL NOTIFY on new commits.
// In real mode: subscribes to RootCommitted events.
func (u *Updater) Run(ctx context.Context) error {
	logger.Info("Updater starting")

	if u.mockMode {
		return u.runMockMode(ctx)
	}
	return u.runRealMode(ctx)
}

// runMockMode listens for new commits via PostgreSQL LISTEN/NOTIFY.
func (u *Updater) runMockMode(ctx context.Context) error {
	logger.Info("Running in mock mode (LISTEN/NOTIFY)")

	// Start listening for new commits
	roundChan, err := u.storage.ListenForCommits(ctx, u.dbConnString)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	logger.Info("Listening for new oracle commits")

	for {
		select {
		case <-ctx.Done():
			logger.Info("Updater stopping")
			return ctx.Err()

		case roundID, ok := <-roundChan:
			if !ok {
				logger.Warn("Listener channel closed")
				return fmt.Errorf("listener closed")
			}

			log := logger.With("round", roundID)
			log.Info("Received notification")

			// Get the commit details
			commit, err := u.storage.GetCommitByRound(ctx, roundID)
			if err != nil {
				log.Errorw("Failed to get commit", "error", err)
				continue
			}
			if commit == nil {
				log.Warn("Commit not found")
				continue
			}

			if err := u.processCommit(ctx, commit.ReferenceBlock, commit.TargetEpoch, commit.MerkleRoot); err != nil {
				log.Errorw("Failed to process commit", "error", err)
			}
		}
	}
}

// runRealMode subscribes to RootCommitted events.
func (u *Updater) runRealMode(ctx context.Context) error {
	logger.Info("Running in real mode (event subscription)")

	for {
		// Subscribe to new events only (nil = from latest block)
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

		// Process events until error or context done
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

				// Look up targetEpoch from oracle_commits by reference block
				commit, err := u.storage.GetCommitByReferenceBlock(ctx, event.BlockNum)
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

				if err := u.processCommit(ctx, event.BlockNum, commit.TargetEpoch, event.MerkleRoot[:]); err != nil {
					log.Errorw("Failed to process commit", "error", err)
				}
			}
		}

		// Brief pause before reconnecting
		logger.Info("Pausing 5s before reconnecting")
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// processCommit rebuilds the merkle tree, validates root, and submits proofs.
func (u *Updater) processCommit(ctx context.Context, blockNum, targetEpoch uint64, committedRoot []byte) error {
	log := logger.With("blockNum", blockNum, "targetEpoch", targetEpoch)
	log.Infow("Processing commit",
		"committedRoot", fmt.Sprintf("0x%x", committedRoot[:8]))

	// 1. Query cluster balances from DB for targetEpoch
	clusterBalances, err := u.storage.GetClusterBalances(ctx, targetEpoch, u.spec.SlotsPerEpoch)
	if err != nil {
		return fmt.Errorf("failed to get cluster balances: %w", err)
	}

	log.Infow("Found clusters", "count", len(clusterBalances))

	if len(clusterBalances) == 0 {
		log.Info("No clusters to update")
		return nil
	}

	// 2. Build merkle tree with proofs
	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range clusterBalances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.TotalEffectiveBalance
		logger.Debugw("Cluster balance",
			"clusterID", fmt.Sprintf("%x", bal.ClusterID[:8]),
			"balance", bal.TotalEffectiveBalance,
			"validators", bal.ValidatorCount)
	}

	tree := merkle.BuildMerkleTreeWithProofs(clusterMap)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", tree.Root[:8]))

	// 3. Validate: computed root == committedRoot
	if !bytes.Equal(tree.Root[:], committedRoot) {
		return fmt.Errorf("root mismatch: computed=0x%x, committed=0x%x",
			tree.Root[:8], committedRoot[:8])
	}

	log.Infow("Root validated, processing clusters", "count", len(clusterBalances))

	// 4. For each cluster: get state, generate proof, call UpdateClusterBalance
	updated := 0
	skipped := 0
	errors := 0

	for _, leaf := range tree.Leaves {
		clusterLog := log.With("clusterID", fmt.Sprintf("%x", leaf.ClusterID[:8]))

		if err := u.processCluster(ctx, clusterLog, blockNum, leaf, tree); err != nil {
			clusterLog.Warnw("Failed to process cluster", "error", err)
			errors++
			continue
		}
		updated++
	}

	log.Infow("Commit complete",
		"updated", updated,
		"skipped", skipped,
		"errors", errors)

	return nil
}

// processCluster handles a single cluster update.
func (u *Updater) processCluster(ctx context.Context, log *zap.SugaredLogger, blockNum uint64, leaf merkle.Leaf, tree *merkle.MerkleTree) error {
	// Get cluster state from DB
	clusterState, err := u.storage.GetClusterState(ctx, leaf.ClusterID[:])
	if err != nil {
		return fmt.Errorf("failed to get cluster state: %w", err)
	}
	if clusterState == nil {
		log.Warn("Cluster not found in state")
		return nil
	}

	// Generate merkle proof
	proof, err := tree.GetProof(leaf.ClusterID)
	if err != nil {
		return fmt.Errorf("failed to get proof: %w", err)
	}

	// Convert to contract types
	owner := common.BytesToAddress(clusterState.OwnerAddress)
	cluster := contract.Cluster{
		ValidatorCount:  clusterState.ValidatorCount,
		NetworkFeeIndex: clusterState.NetworkFeeIndex,
		Index:           clusterState.Index,
		Active:          clusterState.IsActive,
		Balance:         clusterState.Balance,
	}

	// Call UpdateClusterBalance
	tx, err := u.contractClient.UpdateClusterBalance(
		ctx,
		blockNum,
		owner,
		clusterState.OperatorIDs,
		cluster,
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

		// Wait for transaction to be mined and check status
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
