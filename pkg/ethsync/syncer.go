package ethsync

import (
	"context"
	"fmt"
	"log"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/schollz/progressbar/v3"
)

// EventSyncer continuously syncs SSV contract events to the database.
type EventSyncer struct {
	client      *ExecutionClient
	storage     Storage
	parser      *EventParser
	ssvContract common.Address
	spec        *Spec
}

// EventSyncerConfig holds configuration for the event syncer.
type EventSyncerConfig struct {
	ExecutionClient *ExecutionClient
	Storage         Storage
	SSVContract     common.Address
	Spec            *Spec // Beacon chain spec for slot/epoch calculations
}

// NewEventSyncer creates a new event syncer.
func NewEventSyncer(cfg EventSyncerConfig) (*EventSyncer, error) {
	parser, err := NewEventParser()
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	if cfg.Spec == nil {
		return nil, fmt.Errorf("Spec is required for epoch calculations")
	}

	return &EventSyncer{
		client:      cfg.ExecutionClient,
		storage:     cfg.Storage,
		parser:      parser,
		ssvContract: cfg.SSVContract,
		spec:        cfg.Spec,
	}, nil
}

// SyncToFinalized performs a one-time sync from last synced block to current finalized block.
// This is used for initial sync before starting the oracle loop.
// If fromBlock is 0, it auto-detects the contract deployment block.
func (s *EventSyncer) SyncToFinalized(ctx context.Context, fromBlock uint64) error {
	// Set initial sync block if not already set
	lastSynced, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last synced block: %w", err)
	}

	if lastSynced == 0 {
		if fromBlock == 0 {
			return fmt.Errorf("sync_from_block must be set to the SSV contract deployment block")
		}

		log.Printf("First run: setting initial sync position to block %d", fromBlock-1)
		err = s.storage.UpdateLastSyncedBlock(ctx, fromBlock-1)
		if err != nil {
			return fmt.Errorf("failed to set initial sync block: %w", err)
		}
	}

	// Sync once to current finalized block
	return s.syncOnce(ctx)
}

// SyncIncremental performs one incremental sync (used in oracle cycle).
func (s *EventSyncer) SyncIncremental(ctx context.Context) error {
	return s.syncOnce(ctx)
}

