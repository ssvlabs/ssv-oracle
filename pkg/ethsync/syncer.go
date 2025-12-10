package ethsync

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/schollz/progressbar/v3"

	"ssv-oracle/pkg/logger"
)

// storage defines the interface the syncer needs for persistence.
type storage interface {
	GetLastSyncedBlock(ctx context.Context) (uint64, error)
	UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error
	BeginTx(ctx context.Context) (Tx, error)
}

// EventSyncer continuously syncs SSV contract events to the database.
type EventSyncer struct {
	client      *ExecutionClient
	storage     storage
	parser      *EventParser
	ssvContract common.Address
	spec        *Spec
}

// EventSyncerConfig holds configuration for the event syncer.
type EventSyncerConfig struct {
	ExecutionClient *ExecutionClient
	Storage         *Storage
	SSVContract     common.Address
	Spec            *Spec // Beacon chain spec for slot/epoch calculations
}

// NewEventSyncer creates a new event syncer.
func NewEventSyncer(cfg EventSyncerConfig) (*EventSyncer, error) {
	if cfg.Spec == nil {
		return nil, fmt.Errorf("spec is required for epoch calculations")
	}

	return &EventSyncer{
		client:      cfg.ExecutionClient,
		storage:     cfg.Storage,
		parser:      NewEventParser(),
		ssvContract: cfg.SSVContract,
		spec:        cfg.Spec,
	}, nil
}

// SyncToFinalized syncs from last synced block to current finalized block.
func (s *EventSyncer) SyncToFinalized(ctx context.Context, fromBlock uint64) error {
	lastSynced, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last synced block: %w", err)
	}

	if lastSynced == 0 {
		if fromBlock == 0 {
			return fmt.Errorf("sync_from_block must be set to the SSV contract deployment block")
		}

		logger.Infow("First run: setting initial sync position", "block", fromBlock-1)
		err = s.storage.UpdateLastSyncedBlock(ctx, fromBlock-1)
		if err != nil {
			return fmt.Errorf("failed to set initial sync block: %w", err)
		}
	}

	return s.syncOnce(ctx)
}

// SyncIncremental performs one incremental sync (used in oracle cycle).
func (s *EventSyncer) SyncIncremental(ctx context.Context) error {
	return s.syncOnce(ctx)
}

// SyncToBlock syncs events from last synced block to the target block.
func (s *EventSyncer) SyncToBlock(ctx context.Context, targetBlock uint64) error {
	fromBlock, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last synced block: %w", err)
	}

	if fromBlock >= targetBlock {
		logger.Debugw("Events already synced", "lastSynced", fromBlock, "target", targetBlock)
		return nil
	}

	totalBlocks := int(targetBlock - fromBlock)

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

	knownEvents := 0
	err = s.client.FetchLogs(ctx, s.ssvContract, fromBlock+1, targetBlock, func(batchEnd uint64, logs []BlockLogs) error {
		for _, blockLogs := range logs {
			count, err := s.processBlockLogs(ctx, blockLogs)
			if err != nil {
				return fmt.Errorf("failed to process block %d: %w", blockLogs.BlockNumber, err)
			}
			knownEvents += count
		}

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
	logger.Infow("Events synced", "block", targetBlock, "newEvents", knownEvents)
	return nil
}

// syncOnce syncs to the current finalized block.
func (s *EventSyncer) syncOnce(ctx context.Context) error {
	finalizedBlock, err := s.client.GetFinalizedBlock(ctx)
	if err != nil {
		return fmt.Errorf("failed to get finalized block: %w", err)
	}
	return s.SyncToBlock(ctx, finalizedBlock)
}

// processBlockLogs processes all logs from a block in a single transaction.
// Returns the count of known (successfully parsed) events.
func (s *EventSyncer) processBlockLogs(ctx context.Context, blockLogs BlockLogs) (int, error) {
	tx, err := s.storage.BeginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to begin tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	knownEvents := 0
	for _, log := range blockLogs.Logs {
		known, err := s.processLog(ctx, tx, &log, blockLogs)
		if err != nil {
			return 0, fmt.Errorf("failed to process log at index %d: %w", log.Index, err)
		}
		if known {
			knownEvents++
		}
	}

	// Update sync progress atomically with events
	if err := tx.UpdateLastSyncedBlock(ctx, blockLogs.BlockNumber); err != nil {
		return 0, fmt.Errorf("failed to update sync progress: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit tx: %w", err)
	}
	committed = true

	return knownEvents, nil
}

// processLog processes a single log entry.
// Returns (known, error) where known indicates if the event was a recognized type.
func (s *EventSyncer) processLog(ctx context.Context, tx Tx, log *types.Log, blockLogs BlockLogs) (bool, error) {
	eventType, eventData, err := s.parser.ParseLog(log)
	if err != nil {
		return false, s.storeRawEvent(ctx, tx, log, blockLogs, err)
	}

	rawLog, err := EncodeLogToJSON(log)
	if err != nil {
		return false, fmt.Errorf("failed to encode raw log: %w", err)
	}

	rawEvent, err := EncodeEventToJSON(eventData)
	if err != nil {
		return false, fmt.Errorf("failed to encode event: %w", err)
	}

	slot := s.spec.SlotAt(blockLogs.BlockTime)
	clusterID := computeClusterIDFromEvent(eventData)

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
		return false, fmt.Errorf("failed to insert event: %w", err)
	}

	if err := s.updateState(ctx, tx, eventType, eventData, clusterID, slot); err != nil {
		return false, fmt.Errorf("failed to update state: %w", err)
	}

	return true, nil
}

