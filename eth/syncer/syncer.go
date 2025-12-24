package syncer

import (
	"context"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/schollz/progressbar/v3"

	"ssv-oracle/eth/execution"
	"ssv-oracle/logger"
	"ssv-oracle/storage"
)

// Storage defines the interface the syncer needs for persistence.
type Storage interface {
	GetLastSyncedBlock(ctx context.Context) (uint64, error)
	UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error
	BeginTx(ctx context.Context) (storage.Tx, error)
	UpdateClusterIfExists(ctx context.Context, cluster *storage.ClusterRow) error
}

// EventSyncer continuously syncs SSV contract events to the database.
type EventSyncer struct {
	client      *execution.Client
	storage     Storage
	parser      *EventParser
	ssvContract common.Address
}

// Config holds configuration for the event syncer.
type Config struct {
	ExecutionClient *execution.Client
	Storage         Storage
	SSVContract     common.Address
}

// New creates a new event syncer.
func New(cfg Config) *EventSyncer {
	return &EventSyncer{
		client:      cfg.ExecutionClient,
		storage:     cfg.Storage,
		parser:      NewParser(),
		ssvContract: cfg.SSVContract,
	}
}

// SyncToFinalized syncs SSV contract events up to the current finalized block.
func (s *EventSyncer) SyncToFinalized(ctx context.Context, deployBlock uint64) error {
	lastSynced, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("get last synced block: %w", err)
	}

	if lastSynced == 0 {
		logger.Infow("First run: setting initial sync position", "block", deployBlock-1)
		err = s.storage.UpdateLastSyncedBlock(ctx, deployBlock-1)
		if err != nil {
			return fmt.Errorf("set initial sync block: %w", err)
		}
	}

	return s.syncOnce(ctx)
}

