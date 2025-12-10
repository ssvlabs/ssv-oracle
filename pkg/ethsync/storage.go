package ethsync

import (
	"context"
	"database/sql"
	_ "embed" // for schema.sql
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	_ "modernc.org/sqlite" // register sqlite driver

	"ssv-oracle/pkg/logger"
)

// Tx defines the interface for database transactions.
type Tx interface {
	Commit() error
	Rollback() error
	InsertEvent(ctx context.Context, event *ContractEvent) error
	UpsertCluster(ctx context.Context, cluster *ClusterRow) error
	DeleteCluster(ctx context.Context, clusterID []byte) error
	InsertValidator(ctx context.Context, clusterID, pubkey []byte) error
	DeleteValidator(ctx context.Context, clusterID, pubkey []byte) error
	UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error
}

// executor abstracts *sql.DB and *sql.Tx for shared query implementations.
type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ContractEvent represents a stored SSV contract event.
type ContractEvent struct {
	EventType        string
	Slot             uint64
	BlockNumber      uint64
	BlockHash        []byte
	BlockTime        time.Time
	TransactionHash  []byte
	TransactionIndex uint32
	LogIndex         uint32
	ClusterID        []byte
	RawLog           json.RawMessage
	RawEvent         json.RawMessage
	Error            *string
}

// ClusterRow represents a cluster's current state in the database.
type ClusterRow struct {
	ClusterID       []byte
	OwnerAddress    []byte
	OperatorIDs     []uint64
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	IsActive        bool
	Balance         *big.Int
	LastUpdatedSlot uint64
}

// ActiveValidator represents an active validator with its cluster.
type ActiveValidator struct {
	ClusterID       []byte
	ValidatorPubkey []byte
}

// ClusterBalance represents a cluster's aggregated effective balance.
type ClusterBalance struct {
	ClusterID        []byte
	EffectiveBalance uint64
}

// CommitStatus represents the status of an oracle commit.
type CommitStatus string

const (
	CommitStatusPending   CommitStatus = "pending"
	CommitStatusConfirmed CommitStatus = "confirmed"
	CommitStatusFailed    CommitStatus = "failed"
)

// OracleCommit represents a stored oracle merkle root commit.
type OracleCommit struct {
	RoundID         uint64
	TargetEpoch     uint64
	MerkleRoot      []byte
	ReferenceBlock  uint64
	ClusterBalances []ClusterBalance
	Status          CommitStatus
	TxHash          []byte
}

// SQLite configuration constants.
const (
	sqliteBusyTimeoutMs = 5000  // Wait up to 5s for locks
	sqliteCacheSizeKB   = 64000 // 64MB page cache
	maxIdleConns        = 2
)

//go:embed schema.sql
var schemaSQL string

// Storage implements persistent storage using SQLite.
type Storage struct {
	db *sql.DB
}

// NewStorage creates a new SQLite storage and applies the schema.
func NewStorage(dbPath string) (*Storage, error) {
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// WAL mode for concurrent reads, NORMAL sync for durability without excessive fsync
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		fmt.Sprintf("PRAGMA busy_timeout=%d", sqliteBusyTimeoutMs),
		fmt.Sprintf("PRAGMA cache_size=-%d", sqliteCacheSizeKB),
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("failed to set %s: %w", pragma, err)
		}
	}

	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(time.Hour)

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}
	logger.Info("Database schema applied")

	return &Storage{db: db}, nil
}

// Close closes the database connection.
func (s *Storage) Close() error {
	return s.db.Close()
}

// GetChainID returns the stored chain ID, or nil if not set.
func (s *Storage) GetChainID(ctx context.Context) (*uint64, error) {
	var chainID *uint64
	err := s.db.QueryRowContext(ctx, `SELECT chain_id FROM sync_progress WHERE id = 1`).Scan(&chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}
	return chainID, nil
}