// storeRawEvent stores a raw event when parsing fails.
func (s *EventSyncer) storeRawEvent(ctx context.Context, tx Tx, log *types.Log, blockLogs BlockLogs, parseErr error) error {
	rawLog, err := EncodeLogToJSON(log)
	if err != nil {
		return fmt.Errorf("failed to encode raw log: %w", err)
	}

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

func (s *EventSyncer) updateState(ctx context.Context, tx Tx, eventType string, eventData interface{}, clusterID []byte, slot uint64) error {
	switch eventType {
	case EventValidatorAdded:
		return s.handleValidatorAdded(ctx, tx, eventData.(*ValidatorAddedEvent), clusterID, slot)
	case EventValidatorRemoved:
		return s.handleValidatorRemoved(ctx, tx, eventData.(*ValidatorRemovedEvent), clusterID, slot)
	case EventClusterLiquidated:
		return s.handleClusterLiquidated(ctx, tx, eventData.(*ClusterLiquidatedEvent), clusterID, slot)
	case EventClusterReactivated:
		return s.handleClusterReactivated(ctx, tx, eventData.(*ClusterReactivatedEvent), clusterID, slot)
	case EventClusterWithdrawn:
		return s.handleClusterWithdrawn(ctx, tx, eventData.(*ClusterWithdrawnEvent), clusterID, slot)
	case EventClusterDeposited:
		return s.handleClusterDeposited(ctx, tx, eventData.(*ClusterDepositedEvent), clusterID, slot)
	case EventClusterMigratedToETH:
		return s.handleClusterMigratedToETH(ctx, tx, eventData.(*ClusterMigratedToETHEvent), clusterID, slot)
	case EventClusterBalanceUpdated:
		return s.handleClusterBalanceUpdated(ctx, tx, eventData.(*ClusterBalanceUpdatedEvent), slot)
	default:
		return fmt.Errorf("unhandled event type: %s", eventType)
	}
}

func (s *EventSyncer) handleValidatorAdded(ctx context.Context, tx Tx, event *ValidatorAddedEvent, clusterID []byte, slot uint64) error {
	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}
	if err := tx.UpsertCluster(ctx, cluster); err != nil {
		return err
	}

	return tx.InsertValidator(ctx, clusterID, event.PublicKey)
}

// handleValidatorRemoved deletes the validator and removes the cluster if empty.
func (s *EventSyncer) handleValidatorRemoved(ctx context.Context, tx Tx, event *ValidatorRemovedEvent, clusterID []byte, slot uint64) error {
	if err := tx.DeleteValidator(ctx, clusterID, event.PublicKey); err != nil {
		return err
	}

	// Delete cluster when last validator is removed
	if event.Cluster.ValidatorCount == 0 {
		return tx.DeleteCluster(ctx, clusterID)
	}

	cluster := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}
	return tx.UpsertCluster(ctx, cluster)
}

func (s *EventSyncer) handleClusterLiquidated(ctx context.Context, tx Tx, event *ClusterLiquidatedEvent, clusterID []byte, slot uint64) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster, slot)
}

func (s *EventSyncer) handleClusterReactivated(ctx context.Context, tx Tx, event *ClusterReactivatedEvent, clusterID []byte, slot uint64) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster, slot)
}

func (s *EventSyncer) handleClusterWithdrawn(ctx context.Context, tx Tx, event *ClusterWithdrawnEvent, clusterID []byte, slot uint64) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster, slot)
}

func (s *EventSyncer) handleClusterDeposited(ctx context.Context, tx Tx, event *ClusterDepositedEvent, clusterID []byte, slot uint64) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster, slot)
}

func (s *EventSyncer) handleClusterMigratedToETH(ctx context.Context, tx Tx, event *ClusterMigratedToETHEvent, clusterID []byte, slot uint64) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster, slot)
}

func (s *EventSyncer) handleClusterBalanceUpdated(ctx context.Context, tx Tx, event *ClusterBalanceUpdatedEvent, slot uint64) error {
	row := &ClusterRow{
		ClusterID:       event.ClusterID[:],
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
		LastUpdatedSlot: slot,
	}
	return tx.UpsertCluster(ctx, row)
}

func (s *EventSyncer) upsertClusterFromEvent(ctx context.Context, tx Tx, owner common.Address, operatorIDs []uint64, clusterID []byte, cluster *Cluster, slot uint64) error {
	row := &ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    owner.Bytes(),
		OperatorIDs:     operatorIDs,
		ValidatorCount:  cluster.ValidatorCount,
		NetworkFeeIndex: cluster.NetworkFeeIndex,
		Index:           cluster.Index,
		IsActive:        cluster.Active,
		Balance:         cluster.Balance,
		LastUpdatedSlot: slot,
	}
	return tx.UpsertCluster(ctx, row)
}

// clusterEvent is implemented by all events that have Owner and OperatorIDs.
type clusterEvent interface {
	clusterKey() (common.Address, []uint64)
}

func (e *ValidatorAddedEvent) clusterKey() (common.Address, []uint64) { return e.Owner, e.OperatorIDs }
func (e *ValidatorRemovedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *ClusterLiquidatedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *ClusterReactivatedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *ClusterWithdrawnEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *ClusterDepositedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *ClusterMigratedToETHEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}

// computeClusterIDFromEvent extracts cluster ID from event data, or nil if unknown type.
func computeClusterIDFromEvent(eventData interface{}) []byte {
	if e, ok := eventData.(clusterEvent); ok {
		owner, operatorIDs := e.clusterKey()
		id := ComputeClusterID(owner, operatorIDs)
		return id[:]
	}
	return nil
}
