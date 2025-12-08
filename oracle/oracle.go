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
			time.Sleep(errorRetryDelay)
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

	if targetEpoch <= lastTargetEpoch && lastTargetEpoch > 0 {
		_, err := o.waitForFinalization(ctx, beaconClient, spec, lastTargetEpoch+1)
		if err != nil {
			return 0, err
		}
		checkpoint, err = beaconClient.GetFinalizedCheckpoint(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get checkpoint: %w", err)
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

	currentSlot := uint64(time.Since(spec.GenesisTime) / spec.SlotDuration)
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
		_ = o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusFailed, txHash)
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	if err := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusConfirmed, receipt.TxHash.Bytes()); err != nil {
		log.Warnw("Failed to update commit status", "error", err)
	}

	log.Infow("Committed", "txHash", receipt.TxHash.Hex())
	return targetEpoch, nil
}

func (o *Oracle) waitForFinalization(ctx context.Context, beaconClient *ethsync.BeaconClient, spec *ethsync.Spec, targetEpoch uint64) (*ethsync.FinalizedCheckpoint, error) {
	var lastLoggedCheckpoint uint64
	var lastLoggedSlot uint64
	var checkpointRetries int

	for {
		now := time.Now()
		currentSlot := uint64(now.Sub(spec.GenesisTime) / spec.SlotDuration)
		currentEpoch := currentSlot / spec.SlotsPerEpoch
		slotInEpoch := (currentSlot % spec.SlotsPerEpoch) + 1

		checkpoint, err := beaconClient.GetFinalizedCheckpoint(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			checkpointRetries++
			logger.Warnw("Failed to get checkpoint, retrying",
				"attempt", checkpointRetries,
				"error", err)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(spec.SlotDuration):
			}
			continue
		}
		checkpointRetries = 0

		if targetEpoch < checkpoint.Epoch {
			logger.Infow("Finalization detected",
				"slot", currentSlot,
				"epoch", currentEpoch,
				"slotInEpoch", fmt.Sprintf("%d/%d", slotInEpoch, spec.SlotsPerEpoch))
			return checkpoint, nil
		}

		epochsAhead := int64(targetEpoch) - int64(checkpoint.Epoch)
		if epochsAhead > 1 {
			if checkpoint.Epoch != lastLoggedCheckpoint {
				logger.Infow("Waiting for finalization",
					"slot", currentSlot,
					"epoch", currentEpoch,
					"slotInEpoch", fmt.Sprintf("%d/%d", slotInEpoch, spec.SlotsPerEpoch),
					"targetEpoch", targetEpoch,
					"checkpoint", checkpoint.Epoch)
				lastLoggedCheckpoint = checkpoint.Epoch
				lastLoggedSlot = currentSlot
			}
			waitEpochs := epochsAhead - 1
			waitTime := time.Duration(uint64(waitEpochs)*spec.SlotsPerEpoch) * spec.SlotDuration
			logger.Infow("Target ahead, sleeping",
				"epochsAhead", epochsAhead,
				"waitTime", waitTime.Round(time.Second))

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime):
			}
		} else {
			if checkpoint.Epoch != lastLoggedCheckpoint {
				logger.Infow("Waiting for finalization",
					"slot", currentSlot,
					"epoch", currentEpoch,
					"slotInEpoch", fmt.Sprintf("%d/%d", slotInEpoch, spec.SlotsPerEpoch),
					"targetEpoch", targetEpoch,
					"checkpoint", checkpoint.Epoch)
				lastLoggedCheckpoint = checkpoint.Epoch
				lastLoggedSlot = currentSlot
			} else if currentSlot != lastLoggedSlot {
				logger.Debugw("Slot tick",
					"slot", currentSlot,
					"epoch", currentEpoch,
					"slotInEpoch", fmt.Sprintf("%d/%d", slotInEpoch, spec.SlotsPerEpoch))
				lastLoggedSlot = currentSlot
			}

			nextSlotTime := spec.GenesisTime.Add(time.Duration(currentSlot+1) * spec.SlotDuration)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Until(nextSlotTime)):
			}
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

	balanceMap, err := beaconClient.GetFinalizedValidatorBalances(ctx, pubkeys)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch validator balances: %w", err)
	}

	// Aggregate by cluster ID
	clusterTotals := make(map[[32]byte]uint64)
	for _, v := range validators {
		var pk phase0.BLSPubKey
		copy(pk[:], v.ValidatorPubkey)
		balance, onBeacon := balanceMap[pk]
		if !onBeacon {
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
		"clusters", len(result))

	return result, nil
}