// SetChainID stores the chain ID.
func (s *Storage) SetChainID(ctx context.Context, chainID uint64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_progress SET chain_id = ?, updated_at = datetime('now') WHERE id = 1`, chainID)
	if err != nil {
		return fmt.Errorf("failed to set chain ID: %w", err)
	}
	return nil
}

// GetLastSyncedBlock returns the last synced block number.
func (s *Storage) GetLastSyncedBlock(ctx context.Context) (uint64, error) {
	var blockNum uint64
	err := s.db.QueryRowContext(ctx, `SELECT last_synced_block FROM sync_progress WHERE id = 1`).Scan(&blockNum)
	if err != nil {
		return 0, fmt.Errorf("failed to get last synced block: %w", err)
	}
	return blockNum, nil
}

// UpdateLastSyncedBlock updates the last synced block number.
func (s *Storage) UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error {
	return updateLastSyncedBlock(ctx, s.db, blockNum)
}

// InsertEvent stores a contract event.
func (s *Storage) InsertEvent(ctx context.Context, event *ContractEvent) error {
	return insertEvent(ctx, s.db, event)
}

// UpsertCluster creates or updates a cluster.
func (s *Storage) UpsertCluster(ctx context.Context, cluster *ClusterRow) error {
	return upsertCluster(ctx, s.db, cluster)
}

// DeleteCluster removes a cluster and its validators (cascade).
func (s *Storage) DeleteCluster(ctx context.Context, clusterID []byte) error {
	return deleteCluster(ctx, s.db, clusterID)
}

// GetCluster retrieves a cluster by ID, or nil if not found.
func (s *Storage) GetCluster(ctx context.Context, clusterID []byte) (*ClusterRow, error) {
	query := `
		SELECT cluster_id, owner_address, operator_ids, validator_count,
		       network_fee_index, idx, is_active, balance, last_updated_slot
		FROM clusters WHERE cluster_id = ?
	`
	var cluster ClusterRow
	var operatorIDsJSON string
	var balanceStr string
	var isActiveInt int

	err := s.db.QueryRowContext(ctx, query, clusterID).Scan(
		&cluster.ClusterID, &cluster.OwnerAddress, &operatorIDsJSON,
		&cluster.ValidatorCount, &cluster.NetworkFeeIndex, &cluster.Index,
		&isActiveInt, &balanceStr, &cluster.LastUpdatedSlot,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	operatorIDs, err := decodeOperatorIDs(operatorIDsJSON)
	if err != nil {
		return nil, err
	}
	cluster.OperatorIDs = operatorIDs
	cluster.IsActive = intToBool(isActiveInt)
	cluster.Balance = new(big.Int)
	if _, ok := cluster.Balance.SetString(balanceStr, 10); !ok {
		return nil, fmt.Errorf("invalid balance value: %s", balanceStr)
	}

	return &cluster, nil
}

// InsertValidator adds a validator to a cluster.
func (s *Storage) InsertValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return insertValidator(ctx, s.db, clusterID, pubkey)
}

// DeleteValidator removes a validator from a cluster.
func (s *Storage) DeleteValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return deleteValidator(ctx, s.db, clusterID, pubkey)
}

// GetActiveValidators returns all validators belonging to active clusters.
func (s *Storage) GetActiveValidators(ctx context.Context) ([]ActiveValidator, error) {
	query := `
		SELECT v.cluster_id, v.validator_pubkey
		FROM validators v
		JOIN clusters c ON c.cluster_id = v.cluster_id
		WHERE c.is_active = 1
		ORDER BY v.cluster_id, v.validator_pubkey
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get active validators: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var validators []ActiveValidator
	for rows.Next() {
		var v ActiveValidator
		if err := rows.Scan(&v.ClusterID, &v.ValidatorPubkey); err != nil {
			return nil, fmt.Errorf("failed to scan validator: %w", err)
		}
		validators = append(validators, v)
	}
	return validators, rows.Err()
}

