package ethsync

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/lib/pq"

	"ssv-oracle/pkg/logger"
)

// Storage defines the interface for persisting SSV event data and state.
type Storage interface {
	// Event storage
	InsertEvent(ctx context.Context, event *ContractEvent) error
	GetLastSyncedBlock(ctx context.Context) (uint64, error)
	UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error

	// Chain ID validation
	GetChainID(ctx context.Context) (*uint64, error)
	SetChainID(ctx context.Context, chainID uint64) error

	// Validator events (membership changes)
	InsertValidatorEvent(ctx context.Context, event *ValidatorEvent) error

	// Cluster events (operational status changes)
	InsertClusterEvent(ctx context.Context, event *ClusterEvent) error

	// Validator balances
	InsertValidatorBalance(ctx context.Context, balance *ValidatorBalance) error

	// Cluster state (current metadata, updated in place)
	UpsertClusterState(ctx context.Context, cluster *ClusterState) error
	GetClusterState(ctx context.Context, clusterID []byte) (*ClusterState, error)

	// Queries (epoch-based, slotsPerEpoch from beacon spec)
	GetClusterBalances(ctx context.Context, targetEpoch uint64, slotsPerEpoch uint64) ([]ClusterBalance, error)
	GetActiveValidatorsWithClusters(ctx context.Context, atEpoch uint64, slotsPerEpoch uint64) ([]ActiveValidator, error)
	GetLatestValidatorBalances(ctx context.Context, validators []ActiveValidator, beforeEpoch uint64) (map[string]uint64, error)
	IsReadyToCommit(ctx context.Context, targetEpoch uint64, slotsPerEpoch uint64) (bool, error)

	// Oracle commits
	InsertOracleCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, txHash []byte) error

	// Transaction support
	BeginTx(ctx context.Context) (Tx, error)

	// Close
	Close() error
}

// Tx defines transaction interface.
type Tx interface {
	Commit() error
	Rollback() error
	InsertEvent(ctx context.Context, event *ContractEvent) error
	InsertValidatorEvent(ctx context.Context, event *ValidatorEvent) error
	InsertClusterEvent(ctx context.Context, event *ClusterEvent) error
	UpsertClusterState(ctx context.Context, cluster *ClusterState) error
	UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error
}

// ContractEvent represents a single SSV contract event.
type ContractEvent struct {
	EventType        string
	Slot             uint64
	BlockNumber      uint64
	BlockHash        []byte
	BlockTime        time.Time
	TransactionHash  []byte
	TransactionIndex uint32
	LogIndex         uint32
	ClusterID        []byte          // Computed from owner + operatorIds (nil for unknown/error events)
	RawLog           json.RawMessage // JSONB
	RawEvent         json.RawMessage // JSONB
	Error            *string
}

// ValidatorEvent represents a validator membership change (Added/Removed).
// Key: (cluster_id, validator_pubkey, slot, log_index)
type ValidatorEvent struct {
	ClusterID       []byte
	ValidatorPubkey []byte
	Slot            uint64
	LogIndex        uint32
	IsActive        bool // true = Added, false = Removed
}

// ClusterEvent represents a cluster operational status change (Liquidated/Reactivated).
// Key: (cluster_id, slot, log_index)
type ClusterEvent struct {
	ClusterID []byte
	Slot      uint64
	LogIndex  uint32
	IsActive  bool // false = Liquidated, true = Reactivated
}

// ValidatorBalance represents a validator's effective balance snapshot.
// Key: (cluster_id, validator_pubkey, epoch)
type ValidatorBalance struct {
	ClusterID        []byte
	ValidatorPubkey  []byte
	Epoch            uint64
	EffectiveBalance uint64 // In Gwei
}

// ClusterState represents complete cluster state (current metadata).
type ClusterState struct {
	ClusterID       []byte
	OwnerAddress    []byte
	OperatorIDs     []uint64
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	IsActive        bool
	Balance         *big.Int // SSV token balance (uint256)
	LastUpdatedSlot uint64
}

