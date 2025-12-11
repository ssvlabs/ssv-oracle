package oracle

import (
	"context"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
	"ssv-oracle/txmanager"
)

const (
	errorRetryDelay  = 10 * time.Second
	balanceFloorGwei = 32_000_000_000 // Floor for low/missing validator balances (32 ETH)
)

// Config holds Oracle configuration.
type Config struct {
	Storage        *ethsync.Storage
	ContractClient *contract.Client
	Syncer         *ethsync.EventSyncer
	BeaconClient   *ethsync.BeaconClient
	Phases         []CommitPhase
}

// Oracle commits merkle roots of cluster effective balances to the SSV contract.
type Oracle struct {
	storage        storage
	contractClient *contract.Client
	syncer         *ethsync.EventSyncer
	beaconClient   *ethsync.BeaconClient
	phases         []CommitPhase
}

type storage interface {
	GetActiveValidators(ctx context.Context) ([]ethsync.ActiveValidator, error)
	InsertPendingCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []ethsync.ClusterBalance) error
	UpdateCommitStatus(ctx context.Context, roundID uint64, status ethsync.CommitStatus, txHash []byte) error
}

// New creates a new Oracle instance.
func New(cfg *Config) *Oracle {
	return &Oracle{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		syncer:         cfg.Syncer,
		beaconClient:   cfg.BeaconClient,
		phases:         cfg.Phases,
	}
}

// Run starts the oracle main loop, committing roots at each target epoch.
func (o *Oracle) Run(ctx context.Context) error {
	var lastTargetEpoch uint64
	for {
		targetEpoch, err := o.processNextCommit(ctx, lastTargetEpoch)
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

func (o *Oracle) processNextCommit(ctx context.Context, lastTargetEpoch uint64) (uint64, error) {
	checkpoint, err := o.beaconClient.GetFinalizedCheckpoint(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get checkpoint: %w", err)
	}

	targetEpoch := NextTargetEpoch(o.phases, checkpoint.Epoch)

	// Already committed for this target epoch - wait for next finalization
	if targetEpoch <= lastTargetEpoch && lastTargetEpoch > 0 {
		checkpoint, err = o.waitForFinalization(ctx, lastTargetEpoch+1)
		if err != nil {
			return 0, err
		}
		targetEpoch = NextTargetEpoch(o.phases, checkpoint.Epoch)
	}

	phase := GetPhaseForEpoch(o.phases, targetEpoch)
	round := RoundInPhase(phase, targetEpoch)

	log := logger.With("targetEpoch", targetEpoch, "round", round)
	log.Info("Processing round")

	checkpoint, err = o.waitForFinalization(ctx, targetEpoch)
	if err != nil {
		return 0, err
	}

	currentEpoch := o.beaconClient.CurrentEpoch()
	log.Infow("Epoch finalized",
		"currentEpoch", currentEpoch,
		"checkpointEpoch", checkpoint.Epoch,
		"checkpointBlock", checkpoint.BlockNum)

	if err := o.syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return 0, fmt.Errorf("failed to sync to block %d: %w", checkpoint.BlockNum, err)
	}

	clusterBalances, err := o.fetchClusterBalances(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch balances: %w", err)
	}

	merkleRoot := o.buildMerkleRoot(clusterBalances)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", merkleRoot),
		"clusters", len(clusterBalances))

	if err := o.storage.InsertPendingCommit(ctx, round, targetEpoch, merkleRoot[:], checkpoint.BlockNum, clusterBalances); err != nil {
		return 0, fmt.Errorf("failed to store pending commit: %w", err)
	}

	receipt, err := o.contractClient.CommitRoot(ctx, merkleRoot, checkpoint.BlockNum, round, targetEpoch)
	if err != nil {
		return o.handleCommitError(ctx, log, round, targetEpoch, receipt, err)
	}

	if err := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusConfirmed, receipt.TxHash.Bytes()); err != nil {
		log.Warnw("Failed to update commit status", "error", err)
	}

	log.Infow("Committed", "txHash", receipt.TxHash.Hex())
	return targetEpoch, nil
}

func (o *Oracle) buildMerkleRoot(balances []ethsync.ClusterBalance) [32]byte {
	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range balances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}
	return merkle.BuildMerkleTree(clusterMap)
}

func (o *Oracle) fetchClusterBalances(ctx context.Context) ([]ethsync.ClusterBalance, error) {
	validators, err := o.storage.GetActiveValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(validators) == 0 {
		logger.Info("No active validators")
		return nil, nil
	}

	pubkeys := o.deduplicatePubkeys(validators)

	start := time.Now()
	balanceMap, err := o.beaconClient.GetFinalizedValidatorBalances(ctx, pubkeys)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch validator balances: %w", err)
	}

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	logger.Infow("Balances fetched",
		"validators", len(validators),
		"fromBeacon", len(balanceMap),
		"notOnBeacon", notOnBeacon,
		"clusters", len(result),
		"took", time.Since(start).Round(time.Millisecond))

	return result, nil
}