// InsertPendingCommit stores a new oracle commit with pending status.
func (s *Storage) InsertPendingCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []ClusterBalance) error {
	balancesJSON, err := json.Marshal(clusterBalances)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster balances: %w", err)
	}

	query := `
		INSERT INTO oracle_commits (round_id, target_epoch, merkle_root, reference_block, cluster_balances, status)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (round_id) DO NOTHING
	`
	_, err = s.db.ExecContext(ctx, query, roundID, targetEpoch, merkleRoot, referenceBlock, balancesJSON, CommitStatusPending)
	if err != nil {
		return fmt.Errorf("failed to insert oracle commit: %w", err)
	}
	return nil
}

// UpdateCommitStatus updates the status and transaction hash of a commit.
func (s *Storage) UpdateCommitStatus(ctx context.Context, roundID uint64, status CommitStatus, txHash []byte) error {
	query := `UPDATE oracle_commits SET status = ?, tx_hash = ? WHERE round_id = ?`
	_, err := s.db.ExecContext(ctx, query, status, txHash, roundID)
	if err != nil {
		return fmt.Errorf("failed to update commit status: %w", err)
	}
	return nil
}

// GetCommitByBlock retrieves a commit by reference block, or nil if not found.
func (s *Storage) GetCommitByBlock(ctx context.Context, blockNum uint64) (*OracleCommit, error) {
	query := `
		SELECT round_id, target_epoch, merkle_root, reference_block, cluster_balances, status, tx_hash
		FROM oracle_commits WHERE reference_block = ?
	`
	var c OracleCommit
	var balancesJSON []byte
	var status string
	err := s.db.QueryRowContext(ctx, query, blockNum).Scan(&c.RoundID, &c.TargetEpoch, &c.MerkleRoot, &c.ReferenceBlock, &balancesJSON, &status, &c.TxHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}
	c.Status = CommitStatus(status)
	if balancesJSON != nil {
		if err := json.Unmarshal(balancesJSON, &c.ClusterBalances); err != nil {
			return nil, fmt.Errorf("failed to unmarshal cluster balances: %w", err)
		}
	}
	return &c, nil
}