// ClusterBalance represents aggregated cluster effective balance.
type ClusterBalance struct {
	ClusterID             []byte
	TotalEffectiveBalance uint64
	ValidatorCount        uint64
}

// ActiveValidator represents an active validator with its cluster membership.
type ActiveValidator struct {
	ClusterID       []byte
	ValidatorPubkey []byte
}

// OracleCommit represents a committed merkle root (from oracle_commits table).
type OracleCommit struct {
	RoundID        uint64
	TargetEpoch    uint64
	MerkleRoot     []byte
	ReferenceBlock uint64
}

// PostgresStorage implements Storage using PostgreSQL.
type PostgresStorage struct {
	db *sql.DB
}

//go:embed schema.sql
var schemaSQL string

// NewPostgresStorage creates a new PostgreSQL storage instance.
// It automatically applies the schema if tables don't exist.
func NewPostgresStorage(connString string) (*PostgresStorage, error) {
	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Auto-apply schema
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}
	logger.Info("Database schema applied")

	return &PostgresStorage{db: db}, nil
}

// Close closes the database connection.
func (s *PostgresStorage) Close() error {
	return s.db.Close()
}

// GetChainID returns the chain ID from the database, or nil if not set.
func (s *PostgresStorage) GetChainID(ctx context.Context) (*uint64, error) {
	var chainID *uint64
	query := `SELECT chain_id FROM sync_progress WHERE id = 1`

	err := s.db.QueryRowContext(ctx, query).Scan(&chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	return chainID, nil
}

// SetChainID stores the chain ID in the database.
func (s *PostgresStorage) SetChainID(ctx context.Context, chainID uint64) error {
	query := `
		UPDATE sync_progress
		SET chain_id = $1,
		    updated_at = NOW()
		WHERE id = 1
	`

	_, err := s.db.ExecContext(ctx, query, chainID)
	if err != nil {
		return fmt.Errorf("failed to set chain ID: %w", err)
	}

	return nil
}

// InsertOracleCommit records an oracle commit in the database and notifies listeners.
func (s *PostgresStorage) InsertOracleCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, txHash []byte) error {
	query := `
		INSERT INTO oracle_commits (round_id, target_epoch, merkle_root, reference_block, tx_hash, tx_status, submitted_at)
		VALUES ($1, $2, $3, $4, $5, 'confirmed', NOW())
		ON CONFLICT (round_id) DO UPDATE
		SET merkle_root = EXCLUDED.merkle_root,
		    reference_block = EXCLUDED.reference_block,
		    tx_hash = EXCLUDED.tx_hash,
		    tx_status = 'confirmed',
		    confirmed_at = NOW()
	`

	_, err := s.db.ExecContext(ctx, query, roundID, targetEpoch, merkleRoot, referenceBlock, txHash)
	if err != nil {
		return fmt.Errorf("failed to insert oracle commit: %w", err)
	}

	// Notify listeners (for updater in mock mode)
	logger.Debugw("Sending NOTIFY", "channel", "new_oracle_commit", "round", roundID)
	_, err = s.db.ExecContext(ctx, "SELECT pg_notify('new_oracle_commit', $1)", fmt.Sprintf("%d", roundID))
	if err != nil {
		// Log but don't fail - notification is best-effort
		logger.Warnw("Failed to send NOTIFY", "error", err)
	} else {
		logger.Debugw("NOTIFY sent", "round", roundID)
	}

	return nil
}