func (o *Oracle) deduplicatePubkeys(validators []ethsync.ActiveValidator) [][]byte {
	seen := make(map[phase0.BLSPubKey]struct{})
	var pubkeys [][]byte
	for _, v := range validators {
		var pk phase0.BLSPubKey
		copy(pk[:], v.ValidatorPubkey)
		if _, exists := seen[pk]; !exists {
			seen[pk] = struct{}{}
			pubkeys = append(pubkeys, v.ValidatorPubkey)
		}
	}
	return pubkeys
}

func (o *Oracle) aggregateByCluster(validators []ethsync.ActiveValidator, balanceMap map[phase0.BLSPubKey]uint64) ([]ethsync.ClusterBalance, int) {
	clusterTotals := make(map[[32]byte]uint64)
	var notOnBeacon int

	for _, v := range validators {
		var pk phase0.BLSPubKey
		copy(pk[:], v.ValidatorPubkey)

		balance, onBeacon := balanceMap[pk]
		if !onBeacon {
			notOnBeacon++
		}
		if balance < balanceFloorGwei {
			balance = balanceFloorGwei
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

	return result, notOnBeacon
}

func (o *Oracle) handleCommitError(
	ctx context.Context,
	log logger.Logger,
	round, targetEpoch uint64,
	receipt *types.Receipt,
	err error,
) (uint64, error) {
	var txHash []byte
	if receipt != nil {
		txHash = receipt.TxHash.Bytes()
	}
	if statusErr := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusFailed, txHash); statusErr != nil {
		log.Warnw("Failed to update commit status", "error", statusErr)
	}

	if revertErr, ok := txmanager.IsRevertError(err); ok {
		log.Errorw("Commit reverted, skipping to next epoch",
			"reason", revertErr.Reason,
			"simulated", revertErr.Simulated)
		return targetEpoch, nil
	}

	return 0, fmt.Errorf("failed to commit: %w", err)
}

// waitForFinalization blocks until targetEpoch is finalized.
// Returns when checkpoint.Epoch > targetEpoch.
func (o *Oracle) waitForFinalization(ctx context.Context, targetEpoch uint64) (*ethsync.FinalizedCheckpoint, error) {
	const maxRetries = 10

	var lastLoggedCheckpoint uint64
	var retries int

	for {
		currentEpoch := o.beaconClient.CurrentEpoch()

		checkpoint, err := o.beaconClient.GetFinalizedCheckpoint(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			retries++
			if retries > maxRetries {
				return nil, fmt.Errorf("checkpoint fetch failed after %d attempts: %w", retries, err)
			}
			// Exponential backoff: 1, 2, 4, 8 slots (12s, 24s, 48s, 96s at 12s/slot)
			backoff := o.beaconClient.Spec.SlotDuration * time.Duration(1<<min(retries-1, 3))
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

		if targetEpoch < checkpoint.Epoch {
			return checkpoint, nil
		}

		if checkpoint.Epoch != lastLoggedCheckpoint {
			targetCheckpoint := targetEpoch + 1
			logger.Infow("Waiting for finalization",
				"checkpoint", fmt.Sprintf("current=%d target=%d", checkpoint.Epoch, targetCheckpoint),
				"epoch", fmt.Sprintf("head=%d finalized=%d target=%d", currentEpoch, checkpoint.Epoch-1, targetEpoch))
			lastLoggedCheckpoint = checkpoint.Epoch
		}

		waitTime := o.calculateWaitTime(checkpoint.Epoch, targetEpoch, currentEpoch)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitTime):
			logger.Debugw("Slot tick",
				"slot", o.beaconClient.Spec.CurrentSlot(),
				"slotInEpoch", fmt.Sprintf("%d/%d", o.beaconClient.Spec.SlotInEpoch(), o.beaconClient.Spec.SlotsPerEpoch),
				"epoch", o.beaconClient.CurrentEpoch())
		}
	}
}

func (o *Oracle) calculateWaitTime(checkpointEpoch, targetEpoch, currentEpoch uint64) time.Duration {
	epochsUntilFinalization := (targetEpoch + 1) - checkpointEpoch

	if epochsUntilFinalization > 1 {
		epochsToSleep := epochsUntilFinalization - 1
		waitTime := time.Duration(epochsToSleep) * time.Duration(o.beaconClient.Spec.SlotsPerEpoch) * o.beaconClient.Spec.SlotDuration
		logger.Debugw("Target epoch far ahead, sleeping",
			"epochsToSleep", epochsToSleep,
			"sleepDuration", waitTime.Round(time.Second))
		return waitTime
	}

	elapsed := time.Since(o.beaconClient.Spec.GenesisTime)
	if elapsed < 0 {
		elapsed = 0
	}
	currentSlot := uint64(elapsed / o.beaconClient.Spec.SlotDuration)
	nextSlotTime := o.beaconClient.Spec.GenesisTime.Add(time.Duration(currentSlot+1) * o.beaconClient.Spec.SlotDuration)
	waitTime := time.Until(nextSlotTime)

	if waitTime < 0 {
		return o.beaconClient.Spec.SlotDuration
	}
	return waitTime
}
