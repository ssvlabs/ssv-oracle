package oracle

import (
	"context"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
	"ssv-oracle/txmanager"
)

const errorRetryDelay = 10 * time.Second

// storage defines the interface the oracle needs for persistence.
type storage interface {
	GetActiveValidators(ctx context.Context) ([]ethsync.ActiveValidator, error)
	InsertPendingCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []ethsync.ClusterBalance) error
	UpdateCommitStatus(ctx context.Context, roundID uint64, status ethsync.CommitStatus, txHash []byte) error
}

// Oracle commits merkle roots of cluster effective balances to the SSV contract.
type Oracle struct {
	storage        storage
	contractClient *contract.Client
	phases         []CommitPhase
}

// Config holds Oracle configuration.
type Config struct {
	Storage        *ethsync.PostgresStorage
	ContractClient *contract.Client
	Phases         []CommitPhase
}

// New creates a new Oracle instance.
func New(cfg *Config) *Oracle {
	return &Oracle{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		phases:         cfg.Phases,
	}
}

// Run starts the oracle main loop, committing roots at each target epoch.
func (o *Oracle) Run(ctx context.Context, syncer *ethsync.EventSyncer, beaconClient *ethsync.BeaconClient) error {
	spec, err := beaconClient.GetSpec(ctx)
	if err != nil {
		return fmt.Errorf("failed to get beacon spec: %w", err)
	}

	var lastTargetEpoch uint64
	for {
		targetEpoch, err := o.processNextCommit(ctx, syncer, beaconClient, spec, lastTargetEpoch)
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("Oracle stopping")
				return ctx.Err()
			}
			logger.Errorw("Commit failed", "error", err)
			select {
			case <-ctx.Done():
				logger.Info("Oracle stopping")
				return ctx.Err()
			case <-time.After(errorRetryDelay):
			}
			continue
		}
		lastTargetEpoch = targetEpoch
	}
}

func (o *Oracle) processNextCommit(ctx context.Context, syncer *ethsync.EventSyncer, beaconClient *ethsync.BeaconClient, spec *ethsync.Spec, lastTargetEpoch uint64) (uint64, error) {
	checkpoint, err := beaconClient.GetFinalizedCheckpoint(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get checkpoint: %w", err)
	}

	targetEpoch := NextTargetEpoch(o.phases, checkpoint.Epoch)

	// Already committed for this target epoch. Wait for at least one more epoch
	// to finalize before recalculating the next target.
	if targetEpoch <= lastTargetEpoch && lastTargetEpoch > 0 {
		checkpoint, err = o.waitForFinalization(ctx, beaconClient, spec, lastTargetEpoch+1)
		if err != nil {
			return 0, err
		}
		targetEpoch = NextTargetEpoch(o.phases, checkpoint.Epoch)
	}

	phase := GetPhaseForEpoch(o.phases, targetEpoch)
	round := RoundInPhase(phase, targetEpoch)

	log := logger.With("targetEpoch", targetEpoch, "round", round)
	log.Info("Processing round")

	checkpoint, err = o.waitForFinalization(ctx, beaconClient, spec, targetEpoch)
	if err != nil {
		return 0, err
	}

	elapsed := time.Since(spec.GenesisTime)
	if elapsed < 0 {
		elapsed = 0
	}
	currentSlot := uint64(elapsed / spec.SlotDuration)
	currentEpoch := currentSlot / spec.SlotsPerEpoch

	log.Infow("Epoch finalized",
		"currentEpoch", currentEpoch,
		"checkpointEpoch", checkpoint.Epoch,
		"checkpointBlock", checkpoint.BlockNum)

	if err := syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return 0, fmt.Errorf("failed to sync to block %d: %w", checkpoint.BlockNum, err)
	}

	clusterBalances, err := o.fetchClusterBalances(ctx, beaconClient)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch balances: %w", err)
	}

	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range clusterBalances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}

	merkleRoot := merkle.BuildMerkleTree(clusterMap)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", merkleRoot),
		"clusters", len(clusterBalances))

	// Store commit as pending before sending tx (allows updater to find it when event arrives)
	if err := o.storage.InsertPendingCommit(ctx, round, targetEpoch, merkleRoot[:], checkpoint.BlockNum, clusterBalances); err != nil {
		return 0, fmt.Errorf("failed to store pending commit: %w", err)
	}

	// TxManager handles gas estimation, bumping, retries, and cancellation
	receipt, err := o.contractClient.CommitRoot(ctx, merkleRoot, checkpoint.BlockNum, round, targetEpoch)
	if err != nil {
		var txHash []byte
		if receipt != nil {
			txHash = receipt.TxHash.Bytes()
		}
		if statusErr := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusFailed, txHash); statusErr != nil {
			log.Warnw("Failed to update commit status", "error", statusErr)
		}

		// Classify error to determine if we should retry or skip to next epoch
		if revertErr, ok := txmanager.IsRevertError(err); ok {
			// Contract rejected the commit (e.g., stale block, already committed).
			// Don't retry - move to next target epoch.
			log.Errorw("Commit reverted, skipping to next epoch",
				"reason", revertErr.Reason,
				"simulated", revertErr.Simulated)
			return targetEpoch, nil
		}

		// Transient error - will be retried by main loop
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	if err := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusConfirmed, receipt.TxHash.Bytes()); err != nil {
		log.Warnw("Failed to update commit status", "error", err)
	}

	log.Infow("Committed", "txHash", receipt.TxHash.Hex())
	return targetEpoch, nil
}