// SyncToBlock syncs events from last synced block to a specific target block.
// This is used by the oracle to ensure consistency between events and balances at a specific epoch.
func (s *EventSyncer) SyncToBlock(ctx context.Context, targetBlock uint64) error {
	// Get last synced block
	fromBlock, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last synced block: %w", err)
	}

	// Already synced past target?
	if fromBlock >= targetBlock {
		return nil
	}

	totalBlocks := int(targetBlock - fromBlock)

	// Create progress bar
	bar := progressbar.NewOptions(totalBlocks,
		progressbar.OptionSetDescription("Syncing events"),
		progressbar.OptionSetWidth(40),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	// Fetch and process logs
	totalEvents := 0
	err = s.client.FetchLogs(ctx, s.ssvContract, fromBlock+1, targetBlock, func(batchEnd uint64, logs []BlockLogs) error {
		// Process each block's logs
		for _, blockLogs := range logs {
			if err := s.processBlockLogs(ctx, blockLogs); err != nil {
				return fmt.Errorf("failed to process block %d: %w", blockLogs.BlockNumber, err)
			}
			totalEvents += len(blockLogs.Logs)
		}

		// Update progress bar
		_ = bar.Set(int(batchEnd - fromBlock))

		// Advance sync progress to batch end.
		// This is needed for batches with no events (or sparse events) to ensure
		// we don't re-scan empty block ranges on restart.
		// Note: Per-block tx already updates progress for blocks WITH events.
		if err := s.storage.UpdateLastSyncedBlock(ctx, batchEnd); err != nil {
			return fmt.Errorf("failed to update sync progress: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to fetch logs: %w", err)
	}

	_ = bar.Finish()
	fmt.Println() // New line after progress bar
	log.Printf("Events: synced to block %d (%d new)", targetBlock, totalEvents)
	return nil
}

// syncOnce performs a single sync iteration.
func (s *EventSyncer) syncOnce(ctx context.Context) error {
	// Get last synced block
	fromBlock, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last synced block: %w", err)
	}

	// Get finalized block
	finalizedBlock, err := s.client.GetFinalizedBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get finalized block: %w", err)
	}

	// Nothing to sync?
	if fromBlock >= finalizedBlock {
		log.Printf("Events: already synced to block %d", fromBlock)
		return nil
	}

	totalBlocks := int(finalizedBlock - fromBlock)

	// Create progress bar
	bar := progressbar.NewOptions(totalBlocks,
		progressbar.OptionSetDescription("Syncing events"),
		progressbar.OptionSetWidth(40),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)

	// Fetch and process logs
	totalEvents := 0
	err = s.client.FetchLogs(ctx, s.ssvContract, fromBlock+1, finalizedBlock, func(batchEnd uint64, logs []BlockLogs) error {
		// Process each block's logs
		for _, blockLogs := range logs {
			if err := s.processBlockLogs(ctx, blockLogs); err != nil {
				return fmt.Errorf("failed to process block %d: %w", blockLogs.BlockNumber, err)
			}
			totalEvents += len(blockLogs.Logs)
		}

		// Update progress bar
		_ = bar.Set(int(batchEnd - fromBlock))

		// Advance sync progress to batch end.
		// This is needed for batches with no events (or sparse events) to ensure
		// we don't re-scan empty block ranges on restart.
		// Note: Per-block tx already updates progress for blocks WITH events.
		if err := s.storage.UpdateLastSyncedBlock(ctx, batchEnd); err != nil {
			return fmt.Errorf("failed to update sync progress: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to fetch logs: %w", err)
	}

	_ = bar.Finish()
	fmt.Println() // New line after progress bar
	log.Printf("Events: synced to block %d (%d new)", finalizedBlock, totalEvents)
	return nil
}

// processBlockLogs processes all logs from a single block.
// All events + sync progress update happen in a single transaction (all-or-nothing).
func (s *EventSyncer) processBlockLogs(ctx context.Context, blockLogs BlockLogs) error {
	// Start transaction
	tx, err := s.storage.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}

	// Always rollback on error (no-op if already committed)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Process each log
	for _, log := range blockLogs.Logs {
		if err := s.processLog(ctx, tx, &log, blockLogs); err != nil {
			return fmt.Errorf("failed to process log at index %d: %w", log.Index, err)
		}
	}

	// Update sync progress (in same transaction - atomic with events)
	if err := tx.UpdateLastSyncedBlock(ctx, blockLogs.BlockNumber); err != nil {
		return fmt.Errorf("failed to update sync progress: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit tx: %w", err)
	}
	committed = true

	return nil
}

// processLog processes a single log entry.
func (s *EventSyncer) processLog(ctx context.Context, tx Tx, log *types.Log, blockLogs BlockLogs) error {
	// Parse event
	eventType, eventData, err := s.parser.ParseLog(log)
	if err != nil {
		// Store raw event with error
		return s.storeRawEvent(ctx, tx, log, blockLogs, err)
	}

	// Encode raw log and event to JSON
	rawLog, err := EncodeLogToJSON(log)
	if err != nil {
		return fmt.Errorf("failed to encode raw log: %w", err)
	}

	rawEvent, err := EncodeEventToJSON(eventData)
	if err != nil {
		return fmt.Errorf("failed to encode event: %w", err)
	}

	// Calculate slot from block time using beacon spec
	slot := s.spec.SlotAt(blockLogs.BlockTime)

	// Compute cluster ID from event data
	clusterID := computeClusterIDFromEvent(eventData)

	// Store contract event
	contractEvent := &ContractEvent{
		EventType:        eventType,
		Slot:             slot,
		BlockNumber:      blockLogs.BlockNumber,
		BlockHash:        log.BlockHash.Bytes(),
		BlockTime:        blockLogs.BlockTime,
		TransactionHash:  log.TxHash.Bytes(),
		TransactionIndex: uint32(log.TxIndex),
		LogIndex:         uint32(log.Index),
		ClusterID:        clusterID,
		RawLog:           rawLog,
		RawEvent:         rawEvent,
	}

	if err := tx.InsertEvent(ctx, contractEvent); err != nil {
		return fmt.Errorf("failed to insert event: %w", err)
	}

	// Update state tables based on event type
	if err := s.updateState(ctx, tx, eventType, eventData, slot, uint32(log.Index)); err != nil {
		return fmt.Errorf("failed to update state: %w", err)
	}

	return nil
}

// storeRawEvent stores a raw event when parsing fails.
func (s *EventSyncer) storeRawEvent(ctx context.Context, tx Tx, log *types.Log, blockLogs BlockLogs, parseErr error) error {
	rawLog, err := EncodeLogToJSON(log)
	if err != nil {
		return fmt.Errorf("failed to encode raw log: %w", err)
	}

	// Calculate slot from block time using beacon spec
	slot := s.spec.SlotAt(blockLogs.BlockTime)

	errMsg := parseErr.Error()
	contractEvent := &ContractEvent{
		EventType:        "Unknown",
		Slot:             slot,
		BlockNumber:      blockLogs.BlockNumber,
		BlockHash:        log.BlockHash.Bytes(),
		BlockTime:        blockLogs.BlockTime,
		TransactionHash:  log.TxHash.Bytes(),
		TransactionIndex: uint32(log.TxIndex),
		LogIndex:         uint32(log.Index),
		RawLog:           rawLog,
		RawEvent:         []byte("{}"),
		Error:            &errMsg,
	}

	return tx.InsertEvent(ctx, contractEvent)
}

// updateState updates validator and cluster state based on event.
func (s *EventSyncer) updateState(ctx context.Context, tx Tx, eventType string, eventData interface{}, slot uint64, logIndex uint32) error {
	switch eventType {
	case EventValidatorAdded:
		return s.handleValidatorAdded(ctx, tx, eventData.(*ValidatorAddedEvent), slot, logIndex)
	case EventValidatorRemoved:
		return s.handleValidatorRemoved(ctx, tx, eventData.(*ValidatorRemovedEvent), slot, logIndex)
	case EventClusterLiquidated:
		return s.handleClusterLiquidated(ctx, tx, eventData.(*ClusterLiquidatedEvent), slot, logIndex)
	case EventClusterReactivated:
		return s.handleClusterReactivated(ctx, tx, eventData.(*ClusterReactivatedEvent), slot, logIndex)
	case EventClusterWithdrawn:
		return s.handleClusterWithdrawn(ctx, tx, eventData.(*ClusterWithdrawnEvent), slot)
	case EventClusterDeposited:
		return s.handleClusterDeposited(ctx, tx, eventData.(*ClusterDepositedEvent), slot)
	default:
		return fmt.Errorf("unknown event type: %s", eventType)
	}
}

func (s *EventSyncer) handleValidatorAdded(ctx context.Context, tx Tx, event *ValidatorAddedEvent, slot uint64, logIndex uint32) error {
	clusterID := ComputeClusterID(event.Owner, event.OperatorIDs)

	// Insert validator event (Added)
	validatorEvent := &ValidatorEvent{
		ClusterID:       clusterID[:],
		ValidatorPubkey: event.PublicKey,
		Slot:            slot,
		LogIndex:        logIndex,
		IsActive:        true, // Added = active
	}

	if err := tx.InsertValidatorEvent(ctx, validatorEvent); err != nil {
		return err
	}

	// Upsert cluster state
	cluster := &ClusterState{
		ClusterID:       clusterID[:],
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot, // Use slot for tracking
	}

	return tx.UpsertClusterState(ctx, cluster)
}

func (s *EventSyncer) handleValidatorRemoved(ctx context.Context, tx Tx, event *ValidatorRemovedEvent, slot uint64, logIndex uint32) error {
	clusterID := ComputeClusterID(event.Owner, event.OperatorIDs)

	// Insert validator event (Removed)
	validatorEvent := &ValidatorEvent{
		ClusterID:       clusterID[:],
		ValidatorPubkey: event.PublicKey,
		Slot:            slot,
		LogIndex:        logIndex,
		IsActive:        false, // Removed = inactive
	}

	if err := tx.InsertValidatorEvent(ctx, validatorEvent); err != nil {
		return err
	}

	// Update cluster state
	cluster := &ClusterState{
		ClusterID:       clusterID[:],
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}

	return tx.UpsertClusterState(ctx, cluster)
}

func (s *EventSyncer) handleClusterLiquidated(ctx context.Context, tx Tx, event *ClusterLiquidatedEvent, slot uint64, logIndex uint32) error {
	clusterID := ComputeClusterID(event.Owner, event.OperatorIDs)

	// Insert cluster event (Liquidated)
	clusterEvent := &ClusterEvent{
		ClusterID: clusterID[:],
		Slot:      slot,
		LogIndex:  logIndex,
		IsActive:  false, // Liquidated = inactive
	}

	if err := tx.InsertClusterEvent(ctx, clusterEvent); err != nil {
		return err
	}

	// Update cluster state (inactive)
	cluster := &ClusterState{
		ClusterID:       clusterID[:],
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        false, // Liquidated = inactive
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}

	return tx.UpsertClusterState(ctx, cluster)
}

func (s *EventSyncer) handleClusterReactivated(ctx context.Context, tx Tx, event *ClusterReactivatedEvent, slot uint64, logIndex uint32) error {
	clusterID := ComputeClusterID(event.Owner, event.OperatorIDs)

	// Insert cluster event (Reactivated)
	clusterEvent := &ClusterEvent{
		ClusterID: clusterID[:],
		Slot:      slot,
		LogIndex:  logIndex,
		IsActive:  true, // Reactivated = active
	}

	if err := tx.InsertClusterEvent(ctx, clusterEvent); err != nil {
		return err
	}

	// Update cluster state (active again)
	cluster := &ClusterState{
		ClusterID:       clusterID[:],
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        true, // Reactivated
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}

	return tx.UpsertClusterState(ctx, cluster)
}

func (s *EventSyncer) handleClusterWithdrawn(ctx context.Context, tx Tx, event *ClusterWithdrawnEvent, slot uint64) error {
	clusterID := ComputeClusterID(event.Owner, event.OperatorIDs)

	// Update cluster state with new balance (no cluster_events entry needed - just balance change)
	cluster := &ClusterState{
		ClusterID:       clusterID[:],
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}

	return tx.UpsertClusterState(ctx, cluster)
}

func (s *EventSyncer) handleClusterDeposited(ctx context.Context, tx Tx, event *ClusterDepositedEvent, slot uint64) error {
	clusterID := ComputeClusterID(event.Owner, event.OperatorIDs)

	// Update cluster state with new balance (no cluster_events entry needed - just balance change)
	cluster := &ClusterState{
		ClusterID:       clusterID[:],
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}

	return tx.UpsertClusterState(ctx, cluster)
}

// computeClusterIDFromEvent extracts owner and operatorIDs from event data and computes cluster ID.
// Returns nil for unknown event types.
func computeClusterIDFromEvent(eventData interface{}) []byte {
	switch e := eventData.(type) {
	case *ValidatorAddedEvent:
		id := ComputeClusterID(e.Owner, e.OperatorIDs)
		return id[:]
	case *ValidatorRemovedEvent:
		id := ComputeClusterID(e.Owner, e.OperatorIDs)
		return id[:]
	case *ClusterLiquidatedEvent:
		id := ComputeClusterID(e.Owner, e.OperatorIDs)
		return id[:]
	case *ClusterReactivatedEvent:
		id := ComputeClusterID(e.Owner, e.OperatorIDs)
		return id[:]
	case *ClusterWithdrawnEvent:
		id := ComputeClusterID(e.Owner, e.OperatorIDs)
		return id[:]
	case *ClusterDepositedEvent:
		id := ComputeClusterID(e.Owner, e.OperatorIDs)
		return id[:]
	default:
		return nil
	}
}
