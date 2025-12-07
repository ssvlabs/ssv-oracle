package oracle

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
)

// Oracle coordinates the cluster balance tracking and Merkle root commitments.
type Oracle struct {
	storage        ethsync.Storage
	contractClient *contract.Client
	timingPhases   []TimingPhase // Timing config from YAML
}

// Config holds the oracle configuration.
type Config struct {
	Storage        ethsync.Storage
	ContractClient *contract.Client
	TimingPhases   []TimingPhase
}

// New creates a new Oracle instance.
func New(cfg *Config) *Oracle {
	return &Oracle{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		timingPhases:   cfg.TimingPhases,
	}
}

// Run starts the oracle loop, processing rounds continuously.
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

	// Log timing config (from YAML)
	firstPhase := o.timingPhases[0]
	logger.Infow("Oracle timing configured",
		"phases", len(o.timingPhases),
		"firstStartEpoch", firstPhase.StartEpoch,
		"firstInterval", firstPhase.Interval)

	// Main loop: process target epochs as they become finalized
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

// processNextCommit waits for the next target epoch to be finalized and commits it.
// Returns the targetEpoch that was committed.
func (o *Oracle) processNextCommit(ctx context.Context, syncer *ethsync.EventSyncer, beaconClient *ethsync.BeaconClient, spec *ethsync.Spec, lastTargetEpoch uint64) (uint64, error) {
	// Get current finalized checkpoint to calculate next target
	checkpoint, err := beaconClient.GetFinalizedCheckpoint(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get checkpoint: %w", err)
	}

	// Calculate next target epoch (handles phase transitions)
	targetEpoch := NextTargetEpoch(o.timingPhases, checkpoint.Epoch)

	// Skip if we already committed this target (e.g., on retry after partial failure)
	if targetEpoch <= lastTargetEpoch && lastTargetEpoch > 0 {
		// Wait for finalized epoch to advance
		_, err := o.waitForFinalization(ctx, beaconClient, spec, lastTargetEpoch+1)
		if err != nil {
			return 0, err
		}
		// Recalculate
		checkpoint, err = beaconClient.GetFinalizedCheckpoint(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get checkpoint: %w", err)
		}
		targetEpoch = NextTargetEpoch(o.timingPhases, checkpoint.Epoch)
	}

	phase := GetTimingForEpoch(o.timingPhases, targetEpoch)
	round := RoundInPhase(phase, targetEpoch)

	// Create child logger with round context
	log := logger.With("targetEpoch", targetEpoch, "round", round)

	log.Info("Processing round")

	// Step 1: Wait for target epoch to be finalized
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

	// Step 2: Sync events to finalized block
	if err := syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return 0, fmt.Errorf("failed to sync to block %d: %w", checkpoint.BlockNum, err)
	}

	// Step 3: Fetch and store validator balances from finalized state
	if err := o.fetchAndStoreBalances(ctx, log, beaconClient, targetEpoch, spec.SlotsPerEpoch); err != nil {
		return 0, fmt.Errorf("failed to fetch balances: %w", err)
	}

	// Step 4: Build merkle tree
	clusterBalances, err := o.storage.GetClusterBalances(ctx, targetEpoch, spec.SlotsPerEpoch)
	if err != nil {
		return 0, fmt.Errorf("failed to get cluster balances: %w", err)
	}

	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range clusterBalances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.TotalEffectiveBalance
	}

	merkleRoot := merkle.BuildMerkleTree(clusterMap)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", merkleRoot[:8]),
		"clusters", len(clusterBalances))

	// Step 5: Commit to contract (round is for storage reference only)
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

	var txHashBytes []byte
	var txHashStr string
	if tx != nil {
		txHashBytes = tx.Hash().Bytes()
		txHashStr = tx.Hash().Hex()
	} else {
		// Mock mode: generate deterministic fake tx hash from reference block
		mockHash := common.BytesToHash([]byte(fmt.Sprintf("mock-tx-block-%d", checkpoint.BlockNum)))
		txHashBytes = mockHash.Bytes()
		txHashStr = mockHash.Hex()
	}
	if err := o.storage.InsertOracleCommit(ctx, round, targetEpoch, merkleRoot[:], checkpoint.BlockNum, txHashBytes); err != nil {
		log.Warnw("Failed to store commit", "error", err)
	}

	log.Infow("Committed", "txHash", txHashStr)

	return targetEpoch, nil
}