// waitForFinalization blocks until targetEpoch is finalized on the beacon chain.
//
// Finalization semantics: A finalized checkpoint at epoch N contains state at the
// START of epoch N, which reflects the END of epoch N-1. Therefore, to confirm
// that epoch N is fully finalized, we need checkpoint.Epoch > N (i.e., checkpoint
// at epoch N+1 or later).
//
// Returns the checkpoint when checkpoint.Epoch > targetEpoch.
func (o *Oracle) waitForFinalization(ctx context.Context, beaconClient *ethsync.BeaconClient, spec *ethsync.Spec, targetEpoch uint64) (*ethsync.FinalizedCheckpoint, error) {
	const maxRetries = 10

	var lastLoggedCheckpoint uint64
	var retries int

	for {
		// Guard against clock before genesis (would overflow uint64)
		elapsed := time.Since(spec.GenesisTime)
		if elapsed < 0 {
			elapsed = 0
		}
		currentSlot := uint64(elapsed / spec.SlotDuration)
		currentEpoch := currentSlot / spec.SlotsPerEpoch

		checkpoint, err := beaconClient.GetFinalizedCheckpoint(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			retries++
			if retries > maxRetries {
				return nil, fmt.Errorf("checkpoint fetch failed after %d attempts: %w", retries, err)
			}
			// Exponential backoff: slotDuration * 2^retries, capped at 16x
			backoff := spec.SlotDuration * time.Duration(1<<min(retries, 4))
			logger.Warnw("Checkpoint fetch failed",
				"attempt", retries,
				"backoff", backoff.Round(time.Second),
				"error", err)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		retries = 0

		// Target epoch is finalized when checkpoint > targetEpoch
		// (checkpoint at N+1 means epoch N is complete)
		if targetEpoch < checkpoint.Epoch {
			logger.Infow("Epoch finalized",
				"targetEpoch", targetEpoch,
				"checkpoint", checkpoint.Epoch)
			return checkpoint, nil
		}

		// Log progress once per checkpoint change
		if checkpoint.Epoch != lastLoggedCheckpoint {
			targetCheckpoint := targetEpoch + 1
			logger.Infow("Waiting for finalization",
				"checkpoint", fmt.Sprintf("%d/%d", checkpoint.Epoch, targetCheckpoint),
				"epoch", fmt.Sprintf("%d (head=%d)", targetEpoch, currentEpoch))
			lastLoggedCheckpoint = checkpoint.Epoch
		}

		// Calculate wait time
		// We need checkpoint >= targetEpoch + 1, currently at checkpoint.Epoch
		epochsUntilFinalization := (targetEpoch + 1) - checkpoint.Epoch
		var waitTime time.Duration

		if epochsUntilFinalization > 1 {
			// Sleep until 1 epoch before expected finalization
			epochsToSleep := epochsUntilFinalization - 1
			waitTime = time.Duration(epochsToSleep) * time.Duration(spec.SlotsPerEpoch) * spec.SlotDuration
			logger.Debugw("Target epoch far ahead, sleeping",
				"epochsToSleep", epochsToSleep,
				"sleepDuration", waitTime.Round(time.Second))
		} else {
			// Close to finalization, poll each slot
			nextSlotTime := spec.GenesisTime.Add(time.Duration(currentSlot+1) * spec.SlotDuration)
			waitTime = time.Until(nextSlotTime)
		}

		// Guard against negative wait time
		if waitTime < 0 {
			waitTime = spec.SlotDuration
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitTime):
		}
	}
}

func (o *Oracle) fetchClusterBalances(ctx context.Context, beaconClient *ethsync.BeaconClient) ([]ethsync.ClusterBalance, error) {
	validators, err := o.storage.GetActiveValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(validators) == 0 {
		logger.Info("No active validators")
		return nil, nil
	}

	// Deduplicate pubkeys
	pubkeySet := make(map[phase0.BLSPubKey]struct{})
	var pubkeys [][]byte
	for _, v := range validators {
		var pk phase0.BLSPubKey
		copy(pk[:], v.ValidatorPubkey)
		if _, exists := pubkeySet[pk]; !exists {
			pubkeySet[pk] = struct{}{}
			pubkeys = append(pubkeys, v.ValidatorPubkey)
		}
	}

	start := time.Now()
	balanceMap, err := beaconClient.GetFinalizedValidatorBalances(ctx, pubkeys)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch validator balances: %w", err)
	}
	beaconDuration := time.Since(start)

	// Aggregate by cluster ID
	clusterTotals := make(map[[32]byte]uint64)
	var notOnBeacon int
	for _, v := range validators {
		var pk phase0.BLSPubKey
		copy(pk[:], v.ValidatorPubkey)
		balance, onBeacon := balanceMap[pk]
		if !onBeacon {
			notOnBeacon++
			continue
		}
		var clusterID [32]byte
		copy(clusterID[:], v.ClusterID)
		clusterTotals[clusterID] += balance
	}
	var result []ethsync.ClusterBalance
	for clusterID, total := range clusterTotals {
		result = append(result, ethsync.ClusterBalance{
			ClusterID:        clusterID[:],
			EffectiveBalance: total,
		})
	}

	logger.Infow("Balances fetched",
		"validators", len(validators),
		"fromBeacon", len(balanceMap),
		"notOnBeacon", notOnBeacon,
		"clusters", len(result),
		"took", beaconDuration.Round(time.Millisecond))

	return result, nil
}
