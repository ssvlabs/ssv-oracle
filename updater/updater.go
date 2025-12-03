package updater

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/oracle"
	"ssv-oracle/pkg/ethsync"

	"github.com/ethereum/go-ethereum/common"
)

// Updater listens for RootCommitted events and submits merkle proofs to update cluster balances.
type Updater struct {
	storage        *ethsync.PostgresStorage
	contractClient *contract.Client
	spec           *ethsync.Spec
	timingPhases   []oracle.TimingPhase
	mockMode       bool
	dbConnString   string // For LISTEN/NOTIFY in mock mode
}

// Config holds updater configuration.
type Config struct {
	Storage        *ethsync.PostgresStorage
	ContractClient *contract.Client
	Spec           *ethsync.Spec
	TimingPhases   []oracle.TimingPhase
	MockMode       bool
	DBConnString   string // Required for mock mode (LISTEN/NOTIFY)
}

// New creates a new Updater.
func New(cfg *Config) *Updater {
	return &Updater{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		spec:           cfg.Spec,
		timingPhases:   cfg.TimingPhases,
		mockMode:       cfg.MockMode,
		dbConnString:   cfg.DBConnString,
	}
}

// Run starts the updater.
// In mock mode: listens for PostgreSQL NOTIFY on new commits.
// In real mode: subscribes to RootCommitted events.
func (u *Updater) Run(ctx context.Context) error {
	log.Println("Updater starting...")

	if u.mockMode {
		return u.runMockMode(ctx)
	}
	return u.runRealMode(ctx)
}

// runMockMode listens for new commits via PostgreSQL LISTEN/NOTIFY.
func (u *Updater) runMockMode(ctx context.Context) error {
	log.Println("Updater running in mock mode (LISTEN/NOTIFY)")

	// Start listening for new commits
	roundChan, err := u.storage.ListenForCommits(ctx, u.dbConnString)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	log.Println("Listening for new oracle commits...")

	for {
		select {
		case <-ctx.Done():
			log.Println("Updater stopping...")
			return ctx.Err()

		case roundID, ok := <-roundChan:
			if !ok {
				log.Println("Listener channel closed, exiting...")
				return fmt.Errorf("listener closed")
			}

			log.Printf("Received notification for round %d", roundID)

			// Get the commit details
			commit, err := u.storage.GetCommitByRound(ctx, roundID)
			if err != nil {
				log.Printf("Error getting commit for round %d: %v", roundID, err)
				continue
			}
			if commit == nil {
				log.Printf("Warning: commit for round %d not found", roundID)
				continue
			}

			if err := u.processCommit(ctx, commit.ReferenceBlock, commit.TargetEpoch, commit.MerkleRoot); err != nil {
				log.Printf("Error processing commit block %d: %v", commit.ReferenceBlock, err)
			}
		}
	}
}

