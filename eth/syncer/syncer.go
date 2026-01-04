package syncer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/term"

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
	SetSyncMode(bulk bool) error
}

// EventSyncer continuously syncs SSV contract events to the database.
type EventSyncer struct {
	client      *execution.Client
	storage     Storage
	parser      *eventParser
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
		parser:      newParser(),
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
		if err = s.storage.UpdateLastSyncedBlock(ctx, deployBlock-1); err != nil {
			return fmt.Errorf("set initial sync block: %w", err)
		}
		logger.Infow("Initial sync position set", "block", deployBlock-1)
	}

	return s.syncOnce(ctx)
}

// batchResult holds a fetched batch ready for processing.
type batchResult struct {
	batchEnd uint64
	logs     []execution.BlockLogs
	err      error
}

// prefetchBuffer is the number of batches to prefetch ahead of processing.
const prefetchBuffer = 2

// bulkSyncThreshold is the minimum block gap to enable bulk sync mode.
const bulkSyncThreshold = 100000

// SyncToBlock syncs events from last synced block to the target block.
// Uses pipelined fetching: fetcher goroutine prefetches batches while
// the main goroutine processes them sequentially.
func (s *EventSyncer) SyncToBlock(ctx context.Context, targetBlock uint64) error {
	fromBlock, err := s.storage.GetLastSyncedBlock(ctx)
	if err != nil {
		return fmt.Errorf("get last synced block: %w", err)
	}

	if fromBlock >= targetBlock {
		logger.Debugw("Events already synced", "lastSynced", fromBlock, "target", targetBlock)
		return nil
	}

	start := time.Now()
	totalBlocks := int(targetBlock - fromBlock)
	if totalBlocks > bulkSyncThreshold {
		if s.storage.SetSyncMode(true) == nil {
			defer func() { _ = s.storage.SetSyncMode(false) }()
		}
	}

	isTTY := term.IsTerminal(int(os.Stderr.Fd()))

	barOpts := []progressbar.Option{
		progressbar.OptionSetDescription("Syncing events"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetWidth(40),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionThrottle(65 * time.Millisecond),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionClearOnFinish(),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	}
	if !isTTY {
		barOpts = append(barOpts, progressbar.OptionSetVisibility(false))
	}
	bar := progressbar.NewOptions(totalBlocks, barOpts...)

	// Cancellable context allows stopping fetcher if processing fails
	fetchCtx, cancelFetch := context.WithCancel(ctx)
	defer cancelFetch()

	batchCh := make(chan batchResult, prefetchBuffer)
	go s.runFetcher(fetchCtx, fromBlock+1, targetBlock, batchCh)

	knownEvents := 0
	var processErr error
	for batch := range batchCh {
		if batch.err != nil {
			processErr = batch.err
			break
		}

		count, err := s.processBatch(ctx, batch.batchEnd, batch.logs)
		if err != nil {
			processErr = err
			break
		}
		knownEvents += count

		_ = bar.Set(int(batch.batchEnd - fromBlock))
	}

	// Drain channel to unblock fetcher goroutine
	if processErr != nil {
		go func() {
			for range batchCh {
			}
		}()
		return processErr
	}

	// Check if sync was aborted by context cancellation
	if ctx.Err() != nil {
		return ctx.Err()
	}

	_ = bar.Finish()

	logger.Infow("Events synced", "from", fromBlock+1, "to", targetBlock, "events", knownEvents, "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// runFetcher fetches logs and sends batches to the channel.
// Closes the channel when done or on error.
func (s *EventSyncer) runFetcher(ctx context.Context, fromBlock, toBlock uint64, batchCh chan<- batchResult) {
	defer close(batchCh)

	topics := EventTopics() // Filter by handled event signatures
	err := s.client.FetchLogs(ctx, s.ssvContract, fromBlock, toBlock, topics,
		func(batchEnd uint64, logs []execution.BlockLogs) error {
			select {
			case batchCh <- batchResult{batchEnd: batchEnd, logs: logs}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})

	if err != nil {
		select {
		case batchCh <- batchResult{err: err}:
		case <-ctx.Done():
		}
	}
}

func (s *EventSyncer) syncOnce(ctx context.Context) error {
	finalizedBlock, err := s.client.GetFinalizedBlock(ctx)
	if err != nil {
		return fmt.Errorf("get finalized block: %w", err)
	}
	return s.SyncToBlock(ctx, finalizedBlock)
}

// processBatch processes all blocks in a batch within a single transaction.
func (s *EventSyncer) processBatch(ctx context.Context, batchEnd uint64, logs []execution.BlockLogs) (int, error) {
	if len(logs) == 0 {
		if err := s.storage.UpdateLastSyncedBlock(ctx, batchEnd); err != nil {
			return 0, fmt.Errorf("update sync progress: %w", err)
		}
		return 0, nil
	}

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
	for _, blockLogs := range logs {
		for _, log := range blockLogs.Logs {
			known, err := s.processLog(ctx, tx, &log, blockLogs)
			if err != nil {
				return 0, fmt.Errorf("process block %d log %d: %w",
					blockLogs.BlockNumber, log.Index, err)
			}
			if known {
				knownEvents++
			}
		}
	}

	if err := tx.UpdateLastSyncedBlock(ctx, batchEnd); err != nil {
		return 0, fmt.Errorf("update sync progress: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit batch: %w", err)
	}
	committed = true

	return knownEvents, nil
}

func (s *EventSyncer) processLog(ctx context.Context, tx storage.Tx, log *types.Log, blockLogs execution.BlockLogs) (bool, error) {
	eventType, eventData, err := s.parser.parseLog(log)
	if err != nil {
		return false, s.storeRawEvent(ctx, tx, log, blockLogs, err)
	}

	contractEvent := &storage.ContractEvent{
		EventType:        eventType,
		BlockNumber:      blockLogs.BlockNumber,
		BlockHash:        log.BlockHash.Bytes(),
		BlockTime:        blockLogs.BlockTime,
		TransactionHash:  log.TxHash.Bytes(),
		TransactionIndex: uint32(log.TxIndex),
		LogIndex:         uint32(log.Index),
	}

	if err := tx.InsertEvent(ctx, contractEvent); err != nil {
		return false, fmt.Errorf("insert event: %w", err)
	}

	if err := s.applyEvent(ctx, tx, eventType, eventData); err != nil {
		return false, fmt.Errorf("apply event: %w", err)
	}

	return true, nil
}

func (s *EventSyncer) storeRawEvent(ctx context.Context, tx storage.Tx, log *types.Log, blockLogs execution.BlockLogs, parseErr error) error {
	if !errors.Is(parseErr, errUnknownEvent) {
		logger.Warnw("Failed to parse event",
			"block", blockLogs.BlockNumber,
			"txHash", log.TxHash.Hex(),
			"logIndex", log.Index,
			"error", parseErr)
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
		Error:            &errMsg,
	}

	return tx.InsertEvent(ctx, contractEvent)
}

func (s *EventSyncer) applyEvent(ctx context.Context, tx storage.Tx, eventType string, eventData any) error {
	clusterID := computeClusterIDFromEvent(eventData)

	switch eventType {
	case eventValidatorAdded:
		return s.handleValidatorAdded(ctx, tx, eventData.(*validatorAddedEvent), clusterID)
	case eventValidatorRemoved:
		return s.handleValidatorRemoved(ctx, tx, eventData.(*validatorRemovedEvent), clusterID)
	case eventClusterLiquidated:
		return s.handleClusterLiquidated(ctx, tx, eventData.(*clusterLiquidatedEvent), clusterID)
	case eventClusterReactivated:
		return s.handleClusterReactivated(ctx, tx, eventData.(*clusterReactivatedEvent), clusterID)
	case eventClusterWithdrawn:
		return s.handleClusterWithdrawn(ctx, tx, eventData.(*clusterWithdrawnEvent), clusterID)
	case eventClusterDeposited:
		return s.handleClusterDeposited(ctx, tx, eventData.(*clusterDepositedEvent), clusterID)
	case eventClusterMigratedToETH:
		return s.handleClusterMigratedToETH(ctx, tx, eventData.(*clusterMigratedToETHEvent), clusterID)
	case eventClusterBalanceUpdated:
		return s.handleClusterBalanceUpdated(ctx, tx, eventData.(*clusterBalanceUpdatedEvent), clusterID)
	default:
		return fmt.Errorf("unhandled event type: %s", eventType)
	}
}

func (s *EventSyncer) handleValidatorAdded(ctx context.Context, tx storage.Tx, event *validatorAddedEvent, clusterID []byte) error {
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

func (s *EventSyncer) handleValidatorRemoved(ctx context.Context, tx storage.Tx, event *validatorRemovedEvent, clusterID []byte) error {
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

func (s *EventSyncer) handleClusterLiquidated(ctx context.Context, tx storage.Tx, event *clusterLiquidatedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterReactivated(ctx context.Context, tx storage.Tx, event *clusterReactivatedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterWithdrawn(ctx context.Context, tx storage.Tx, event *clusterWithdrawnEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterDeposited(ctx context.Context, tx storage.Tx, event *clusterDepositedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterMigratedToETH(ctx context.Context, tx storage.Tx, event *clusterMigratedToETHEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) handleClusterBalanceUpdated(ctx context.Context, tx storage.Tx, event *clusterBalanceUpdatedEvent, clusterID []byte) error {
	return s.upsertClusterFromEvent(ctx, tx, event.Owner, event.OperatorIDs, clusterID, &event.Cluster)
}

func (s *EventSyncer) upsertClusterFromEvent(ctx context.Context, tx storage.Tx, owner common.Address, operatorIDs []uint64, clusterID []byte, cluster *cluster) error {
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

func computeClusterIDFromEvent(eventData any) []byte {
	if e, ok := eventData.(clusterEvent); ok {
		owner, operatorIDs := e.clusterKey()
		id := computeClusterID(owner, operatorIDs)
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

	topics := EventTopics() // Filter by handled event signatures
	err = s.client.FetchLogs(ctx, s.ssvContract, fromBlock+1, headBlock, topics,
		func(batchEnd uint64, logs []execution.BlockLogs) error {
			for _, blockLogs := range logs {
				if err := s.applyClusterUpdates(ctx, blockLogs); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		return err
	}

	logger.Debugw("Clusters synced to head", "from", fromBlock+1, "to", headBlock)
	return nil
}

func (s *EventSyncer) applyClusterUpdates(ctx context.Context, blockLogs execution.BlockLogs) error {
	for _, log := range blockLogs.Logs {
		_, eventData, err := s.parser.parseLog(&log)
		if err != nil {
			if !errors.Is(err, errUnknownEvent) {
				logger.Warnw("Failed to parse event in head sync",
					"block", blockLogs.BlockNumber,
					"txHash", log.TxHash.Hex(),
					"logIndex", log.Index,
					"error", err)
			}
			continue
		}

		e, ok := eventData.(clusterEvent)
		if !ok {
			continue
		}

		owner, operatorIDs := e.clusterKey()
		clusterID := computeClusterID(owner, operatorIDs)
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