// SyncToBlock syncs events from last synced block to the target block.
func (s *EventSyncer) SyncToBlock(ctx context.Context, targetBlock uint64) error {
	fromBlock, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("get last synced block: %w", err)
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
	err = s.client.FetchLogs(ctx, s.ssvContract, fromBlock+1, targetBlock, func(batchEnd uint64, logs []execution.BlockLogs) error {
		for _, blockLogs := range logs {
			count, err := s.processBlockLogs(ctx, blockLogs)
			if err != nil {
				return fmt.Errorf("process block %d: %w", blockLogs.BlockNumber, err)
			}
			knownEvents += count
		}

		_ = bar.Set(int(batchEnd - fromBlock))

		// Advance sync progress to batch end.
		// This is needed for batches with no events (or sparse events) to ensure
		// we don't re-scan empty block ranges on restart.
		// Note: Per-block tx already updates progress for blocks WITH events.
		if err := s.storage.UpdateLastSyncedBlock(ctx, batchEnd); err != nil {
			return fmt.Errorf("update sync progress: %w", err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("fetch logs: %w", err)
	}

	_ = bar.Finish()
	fmt.Println() // New line after progress bar
	logger.Infow("Events synced", "from", fromBlock+1, "to", targetBlock, "newEvents", knownEvents)
	return nil
}

// syncOnce syncs to the current finalized block.
func (s *EventSyncer) syncOnce(ctx context.Context) error {
	finalizedBlock, err := s.client.GetFinalizedBlock(ctx)
	if err != nil {
		return fmt.Errorf("get finalized block: %w", err)
	}
	return s.SyncToBlock(ctx, finalizedBlock)
}

// processBlockLogs processes all logs from a block in a single transaction.
// Returns the count of known (successfully parsed) events.
func (s *EventSyncer) processBlockLogs(ctx context.Context, blockLogs execution.BlockLogs) (int, error) {
	tx, err := s.storage.BeginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
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
			return 0, fmt.Errorf("process log at index %d: %w", log.Index, err)
		}
		if known {
			knownEvents++
		}
	}

	// Update sync progress atomically with events
	if err := tx.UpdateLastSyncedBlock(ctx, blockLogs.BlockNumber); err != nil {
		return 0, fmt.Errorf("update sync progress: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	committed = true

	return knownEvents, nil
}

// processLog processes a single log entry.
// Returns (known, error) where known indicates if the event was a recognized type.
func (s *EventSyncer) processLog(ctx context.Context, tx storage.Tx, log *types.Log, blockLogs execution.BlockLogs) (bool, error) {
	eventType, eventData, err := s.parser.ParseLog(log)
	if err != nil {
		return false, s.storeRawEvent(ctx, tx, log, blockLogs, err)
	}

	rawLog, err := EncodeLogToJSON(log)
	if err != nil {
		return false, fmt.Errorf("encode raw log: %w", err)
	}

	rawEvent, err := EncodeEventToJSON(eventData)
	if err != nil {
		return false, fmt.Errorf("encode event: %w", err)
	}

	contractEvent := &storage.ContractEvent{
		EventType:        eventType,
		BlockNumber:      blockLogs.BlockNumber,
		BlockHash:        log.BlockHash.Bytes(),
		BlockTime:        blockLogs.BlockTime,
		TransactionHash:  log.TxHash.Bytes(),
		TransactionIndex: uint32(log.TxIndex),
		LogIndex:         uint32(log.Index),
		RawLog:           rawLog,
		RawEvent:         rawEvent,
	}

	if err := tx.InsertEvent(ctx, contractEvent); err != nil {
		return false, fmt.Errorf("insert event: %w", err)
	}

	if err := s.applyEvent(ctx, tx, eventType, eventData); err != nil {
		return false, fmt.Errorf("apply event: %w", err)
	}

	return true, nil
}

// storeRawEvent stores a raw event when parsing fails.
func (s *EventSyncer) storeRawEvent(ctx context.Context, tx storage.Tx, log *types.Log, blockLogs execution.BlockLogs, parseErr error) error {
	if !errors.Is(parseErr, errUnknownEvent) {
		logger.Warnw("Failed to parse event",
			"block", blockLogs.BlockNumber,
			"txHash", log.TxHash.Hex(),
			"logIndex", log.Index,
			"error", parseErr)
	}

	rawLog, err := EncodeLogToJSON(log)
	if err != nil {
		return fmt.Errorf("encode raw log: %w", err)
	}

	errMsg := parseErr.Error()
	contractEvent := &storage.ContractEvent{
		EventType:        "Unknown",
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

func (s *EventSyncer) applyEvent(ctx context.Context, tx storage.Tx, eventType string, eventData any) error {
	clusterID := computeClusterIDFromEvent(eventData)

	switch eventType {
	case EventValidatorAdded:
		return s.handleValidatorAdded(ctx, tx, eventData.(*ValidatorAddedEvent), clusterID)
	case EventValidatorRemoved:
		return s.handleValidatorRemoved(ctx, tx, eventData.(*ValidatorRemovedEvent), clusterID)
	case EventClusterLiquidated:
		return s.handleClusterLiquidated(ctx, tx, eventData.(*ClusterLiquidatedEvent), clusterID)
	case EventClusterReactivated:
		return s.handleClusterReactivated(ctx, tx, eventData.(*ClusterReactivatedEvent), clusterID)
	case EventClusterWithdrawn:
		return s.handleClusterWithdrawn(ctx, tx, eventData.(*ClusterWithdrawnEvent), clusterID)
	case EventClusterDeposited:
		return s.handleClusterDeposited(ctx, tx, eventData.(*ClusterDepositedEvent), clusterID)
	case EventClusterMigratedToETH:
		return s.handleClusterMigratedToETH(ctx, tx, eventData.(*ClusterMigratedToETHEvent), clusterID)
	case EventClusterBalanceUpdated:
		return s.handleClusterBalanceUpdated(ctx, tx, eventData.(*ClusterBalanceUpdatedEvent), clusterID)
	default:
		return fmt.Errorf("unhandled event type: %s", eventType)
	}
}

func (s *EventSyncer) handleValidatorAdded(ctx context.Context, tx storage.Tx, event *ValidatorAddedEvent, clusterID []byte) error {
	cluster := &storage.ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
	}
	if err := tx.UpsertCluster(ctx, cluster); err != nil {
		return err
	}

	return tx.InsertValidator(ctx, clusterID, event.PublicKey)
}

// handleValidatorRemoved deletes the validator and removes the cluster if empty.
func (s *EventSyncer) handleValidatorRemoved(ctx context.Context, tx storage.Tx, event *ValidatorRemovedEvent, clusterID []byte) error {
	if err := tx.DeleteValidator(ctx, clusterID, event.PublicKey); err != nil {
		return err
	}

	// Delete cluster when last validator is removed
	if event.Cluster.ValidatorCount == 0 {
		return tx.DeleteCluster(ctx, clusterID)
	}

	cluster := &storage.ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    event.Owner.Bytes(),
		OperatorIDs:     event.OperatorIDs,
		ValidatorCount:  event.Cluster.ValidatorCount,
		NetworkFeeIndex: event.Cluster.NetworkFeeIndex,
		Index:           event.Cluster.Index,
		IsActive:        event.Cluster.Active,
		Balance:         event.Cluster.Balance,
	}
	return tx.UpsertCluster(ctx, cluster)
}

func (s *EventSyncer) handleClusterLiquidated(ctx context.Context, tx storage.Tx, event *ClusterLiquidatedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterReactivated(ctx context.Context, tx storage.Tx, event *ClusterReactivatedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterWithdrawn(ctx context.Context, tx storage.Tx, event *ClusterWithdrawnEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterDeposited(ctx context.Context, tx storage.Tx, event *ClusterDepositedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterMigratedToETH(ctx context.Context, tx storage.Tx, event *ClusterMigratedToETHEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterBalanceUpdated(ctx context.Context, tx storage.Tx, event *ClusterBalanceUpdatedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) upsertClusterFromEvent(ctx context.Context, tx storage.Tx, owner common.Address, operatorIDs []uint64, clusterID []byte, cluster *Cluster) error {
	row := &storage.ClusterRow{
		ClusterID:       clusterID,
		OwnerAddress:    owner.Bytes(),
		OperatorIDs:     operatorIDs,
		ValidatorCount:  cluster.ValidatorCount,
		NetworkFeeIndex: cluster.NetworkFeeIndex,
		Index:           cluster.Index,
		IsActive:        cluster.Active,
		Balance:         cluster.Balance,
	}
	return tx.UpsertCluster(ctx, row)
}

// computeClusterIDFromEvent extracts cluster ID from event data, or nil if unknown type.
func computeClusterIDFromEvent(eventData any) []byte {
	if e, ok := eventData.(clusterEvent); ok {
		owner, operatorIDs := e.clusterKey()
		id := ComputeClusterID(owner, operatorIDs)
		return id[:]
	}
	return nil
}

// SyncClustersToHead fetches events from finalized to head and updates
// only the clusters table. Does not modify contract_events, validators,
// or sync_progress. Used by updater to get fresh cluster data.
func (s *EventSyncer) SyncClustersToHead(ctx context.Context) error {
	fromBlock, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("get last synced block: %w", err)
	}

	headBlock, err := s.client.GetHeadBlock(ctx)
	if err != nil {
		return fmt.Errorf("get head block: %w", err)
	}

	if fromBlock >= headBlock {
		return nil
	}

	logger.Debugw("Syncing clusters to head",
		"fromBlock", fromBlock+1,
		"headBlock", headBlock)

	return s.client.FetchLogs(ctx, s.ssvContract, fromBlock+1, headBlock,
		func(batchEnd uint64, logs []execution.BlockLogs) error {
			for _, blockLogs := range logs {
				if err := s.applyClusterUpdates(ctx, blockLogs); err != nil {
					return err
				}
			}
			return nil
		})
}

// applyClusterUpdates processes events and updates only the clusters table.
// Does not insert events, validators, or update sync progress.
func (s *EventSyncer) applyClusterUpdates(ctx context.Context, blockLogs execution.BlockLogs) error {
	for _, log := range blockLogs.Logs {
		_, eventData, err := s.parser.ParseLog(&log)
		if err != nil {
			continue // skip unknown events
		}

		e, ok := eventData.(clusterEvent)
		if !ok {
			continue
		}

		owner, operatorIDs := e.clusterKey()
		clusterID := ComputeClusterID(owner, operatorIDs)
		cluster := e.cluster()

		row := &storage.ClusterRow{
			ClusterID:       clusterID[:],
			NetworkFeeIndex: cluster.NetworkFeeIndex,
			Index:           cluster.Index,
			IsActive:        cluster.Active,
			Balance:         cluster.Balance,
		}

		if err := s.storage.UpdateClusterIfExists(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
