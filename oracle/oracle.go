package oracle

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
)

type Oracle struct {
	storage        ethsync.Storage
	contractClient *contract.Client
	timingPhases   []TimingPhase
}

type Config struct {
	Storage        ethsync.Storage
	ContractClient *contract.Client
	TimingPhases   []TimingPhase
}

func New(cfg *Config) *Oracle {
	return &Oracle{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		timingPhases:   cfg.TimingPhases,
	}
}

func (o *Oracle) Run(ctx context.Context, syncer *ethsync.EventSyncer, beaconClient *ethsync.BeaconClient) error {
	logger.Info("Oracle starting")

	spec, err := beaconClient.GetSpec(ctx)
	if err != nil {
		return fmt.Errorf("failed to get beacon spec: %w", err)
	}
	logger.Infow("Beacon spec loaded",
		"genesis", spec.GenesisTime.Format(time.RFC3339),
		"slotsPerEpoch", spec.SlotsPerEpoch,
		"slotDuration", spec.SlotDuration)

	firstPhase := o.timingPhases[0]
	logger.Infow("Oracle timing configured",
		"phases", len(o.timingPhases),
		"firstStartEpoch", firstPhase.StartEpoch,
		"firstInterval", firstPhase.Interval)

	var lastTargetEpoch uint64
	for {
		targetEpoch, err := o.processNextCommit(ctx, syncer, beaconClient, spec, lastTargetEpoch)
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("Oracle stopping")
				return ctx.Err()
			}
			logger.Errorw("Commit failed", "error", err)
			time.Sleep(10 * time.Second)
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

	targetEpoch := NextTargetEpoch(o.timingPhases, checkpoint.Epoch)

	if targetEpoch <= lastTargetEpoch && lastTargetEpoch > 0 {
		_, err := o.waitForFinalization(ctx, beaconClient, spec, lastTargetEpoch+1)
		if err != nil {
			return 0, err
		}
		checkpoint, err = beaconClient.GetFinalizedCheckpoint(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get checkpoint: %w", err)
		}
		targetEpoch = NextTargetEpoch(o.timingPhases, checkpoint.Epoch)
	}

	phase := GetTimingForEpoch(o.timingPhases, targetEpoch)
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

	clusterBalances, err := o.fetchClusterBalances(ctx, log, beaconClient)
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
		"root", fmt.Sprintf("0x%x", merkleRoot[:8]),
		"clusters", len(clusterBalances))

	tx, err := o.contractClient.CommitRoot(ctx, merkleRoot, checkpoint.BlockNum, round, targetEpoch)
	if err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	receipt, err := o.contractClient.WaitForReceipt(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("failed waiting for receipt: %w", err)
	}

	if receipt.Status != 1 {
		return 0, fmt.Errorf("transaction reverted")
	}

	if err := o.storage.InsertOracleCommit(ctx, round, targetEpoch, merkleRoot[:], checkpoint.BlockNum, tx.Hash().Bytes(), clusterBalances); err != nil {
		log.Warnw("Failed to store commit", "error", err)
	}

	log.Infow("Committed", "txHash", tx.Hash().Hex())
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
			checkpointRetries++
			logger.Warnw("Failed to get checkpoint, retrying",
				"attempt", checkpointRetries,
				"error", err)
			time.Sleep(spec.SlotDuration)
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

// fetchClusterBalances fetches validator balances from beacon and aggregates by cluster.
func (o *Oracle) fetchClusterBalances(ctx context.Context, log *zap.SugaredLogger, beaconClient *ethsync.BeaconClient) ([]ethsync.ClusterBalance, error) {
	validators, err := o.storage.GetActiveValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(validators) == 0 {
		log.Info("No active validators")
		return nil, nil
	}

	pubkeySet := make(map[string]struct{})
	var pubkeys [][]byte
	for _, v := range validators {
		pubkeyHex := fmt.Sprintf("0x%x", v.ValidatorPubkey)
		if _, exists := pubkeySet[pubkeyHex]; !exists {
			pubkeySet[pubkeyHex] = struct{}{}
			pubkeys = append(pubkeys, v.ValidatorPubkey)
		}
	}

	balanceMap, err := beaconClient.GetFinalizedValidatorBalances(ctx, pubkeys)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch validator balances: %w", err)
	}

	clusterTotals := make(map[string]uint64)
	for _, v := range validators {
		pubkeyHex := fmt.Sprintf("0x%x", v.ValidatorPubkey)
		balance, onBeacon := balanceMap[pubkeyHex]
		if !onBeacon {
			continue
		}
		clusterKey := fmt.Sprintf("%x", v.ClusterID)
		clusterTotals[clusterKey] += balance
	}

	var result []ethsync.ClusterBalance
	for clusterKey, total := range clusterTotals {
		var clusterID []byte
		fmt.Sscanf(clusterKey, "%x", &clusterID)
		result = append(result, ethsync.ClusterBalance{
			ClusterID:        clusterID,
			EffectiveBalance: total,
		})
	}

	log.Infow("Balances fetched",
		"validators", len(validators),
		"fromBeacon", len(balanceMap),
		"clusters", len(result))

	return result, nil
}
