package oracle

import (
	"context"
	"fmt"
	"log"
	"time"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
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
	log.Println("Oracle starting...")

	spec, err := beaconClient.GetSpec(ctx)
	if err != nil {
		return fmt.Errorf("failed to get beacon spec: %w", err)
	}
	log.Printf("Beacon spec: genesis=%s, slotsPerEpoch=%d, slotDuration=%v",
		spec.GenesisTime.Format(time.RFC3339), spec.SlotsPerEpoch, spec.SlotDuration)

	// Log timing config (from YAML)
	firstPhase := o.timingPhases[0]
	log.Printf("Oracle timing: %d phases, first phase: startEpoch=%d, interval=%d",
		len(o.timingPhases), firstPhase.StartEpoch, firstPhase.Interval)

	// Main loop: process target epochs as they become finalized
	var lastTargetEpoch uint64
	for {
		targetEpoch, err := o.processNextCommit(ctx, syncer, beaconClient, spec, lastTargetEpoch)
		if err != nil {
			if ctx.Err() != nil {
				log.Println("Oracle stopping...")
				return ctx.Err()
			}
			log.Printf("Commit error: %v", err)
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

	log.Printf("--- Target epoch %d (phase starting %d, round %d) ---", targetEpoch, phase.StartEpoch, round)

	// Step 1: Wait for target epoch to be finalized
	checkpoint, err = o.waitForFinalization(ctx, beaconClient, spec, targetEpoch)
	if err != nil {
		return 0, err
	}

	currentSlot := uint64(time.Since(spec.GenesisTime) / spec.SlotDuration)
	currentEpoch := currentSlot / spec.SlotsPerEpoch

	log.Printf("Epoch %d finalized (current=%d, checkpoint: epoch=%d block=%d)",
		targetEpoch, currentEpoch, checkpoint.Epoch, checkpoint.BlockNum)

	// Step 2: Sync events to finalized block
	if err := syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return 0, fmt.Errorf("failed to sync to block %d: %w", checkpoint.BlockNum, err)
	}

	// Step 3: Fetch and store validator balances from finalized state
	if err := o.fetchAndStoreBalances(ctx, beaconClient, targetEpoch, spec.SlotsPerEpoch); err != nil {
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
	log.Printf("Merkle root: 0x%x (%d clusters)", merkleRoot[:], len(clusterBalances))

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

	txHash := "mock"
	if tx != nil {
		txHash = tx.Hash().Hex()
	}
	log.Printf("Committed target epoch %d (tx: %s)", targetEpoch, txHash)

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
			log.Printf("Warning: failed to get checkpoint (attempt %d): %v, retrying...", checkpointRetries, err)
			time.Sleep(spec.SlotDuration)
			continue
		}
		checkpointRetries = 0 // Reset on success

		// Finalized when checkpoint.Epoch > targetEpoch
		if targetEpoch < checkpoint.Epoch {
			log.Printf("Slot %d (epoch %d, %d/%d) - finalization detected!",
				currentSlot, currentEpoch, slotInEpoch, spec.SlotsPerEpoch)
			return checkpoint, nil
		}

		// Wait based on distance to target
		epochsAhead := int64(targetEpoch) - int64(checkpoint.Epoch)
		if epochsAhead > 1 {
			// Far from target: coarse wait
			if checkpoint.Epoch != lastLoggedCheckpoint {
				log.Printf("Slot %d (epoch %d, %d/%d) - waiting for epoch %d (checkpoint: %d, need > %d)",
					currentSlot, currentEpoch, slotInEpoch, spec.SlotsPerEpoch,
					targetEpoch, checkpoint.Epoch, targetEpoch)
				lastLoggedCheckpoint = checkpoint.Epoch
				lastLoggedSlot = currentSlot
			}
			waitEpochs := epochsAhead - 1
			waitTime := time.Duration(uint64(waitEpochs)*spec.SlotsPerEpoch) * spec.SlotDuration
			log.Printf("Target is %d epochs ahead, waiting %v", epochsAhead, waitTime.Round(time.Second))

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTime):
			}
		} else {
			// Close to target: poll at slot boundaries
			if checkpoint.Epoch != lastLoggedCheckpoint {
				log.Printf("Slot %d (epoch %d, %d/%d) - waiting for epoch %d (checkpoint: %d, need > %d)",
					currentSlot, currentEpoch, slotInEpoch, spec.SlotsPerEpoch,
					targetEpoch, checkpoint.Epoch, targetEpoch)
				lastLoggedCheckpoint = checkpoint.Epoch
				lastLoggedSlot = currentSlot
			} else if currentSlot != lastLoggedSlot {
				log.Printf("Slot %d (epoch %d, %d/%d)", currentSlot, currentEpoch, slotInEpoch, spec.SlotsPerEpoch)
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
func (o *Oracle) fetchAndStoreBalances(ctx context.Context, beaconClient *ethsync.BeaconClient, targetEpoch uint64, slotsPerEpoch uint64) error {
	validators, err := o.storage.GetActiveValidatorsWithClusters(ctx, targetEpoch, slotsPerEpoch)
	if err != nil {
		return fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(validators) == 0 {
		log.Printf("Balances: no active validators")
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
			log.Printf("Warning: failed to insert balance for validator %s: %v", pubkeyHex, err)
			insertErrors++
		} else {
			stored++
		}
	}

	log.Printf("Balances: %d/%d from beacon, %d changed, %d not deposited",
		len(balanceMap), len(validators), stored, notOnBeacon)

	if insertErrors > 0 {
		log.Printf("Warning: %d balance insert errors", insertErrors)
	}

	return nil
}