// ClearAllState removes all data and resets sync progress.
func (s *Storage) ClearAllState(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Order matters: validators references clusters
	tables := []string{"oracle_commits", "validators", "clusters", "contract_events"}
	for _, table := range tables {
		if _, err = tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return fmt.Errorf("failed to clear %s: %w", table, err)
		}
	}

	_, err = tx.ExecContext(ctx, `UPDATE sync_progress SET chain_id = NULL, last_synced_block = 0, updated_at = datetime('now') WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("failed to reset sync progress: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	logger.Info("Database cleared")
	return nil
}

// BeginTx starts a new database transaction.
func (s *Storage) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	return &storageTx{tx: tx}, nil
}

// storageTx wraps sql.Tx to implement the Tx interface.
type storageTx struct {
	tx *sql.Tx
}

func (t *storageTx) Commit() error   { return t.tx.Commit() }
func (t *storageTx) Rollback() error { return t.tx.Rollback() }

func (t *storageTx) InsertEvent(ctx context.Context, event *ContractEvent) error {
	return insertEvent(ctx, t.tx, event)
}

func (t *storageTx) UpsertCluster(ctx context.Context, cluster *ClusterRow) error {
	return upsertCluster(ctx, t.tx, cluster)
}

func (t *storageTx) DeleteCluster(ctx context.Context, clusterID []byte) error {
	return deleteCluster(ctx, t.tx, clusterID)
}

func (t *storageTx) InsertValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return insertValidator(ctx, t.tx, clusterID, pubkey)
}

func (t *storageTx) DeleteValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return deleteValidator(ctx, t.tx, clusterID, pubkey)
}

func (t *storageTx) UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error {
	return updateLastSyncedBlock(ctx, t.tx, blockNum)
}

func insertEvent(ctx context.Context, e executor, event *ContractEvent) error {
	query := `
		INSERT INTO contract_events (
			block_number, log_index, event_type, slot, block_hash, block_time,
			transaction_hash, transaction_index, cluster_id, raw_log, raw_event, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (block_number, log_index) DO NOTHING
	`
	_, err := e.ExecContext(ctx, query,
		event.BlockNumber, event.LogIndex, event.EventType, event.Slot,
		event.BlockHash, event.BlockTime.Format(time.RFC3339), event.TransactionHash, event.TransactionIndex,
		event.ClusterID, string(event.RawLog), string(event.RawEvent), event.Error,
	)
	if err != nil {
		return fmt.Errorf("failed to insert event: %w", err)
	}
	return nil
}

func upsertCluster(ctx context.Context, e executor, cluster *ClusterRow) error {
	operatorIDsJSON, err := encodeOperatorIDs(cluster.OperatorIDs)
	if err != nil {
		return err
	}

	query := `
		INSERT INTO clusters (
			cluster_id, owner_address, operator_ids, validator_count,
			network_fee_index, idx, is_active, balance, last_updated_slot
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (cluster_id) DO UPDATE SET
			owner_address = EXCLUDED.owner_address,
			operator_ids = EXCLUDED.operator_ids,
			validator_count = EXCLUDED.validator_count,
			network_fee_index = EXCLUDED.network_fee_index,
			idx = EXCLUDED.idx,
			is_active = EXCLUDED.is_active,
			balance = EXCLUDED.balance,
			last_updated_slot = EXCLUDED.last_updated_slot
	`
	_, err = e.ExecContext(ctx, query,
		cluster.ClusterID, cluster.OwnerAddress, operatorIDsJSON,
		cluster.ValidatorCount, cluster.NetworkFeeIndex, cluster.Index,
		boolToInt(cluster.IsActive), cluster.Balance.String(), cluster.LastUpdatedSlot,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert cluster: %w", err)
	}
	return nil
}

func deleteCluster(ctx context.Context, e executor, clusterID []byte) error {
	_, err := e.ExecContext(ctx, `DELETE FROM clusters WHERE cluster_id = ?`, clusterID)
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}
	return nil
}

func insertValidator(ctx context.Context, e executor, clusterID, pubkey []byte) error {
	if len(pubkey) != phase0.PublicKeyLength {
		return fmt.Errorf("invalid validator pubkey length: got %d, expected %d", len(pubkey), phase0.PublicKeyLength)
	}
	_, err := e.ExecContext(ctx,
		`INSERT INTO validators (cluster_id, validator_pubkey) VALUES (?, ?) ON CONFLICT DO NOTHING`,
		clusterID, pubkey,
	)
	if err != nil {
		return fmt.Errorf("failed to insert validator: %w", err)
	}
	return nil
}

func deleteValidator(ctx context.Context, e executor, clusterID, pubkey []byte) error {
	_, err := e.ExecContext(ctx,
		`DELETE FROM validators WHERE cluster_id = ? AND validator_pubkey = ?`,
		clusterID, pubkey,
	)
	if err != nil {
		return fmt.Errorf("failed to delete validator: %w", err)
	}
	return nil
}

func updateLastSyncedBlock(ctx context.Context, e executor, blockNum uint64) error {
	_, err := e.ExecContext(ctx, `UPDATE sync_progress SET last_synced_block = ?, updated_at = datetime('now') WHERE id = 1`, blockNum)
	if err != nil {
		return fmt.Errorf("failed to update last synced block: %w", err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(i int) bool {
	return i != 0
}

func encodeOperatorIDs(ids []uint64) (string, error) {
	data, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("failed to encode operator IDs: %w", err)
	}
	return string(data), nil
}

func decodeOperatorIDs(data string) ([]uint64, error) {
	var ids []uint64
	if err := json.Unmarshal([]byte(data), &ids); err != nil {
		return nil, fmt.Errorf("failed to decode operator IDs: %w", err)
	}
	return ids, nil
}