// ListenForCommits listens for new oracle commits via PostgreSQL NOTIFY.
// Returns a channel that receives round IDs when new commits are inserted.
// The caller should handle reconnection on error.
func (s *PostgresStorage) ListenForCommits(ctx context.Context, connString string) (<-chan uint64, error) {
	// Create a dedicated listener connection
	listener := pq.NewListener(connString, 10*time.Second, time.Minute, func(ev pq.ListenerEventType, err error) {
		if err != nil {
			logger.Errorw("Listener event error", "error", err)
		}
		switch ev {
		case pq.ListenerEventConnected:
			logger.Debug("PostgreSQL listener connected")
		case pq.ListenerEventDisconnected:
			logger.Debug("PostgreSQL listener disconnected")
		case pq.ListenerEventReconnected:
			logger.Debug("PostgreSQL listener reconnected")
		case pq.ListenerEventConnectionAttemptFailed:
			logger.Warn("PostgreSQL listener connection attempt failed")
		}
	})

	if err := listener.Listen("new_oracle_commit"); err != nil {
		listener.Close()
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	logger.Debug("Subscribed to PostgreSQL channel: new_oracle_commit")

	roundChan := make(chan uint64, 10)

	go func() {
		defer close(roundChan)
		defer listener.Close()

		// Ping periodically to keep connection alive and detect failures
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Debug("Listener context cancelled")
				return

			case <-pingTicker.C:
				if err := listener.Ping(); err != nil {
					logger.Warnw("Listener ping failed", "error", err)
				}

			case notification := <-listener.Notify:
				if notification == nil {
					// Connection lost or reconnecting
					logger.Warn("Received nil notification (connection issue), waiting")
					continue
				}

				logger.Debugw("Received PostgreSQL notification",
					"channel", notification.Channel,
					"payload", notification.Extra)

				// Parse round ID from payload
				var roundID uint64
				if _, err := fmt.Sscanf(notification.Extra, "%d", &roundID); err != nil {
					logger.Warnw("Failed to parse round ID from payload",
						"payload", notification.Extra,
						"error", err)
					continue
				}

				select {
				case roundChan <- roundID:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return roundChan, nil
}

// GetCommitByRound returns a single oracle commit by round ID.
func (s *PostgresStorage) GetCommitByRound(ctx context.Context, roundID uint64) (*OracleCommit, error) {
	query := `
		SELECT round_id, target_epoch, merkle_root, reference_block
		FROM oracle_commits
		WHERE round_id = $1
		  AND tx_status = 'confirmed'
	`

	var c OracleCommit
	err := s.db.QueryRowContext(ctx, query, roundID).Scan(&c.RoundID, &c.TargetEpoch, &c.MerkleRoot, &c.ReferenceBlock)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get commit for round %d: %w", roundID, err)
	}

	return &c, nil
}

// GetCommitByReferenceBlock returns a single oracle commit by reference block number.
// Used by updater to look up targetEpoch from RootCommitted events.
func (s *PostgresStorage) GetCommitByReferenceBlock(ctx context.Context, blockNum uint64) (*OracleCommit, error) {
	query := `
		SELECT round_id, target_epoch, merkle_root, reference_block
		FROM oracle_commits
		WHERE reference_block = $1
		  AND tx_status = 'confirmed'
	`

	var c OracleCommit
	err := s.db.QueryRowContext(ctx, query, blockNum).Scan(&c.RoundID, &c.TargetEpoch, &c.MerkleRoot, &c.ReferenceBlock)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get commit for block %d: %w", blockNum, err)
	}

	return &c, nil
}

// ClearAllState removes all data from the database (for fresh start).
func (s *PostgresStorage) ClearAllState(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Clear all tables in reverse dependency order
	tables := []string{
		"oracle_commits",
		"validator_balances",
		"validator_events",
		"cluster_events",
		"cluster_state",
		"contract_events",
	}

	for _, table := range tables {
		_, err = tx.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		if err != nil {
			return fmt.Errorf("failed to truncate %s: %w", table, err)
		}
	}

	// Reset sync progress
	_, err = tx.ExecContext(ctx, `
		UPDATE sync_progress SET
			chain_id = NULL,
			last_synced_block = 0,
			updated_at = NOW()
		WHERE id = 1
	`)
	if err != nil {
		return fmt.Errorf("failed to reset sync progress: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	logger.Info("Database cleared")
	return nil
}

// BeginTx starts a new transaction.
func (s *PostgresStorage) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	return &postgresTx{tx: tx}, nil
}

// InsertEvent inserts a contract event.
func (s *PostgresStorage) InsertEvent(ctx context.Context, event *ContractEvent) error {
	query := `
		INSERT INTO contract_events (
			event_type, slot, block_number, block_hash, block_time,
			transaction_hash, transaction_index, log_index,
			cluster_id, raw_log, raw_event, error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (block_number, log_index) DO NOTHING
	`

	_, err := s.db.ExecContext(ctx, query,
		event.EventType,
		event.Slot,
		event.BlockNumber,
		event.BlockHash,
		event.BlockTime,
		event.TransactionHash,
		event.TransactionIndex,
		event.LogIndex,
		event.ClusterID,
		event.RawLog,
		event.RawEvent,
		event.Error,
	)

	if err != nil {
		return fmt.Errorf("failed to insert event: %w", err)
	}

	return nil
}

// GetLastSyncedBlock returns the last synced block number.
func (s *PostgresStorage) GetLastSyncedBlock(ctx context.Context) (uint64, error) {
	var blockNum uint64
	query := `SELECT last_synced_block FROM sync_progress WHERE id = 1`

	err := s.db.QueryRowContext(ctx, query).Scan(&blockNum)
	if err != nil {
		return 0, fmt.Errorf("failed to get last synced block: %w", err)
	}

	return blockNum, nil
}

// UpdateLastSyncedBlock updates the last synced block.
func (s *PostgresStorage) UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error {
	query := `
		UPDATE sync_progress
		SET last_synced_block = $1,
		    updated_at = NOW()
		WHERE id = 1
	`

	_, err := s.db.ExecContext(ctx, query, blockNum)
	if err != nil {
		return fmt.Errorf("failed to update last synced block: %w", err)
	}

	return nil
}

// InsertValidatorEvent inserts a new validator membership event.
func (s *PostgresStorage) InsertValidatorEvent(ctx context.Context, event *ValidatorEvent) error {
	// Validate pubkey length (must be 48 bytes for BLS public key)
	if len(event.ValidatorPubkey) != 48 {
		return fmt.Errorf("invalid validator pubkey length: got %d, expected 48", len(event.ValidatorPubkey))
	}

	query := `
		INSERT INTO validator_events (
			cluster_id, validator_pubkey, slot, log_index, is_active
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (cluster_id, validator_pubkey, slot, log_index) DO NOTHING
	`

	_, err := s.db.ExecContext(ctx, query,
		event.ClusterID,
		event.ValidatorPubkey,
		event.Slot,
		event.LogIndex,
		event.IsActive,
	)

	if err != nil {
		return fmt.Errorf("failed to insert validator event: %w", err)
	}

	return nil
}

// InsertClusterEvent inserts a new cluster operational status event.
func (s *PostgresStorage) InsertClusterEvent(ctx context.Context, event *ClusterEvent) error {
	query := `
		INSERT INTO cluster_events (
			cluster_id, slot, log_index, is_active
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (cluster_id, slot, log_index) DO NOTHING
	`

	_, err := s.db.ExecContext(ctx, query,
		event.ClusterID,
		event.Slot,
		event.LogIndex,
		event.IsActive,
	)

	if err != nil {
		return fmt.Errorf("failed to insert cluster event: %w", err)
	}

	return nil
}

// InsertValidatorBalance inserts or updates a validator balance snapshot.
func (s *PostgresStorage) InsertValidatorBalance(ctx context.Context, balance *ValidatorBalance) error {
	// Validate pubkey length (must be 48 bytes for BLS public key)
	if len(balance.ValidatorPubkey) != 48 {
		return fmt.Errorf("invalid validator pubkey length: got %d, expected 48", len(balance.ValidatorPubkey))
	}

	query := `
		INSERT INTO validator_balances (
			cluster_id, validator_pubkey, epoch, effective_balance
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (cluster_id, validator_pubkey, epoch) DO UPDATE
		SET effective_balance = EXCLUDED.effective_balance
	`

	_, err := s.db.ExecContext(ctx, query,
		balance.ClusterID,
		balance.ValidatorPubkey,
		balance.Epoch,
		balance.EffectiveBalance,
	)

	if err != nil {
		return fmt.Errorf("failed to insert validator balance: %w", err)
	}

	return nil
}

// UpsertClusterState inserts or updates cluster state.
func (s *PostgresStorage) UpsertClusterState(ctx context.Context, cluster *ClusterState) error {
	query := `
		INSERT INTO cluster_state (
			cluster_id, owner_address, operator_ids,
			validator_count, network_fee_index, index,
			is_active, balance, last_updated_slot
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (cluster_id) DO UPDATE
		SET owner_address = EXCLUDED.owner_address,
		    operator_ids = EXCLUDED.operator_ids,
		    validator_count = EXCLUDED.validator_count,
		    network_fee_index = EXCLUDED.network_fee_index,
		    index = EXCLUDED.index,
		    is_active = EXCLUDED.is_active,
		    balance = EXCLUDED.balance,
		    last_updated_slot = EXCLUDED.last_updated_slot
	`

	// Convert []uint64 to []int64 for PostgreSQL BIGINT[]
	operatorIDs := make([]int64, len(cluster.OperatorIDs))
	for i, id := range cluster.OperatorIDs {
		operatorIDs[i] = int64(id)
	}

	_, err := s.db.ExecContext(ctx, query,
		cluster.ClusterID,
		cluster.OwnerAddress,
		pq.Array(operatorIDs),
		cluster.ValidatorCount,
		cluster.NetworkFeeIndex,
		cluster.Index,
		cluster.IsActive,
		cluster.Balance.String(), // Store as string for NUMERIC(78,0)
		cluster.LastUpdatedSlot,
	)

	if err != nil {
		return fmt.Errorf("failed to upsert cluster state: %w", err)
	}

	return nil
}

// GetClusterState returns the cluster state for a given cluster ID.
// Returns nil if cluster is not found.
func (s *PostgresStorage) GetClusterState(ctx context.Context, clusterID []byte) (*ClusterState, error) {
	query := `
		SELECT cluster_id, owner_address, operator_ids,
		       validator_count, network_fee_index, index,
		       is_active, balance, last_updated_slot
		FROM cluster_state
		WHERE cluster_id = $1
	`

	var cluster ClusterState
	var operatorIDs []int64
	var balanceStr string

	err := s.db.QueryRowContext(ctx, query, clusterID).Scan(
		&cluster.ClusterID,
		&cluster.OwnerAddress,
		pq.Array(&operatorIDs),
		&cluster.ValidatorCount,
		&cluster.NetworkFeeIndex,
		&cluster.Index,
		&cluster.IsActive,
		&balanceStr,
		&cluster.LastUpdatedSlot,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to get cluster state: %w", err)
	}

	// Convert []int64 to []uint64
	cluster.OperatorIDs = make([]uint64, len(operatorIDs))
	for i, id := range operatorIDs {
		cluster.OperatorIDs[i] = uint64(id)
	}

	// Parse balance from string
	cluster.Balance = new(big.Int)
	if _, ok := cluster.Balance.SetString(balanceStr, 10); !ok {
		return nil, fmt.Errorf("failed to parse balance string: %q", balanceStr)
	}

	return &cluster, nil
}

// GetClusterBalances returns cluster balances for merkle tree at specific epoch.
func (s *PostgresStorage) GetClusterBalances(ctx context.Context, targetEpoch uint64, slotsPerEpoch uint64) ([]ClusterBalance, error) {
	query := `SELECT * FROM get_cluster_effective_balances($1, $2)`

	rows, err := s.db.QueryContext(ctx, query, targetEpoch, slotsPerEpoch)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster balances: %w", err)
	}
	defer rows.Close()

	var balances []ClusterBalance
	for rows.Next() {
		var b ClusterBalance
		if err := rows.Scan(&b.ClusterID, &b.TotalEffectiveBalance, &b.ValidatorCount); err != nil {
			return nil, fmt.Errorf("failed to scan cluster balance: %w", err)
		}
		balances = append(balances, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating cluster balances: %w", err)
	}

	return balances, nil
}

// GetActiveValidatorsWithClusters returns all active validators with their cluster IDs at a specific epoch.
func (s *PostgresStorage) GetActiveValidatorsWithClusters(ctx context.Context, atEpoch uint64, slotsPerEpoch uint64) ([]ActiveValidator, error) {
	query := `SELECT * FROM get_active_validators_with_clusters($1, $2)`

	rows, err := s.db.QueryContext(ctx, query, atEpoch, slotsPerEpoch)
	if err != nil {
		return nil, fmt.Errorf("failed to get active validators with clusters: %w", err)
	}
	defer rows.Close()

	var validators []ActiveValidator
	for rows.Next() {
		var v ActiveValidator
		if err := rows.Scan(&v.ClusterID, &v.ValidatorPubkey); err != nil {
			return nil, fmt.Errorf("failed to scan active validator: %w", err)
		}
		validators = append(validators, v)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating active validators: %w", err)
	}

	return validators, nil
}

// GetLatestValidatorBalances returns the most recent balance for each validator before the given epoch.
// Returns a map of "clusterID:pubkey" -> effective_balance.
// Used to determine if a balance has changed and needs to be stored.
func (s *PostgresStorage) GetLatestValidatorBalances(ctx context.Context, validators []ActiveValidator, beforeEpoch uint64) (map[string]uint64, error) {
	if len(validators) == 0 {
		return make(map[string]uint64), nil
	}

	// Build query with IN clause for all validators
	query := `
		SELECT DISTINCT ON (cluster_id, validator_pubkey)
			cluster_id, validator_pubkey, effective_balance
		FROM validator_balances
		WHERE epoch < $1
		ORDER BY cluster_id, validator_pubkey, epoch DESC
	`

	rows, err := s.db.QueryContext(ctx, query, beforeEpoch)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest validator balances: %w", err)
	}
	defer rows.Close()

	result := make(map[string]uint64)
	for rows.Next() {
		var clusterID, pubkey []byte
		var balance uint64
		if err := rows.Scan(&clusterID, &pubkey, &balance); err != nil {
			return nil, fmt.Errorf("failed to scan balance: %w", err)
		}
		key := fmt.Sprintf("%x:%x", clusterID, pubkey)
		result[key] = balance
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating balances: %w", err)
	}

	return result, nil
}

// IsReadyToCommit checks if all active validators have balances fetched for target epoch.
func (s *PostgresStorage) IsReadyToCommit(ctx context.Context, targetEpoch uint64, slotsPerEpoch uint64) (bool, error) {
	query := `SELECT is_ready_to_commit($1, $2)`

	var ready bool
	err := s.db.QueryRowContext(ctx, query, targetEpoch, slotsPerEpoch).Scan(&ready)
	if err != nil {
		return false, fmt.Errorf("failed to check if ready to commit: %w", err)
	}

	return ready, nil
}

// postgresTx implements Tx interface.
type postgresTx struct {
	tx *sql.Tx
}

func (t *postgresTx) Commit() error {
	return t.tx.Commit()
}

func (t *postgresTx) Rollback() error {
	return t.tx.Rollback()
}

func (t *postgresTx) InsertEvent(ctx context.Context, event *ContractEvent) error {
	query := `
		INSERT INTO contract_events (
			event_type, slot, block_number, block_hash, block_time,
			transaction_hash, transaction_index, log_index,
			cluster_id, raw_log, raw_event, error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (block_number, log_index) DO NOTHING
	`

	_, err := t.tx.ExecContext(ctx, query,
		event.EventType,
		event.Slot,
		event.BlockNumber,
		event.BlockHash,
		event.BlockTime,
		event.TransactionHash,
		event.TransactionIndex,
		event.LogIndex,
		event.ClusterID,
		event.RawLog,
		event.RawEvent,
		event.Error,
	)

	if err != nil {
		return fmt.Errorf("failed to insert event in tx: %w", err)
	}

	return nil
}

func (t *postgresTx) InsertValidatorEvent(ctx context.Context, event *ValidatorEvent) error {
	// Validate pubkey length (must be 48 bytes for BLS public key)
	if len(event.ValidatorPubkey) != 48 {
		return fmt.Errorf("invalid validator pubkey length: got %d, expected 48", len(event.ValidatorPubkey))
	}

	query := `
		INSERT INTO validator_events (
			cluster_id, validator_pubkey, slot, log_index, is_active
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (cluster_id, validator_pubkey, slot, log_index) DO NOTHING
	`

	_, err := t.tx.ExecContext(ctx, query,
		event.ClusterID,
		event.ValidatorPubkey,
		event.Slot,
		event.LogIndex,
		event.IsActive,
	)

	if err != nil {
		return fmt.Errorf("failed to insert validator event in tx: %w", err)
	}

	return nil
}

func (t *postgresTx) InsertClusterEvent(ctx context.Context, event *ClusterEvent) error {
	query := `
		INSERT INTO cluster_events (
			cluster_id, slot, log_index, is_active
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (cluster_id, slot, log_index) DO NOTHING
	`

	_, err := t.tx.ExecContext(ctx, query,
		event.ClusterID,
		event.Slot,
		event.LogIndex,
		event.IsActive,
	)

	if err != nil {
		return fmt.Errorf("failed to insert cluster event in tx: %w", err)
	}

	return nil
}

func (t *postgresTx) UpsertClusterState(ctx context.Context, cluster *ClusterState) error {
	query := `
		INSERT INTO cluster_state (
			cluster_id, owner_address, operator_ids,
			validator_count, network_fee_index, index,
			is_active, balance, last_updated_slot
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (cluster_id) DO UPDATE
		SET owner_address = EXCLUDED.owner_address,
		    operator_ids = EXCLUDED.operator_ids,
		    validator_count = EXCLUDED.validator_count,
		    network_fee_index = EXCLUDED.network_fee_index,
		    index = EXCLUDED.index,
		    is_active = EXCLUDED.is_active,
		    balance = EXCLUDED.balance,
		    last_updated_slot = EXCLUDED.last_updated_slot
	`

	// Convert []uint64 to []int64 for PostgreSQL BIGINT[]
	operatorIDs := make([]int64, len(cluster.OperatorIDs))
	for i, id := range cluster.OperatorIDs {
		operatorIDs[i] = int64(id)
	}

	_, err := t.tx.ExecContext(ctx, query,
		cluster.ClusterID,
		cluster.OwnerAddress,
		pq.Array(operatorIDs),
		cluster.ValidatorCount,
		cluster.NetworkFeeIndex,
		cluster.Index,
		cluster.IsActive,
		cluster.Balance.String(),
		cluster.LastUpdatedSlot,
	)

	if err != nil {
		return fmt.Errorf("failed to upsert cluster state in tx: %w", err)
	}

	return nil
}

func (t *postgresTx) UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error {
	query := `
		UPDATE sync_progress
		SET last_synced_block = $1,
		    updated_at = NOW()
		WHERE id = 1
	`

	_, err := t.tx.ExecContext(ctx, query, blockNum)
	if err != nil {
		return fmt.Errorf("failed to update last synced block in tx: %w", err)
	}

	return nil
}