// runRealMode subscribes to RootCommitted events.
func (u *Updater) runRealMode(ctx context.Context) error {
	log.Println("Updater running in real mode (event subscription)")

	for {
		// Subscribe to new events only (nil = from latest block)
		events, errChan, err := u.contractClient.SubscribeRootCommitted(ctx, nil)
		if err != nil {
			log.Printf("Failed to subscribe to events: %v, retrying in 10s...", err)
			select {
			case <-time.After(10 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		log.Println("Subscribed to RootCommitted events")

		// Process events until error or context done
	innerLoop:
		for {
			select {
			case <-ctx.Done():
				log.Println("Updater stopping...")
				return ctx.Err()

			case err := <-errChan:
				log.Printf("Subscription error: %v, reconnecting...", err)
				break innerLoop

			case event, ok := <-events:
				if !ok {
					log.Println("Event channel closed, reconnecting...")
					break innerLoop
				}

				// Calculate targetEpoch from block timestamp
				targetEpoch := u.calculateTargetEpoch(event.Timestamp)

				log.Printf("Received RootCommitted: blockNum=%d, timestamp=%d, targetEpoch=%d, merkleRoot=0x%x",
					event.BlockNum, event.Timestamp, targetEpoch, event.MerkleRoot[:8])

				if err := u.processCommit(ctx, event.BlockNum, targetEpoch, event.MerkleRoot[:]); err != nil {
					log.Printf("Error processing commit block %d: %v", event.BlockNum, err)
				}
			}
		}

		// Brief pause before reconnecting
		log.Println("Pausing 5s before reconnecting...")
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// processCommit rebuilds the merkle tree, validates root, and submits proofs.
func (u *Updater) processCommit(ctx context.Context, blockNum, targetEpoch uint64, committedRoot []byte) error {
	log.Printf("Processing commit (blockNum=%d, targetEpoch=%d, committedRoot=0x%x)",
		blockNum, targetEpoch, committedRoot[:8])

	// 1. Query cluster balances from DB for targetEpoch
	clusterBalances, err := u.storage.GetClusterBalances(ctx, targetEpoch, u.spec.SlotsPerEpoch)
	if err != nil {
		return fmt.Errorf("failed to get cluster balances: %w", err)
	}

	log.Printf("Block %d: found %d clusters with balances", blockNum, len(clusterBalances))

	if len(clusterBalances) == 0 {
		log.Printf("Block %d: no clusters to update", blockNum)
		return nil
	}

	// 2. Build merkle tree with proofs
	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range clusterBalances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.TotalEffectiveBalance
		log.Printf("  Cluster %x: totalBalance=%d Gwei (%d validators)",
			bal.ClusterID[:8], bal.TotalEffectiveBalance, bal.ValidatorCount)
	}

	tree := merkle.BuildMerkleTreeWithProofs(clusterMap)
	log.Printf("Block %d: built merkle tree with root 0x%x", blockNum, tree.Root[:8])

	// 3. Validate: computed root == committedRoot
	if !bytes.Equal(tree.Root[:], committedRoot) {
		return fmt.Errorf("root mismatch: computed=0x%x, committed=0x%x",
			tree.Root[:8], committedRoot[:8])
	}

	log.Printf("Block %d: root validated ✓, processing %d clusters", blockNum, len(clusterBalances))

	// 4. For each cluster: get state, generate proof, call UpdateClusterBalance
	updated := 0
	skipped := 0
	errors := 0

	for _, leaf := range tree.Leaves {
		// Get cluster state from DB
		clusterState, err := u.storage.GetClusterState(ctx, leaf.ClusterID[:])
		if err != nil {
			log.Printf("Warning: failed to get cluster state for %x: %v", leaf.ClusterID[:8], err)
			errors++
			continue
		}
		if clusterState == nil {
			log.Printf("Warning: cluster %x not found in state", leaf.ClusterID[:8])
			skipped++
			continue
		}

		// Generate merkle proof
		proof, err := tree.GetProof(leaf.ClusterID)
		if err != nil {
			log.Printf("Warning: failed to get proof for cluster %x: %v", leaf.ClusterID[:8], err)
			errors++
			continue
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
			log.Printf("Warning: failed to update cluster %x: %v", leaf.ClusterID[:8], err)
			errors++
			continue
		}

		if u.mockMode {
			log.Printf("  Cluster %x: effectiveBalance=%d Gwei, proof=%d siblings (mock)",
				leaf.ClusterID[:8], leaf.EffectiveBalance, len(proof))
		} else {
			log.Printf("  Cluster %x: effectiveBalance=%d Gwei, tx=%s (waiting for confirmation...)",
				leaf.ClusterID[:8], leaf.EffectiveBalance, tx.Hash().Hex())

			// Wait for transaction to be mined and check status
			receipt, err := u.contractClient.WaitForReceipt(ctx, tx)
			if err != nil {
				log.Printf("Warning: tx %s failed to mine for cluster %x: %v",
					tx.Hash().Hex(), leaf.ClusterID[:8], err)
				errors++
				continue
			}
			if receipt.Status != 1 {
				log.Printf("Warning: tx %s reverted for cluster %x",
					tx.Hash().Hex(), leaf.ClusterID[:8])
				errors++
				continue
			}
			log.Printf("  Cluster %x: tx %s confirmed in block %d",
				leaf.ClusterID[:8], tx.Hash().Hex(), receipt.BlockNumber.Uint64())
		}

		updated++
	}

	log.Printf("Block %d complete: %d updated, %d skipped, %d errors",
		blockNum, updated, skipped, errors)

	return nil
}

// calculateTargetEpoch derives the target epoch from a block timestamp.
// The block was created after the oracle committed, so we find the most recent
// targetEpoch that would have been finalized before this block.
func (u *Updater) calculateTargetEpoch(timestamp uint64) uint64 {
	blockEpoch := u.spec.EpochAtTimestamp(timestamp)

	// Find the timing phase and calculate which targetEpoch this corresponds to
	phase := oracle.GetTimingForEpoch(u.timingPhases, blockEpoch)
	_, targetEpoch, ready := phase.RoundForFinalizedEpoch(blockEpoch)
	if !ready {
		return phase.StartEpoch
	}
	return targetEpoch
}