// waitForFinalization waits until targetEpoch is fully finalized.
// Polls at slot boundaries, with coarse waiting when target is far ahead.
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
		checkpointRetries = 0 // Reset on success

		// Finalized when checkpoint.Epoch > targetEpoch
		if targetEpoch < checkpoint.Epoch {
			logger.Infow("Finalization detected",
				"slot", currentSlot,
				"epoch", currentEpoch,
				"slotInEpoch", fmt.Sprintf("%d/%d", slotInEpoch, spec.SlotsPerEpoch))
			return checkpoint, nil
		}

		// Wait based on distance to target
		epochsAhead := int64(targetEpoch) - int64(checkpoint.Epoch)
		if epochsAhead > 1 {
			// Far from target: coarse wait
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
			// Close to target: poll at slot boundaries
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

// fetchAndStoreBalances fetches effective balances from finalized beacon state for all active validators.
// Stores balances with targetEpoch. Only stores balances that changed since the previous epoch.
func (o *Oracle) fetchAndStoreBalances(ctx context.Context, log *zap.SugaredLogger, beaconClient *ethsync.BeaconClient, targetEpoch uint64, slotsPerEpoch uint64) error {
	validators, err := o.storage.GetActiveValidatorsWithClusters(ctx, targetEpoch, slotsPerEpoch)
	if err != nil {
		return fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(validators) == 0 {
		log.Info("No active validators")
		return nil
	}

	// Deduplicate validator pubkeys for beacon query
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
		return fmt.Errorf("failed to fetch validator balances: %w", err)
	}

	prevBalances, err := o.storage.GetLatestValidatorBalances(ctx, validators, targetEpoch)
	if err != nil {
		return fmt.Errorf("failed to get previous balances: %w", err)
	}

	// Process balances and store only changed values
	stored := 0
	skipped := 0
	notOnBeacon := 0
	insertErrors := 0

	for _, v := range validators {
		pubkeyHex := fmt.Sprintf("0x%x", v.ValidatorPubkey)
		newBalance, onBeacon := balanceMap[pubkeyHex]

		key := fmt.Sprintf("%x:%x", v.ClusterID, v.ValidatorPubkey)
		prevBalance, hasPrev := prevBalances[key]

		if !onBeacon {
			notOnBeacon++
			if !hasPrev {
				// Validator registered to SSV but never deposited to beacon - skip (implicit 0)
				skipped++
				continue
			}
			// Previously had balance but now gone from beacon (exited/withdrawn) - record as 0
			newBalance = 0
		}

		if hasPrev && prevBalance == newBalance {
			skipped++
			continue
		}

		balance := &ethsync.ValidatorBalance{
			ClusterID:        v.ClusterID,
			ValidatorPubkey:  v.ValidatorPubkey,
			Epoch:            targetEpoch,
			EffectiveBalance: newBalance,
		}

		if err := o.storage.InsertValidatorBalance(ctx, balance); err != nil {
			logger.Warnw("Failed to insert balance",
				"validator", pubkeyHex,
				"error", err)
			insertErrors++
		} else {
			stored++
		}
	}

	log.Infow("Balances processed",
		"fromBeacon", len(balanceMap),
		"total", len(validators),
		"changed", stored,
		"notDeposited", notOnBeacon)

	if insertErrors > 0 {
		log.Warnw("Balance insert errors", "count", insertErrors)
	}

	return nil
}
