package storage

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

	"ssv-oracle/logger"
)

const (
	sqliteBusyTimeoutMs = 5000      // Wait up to 5s for locks
	sqliteCacheSizeKB   = 64000     // 64MB page cache
	sqliteMmapSize      = 256 << 20 // 256MB memory-mapped I/O for bulk sync
)

//go:embed schema.sql
var schemaSQL string

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
	BlockNumber      uint64
	BlockHash        []byte
	BlockTime        time.Time
	TransactionHash  []byte
	TransactionIndex uint32
	LogIndex         uint32
	ClusterID        []byte
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
}

// ActiveValidator represents an active validator with its cluster.
type ActiveValidator struct {
	ClusterID       []byte
	ValidatorPubkey []byte
}

// ClusterBalance represents a cluster's aggregated effective balance.
type ClusterBalance struct {
	ClusterID        []byte
	EffectiveBalance uint32
}

// CommitStatus represents the status of an oracle commit.
type CommitStatus string

const (
	// CommitStatusPending indicates a commit awaiting confirmation.
	CommitStatusPending CommitStatus = "pending"
	// CommitStatusConfirmed indicates a confirmed commit.
	CommitStatusConfirmed CommitStatus = "confirmed"
	// CommitStatusFailed indicates a failed commit.
	CommitStatusFailed CommitStatus = "failed"
)

// OracleCommit represents a stored oracle merkle root commit.
type OracleCommit struct {
	TargetEpoch     uint64
	MerkleRoot      []byte
	ReferenceBlock  uint64
	ClusterBalances []ClusterBalance
	Status          CommitStatus
	TxHash          []byte
}

// Storage implements persistent storage using SQLite.
type Storage struct {
	db *sql.DB
}

// New creates a new SQLite storage and applies the schema.
func New(dbPath string) (*Storage, error) {
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	// Use DSN pragmas for per-connection settings (foreign_keys, busy_timeout, cache_size)
	// These are applied to every connection from the pool automatically
	dsn := fmt.Sprintf("%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(%d)&_pragma=cache_size(-%d)",
		dbPath, sqliteBusyTimeoutMs, sqliteCacheSizeKB)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Database-level pragmas (only need to be set once, not per-connection)
	// WAL mode for concurrent reads, NORMAL sync for durability without excessive fsync
	dbPragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
	}
	for _, pragma := range dbPragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("set %s: %w", pragma, err)
		}
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
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
		return nil, fmt.Errorf("get chain ID: %w", err)
	}
	return chainID, nil
}

// SetChainID stores the chain ID.
func (s *Storage) SetChainID(ctx context.Context, chainID uint64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_progress SET chain_id = ?, updated_at = datetime('now') WHERE id = 1`, chainID)
	if err != nil {
		return fmt.Errorf("set chain ID: %w", err)
	}
	return nil
}

// GetLastSyncedBlock returns the last synced block number.
func (s *Storage) GetLastSyncedBlock(ctx context.Context) (uint64, error) {
	var blockNum uint64
	err := s.db.QueryRowContext(ctx, `SELECT last_synced_block FROM sync_progress WHERE id = 1`).Scan(&blockNum)
	if err != nil {
		return 0, fmt.Errorf("get last synced block: %w", err)
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
		       network_fee_index, idx, is_active, balance
		FROM clusters WHERE cluster_id = ?
	`
	var cluster ClusterRow
	var operatorIDsJSON string
	var balanceStr string
	var isActiveInt int

	err := s.db.QueryRowContext(ctx, query, clusterID).Scan(
		&cluster.ClusterID, &cluster.OwnerAddress, &operatorIDsJSON,
		&cluster.ValidatorCount, &cluster.NetworkFeeIndex, &cluster.Index,
		&isActiveInt, &balanceStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get cluster: %w", err)
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
		return nil, fmt.Errorf("query active validators: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var validators []ActiveValidator
	for rows.Next() {
		var v ActiveValidator
		if err := rows.Scan(&v.ClusterID, &v.ValidatorPubkey); err != nil {
			return nil, fmt.Errorf("scan validator row: %w", err)
		}
		validators = append(validators, v)
	}
	return validators, rows.Err()
}

// InsertPendingCommit stores a new oracle commit with pending status.
func (s *Storage) InsertPendingCommit(ctx context.Context, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []ClusterBalance) error {
	balancesJSON, err := json.Marshal(clusterBalances)
	if err != nil {
		return fmt.Errorf("marshal cluster balances: %w", err)
	}

	query := `
		INSERT INTO oracle_commits (target_epoch, merkle_root, reference_block, cluster_balances, status)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (target_epoch) DO NOTHING
	`
	result, err := s.db.ExecContext(ctx, query, targetEpoch, merkleRoot, referenceBlock, balancesJSON, CommitStatusPending)
	if err != nil {
		return fmt.Errorf("insert oracle commit: %w", err)
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		logger.Warnw("Duplicate commit ignored", "targetEpoch", targetEpoch)
	}
	return nil
}

// UpdateCommitStatus updates status only if current status is 'pending'.
// Already-finalized rows (confirmed/failed) are silently skipped to prevent clobbering.
func (s *Storage) UpdateCommitStatus(ctx context.Context, targetEpoch uint64, status CommitStatus, txHash []byte) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE oracle_commits
		SET status = ?, tx_hash = COALESCE(?, tx_hash)
		WHERE target_epoch = ? AND status = 'pending'
	`, status, txHash, targetEpoch)
	if err != nil {
		return fmt.Errorf("update commit status: %w", err)
	}
	return nil
}

// GetCommitByBlock retrieves a commit by reference block, or nil if not found.
func (s *Storage) GetCommitByBlock(ctx context.Context, blockNum uint64) (*OracleCommit, error) {
	query := `
		SELECT target_epoch, merkle_root, reference_block, cluster_balances, status, tx_hash
		FROM oracle_commits WHERE reference_block = ?
	`
	var c OracleCommit
	var balancesJSON []byte
	var status string
	err := s.db.QueryRowContext(ctx, query, blockNum).Scan(&c.TargetEpoch, &c.MerkleRoot, &c.ReferenceBlock, &balancesJSON, &status, &c.TxHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get commit: %w", err)
	}
	c.Status = CommitStatus(status)
	if balancesJSON != nil {
		if err := json.Unmarshal(balancesJSON, &c.ClusterBalances); err != nil {
			return nil, fmt.Errorf("unmarshal cluster balances: %w", err)
		}
	}
	return &c, nil
}

// GetLatestCommit returns the most recent confirmed commit with cluster balances.
// Returns nil if no confirmed commit exists or if cluster_balances is missing.
func (s *Storage) GetLatestCommit(ctx context.Context) (*OracleCommit, error) {
	query := `
		SELECT target_epoch, merkle_root, reference_block, cluster_balances, status, tx_hash
		FROM oracle_commits
		WHERE status = ? AND cluster_balances IS NOT NULL
		ORDER BY target_epoch DESC
		LIMIT 1
	`
	var c OracleCommit
	var balancesJSON []byte
	var status string
	err := s.db.QueryRowContext(ctx, query, CommitStatusConfirmed).Scan(
		&c.TargetEpoch, &c.MerkleRoot, &c.ReferenceBlock, &balancesJSON, &status, &c.TxHash,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest commit: %w", err)
	}
	c.Status = CommitStatus(status)
	if err := json.Unmarshal(balancesJSON, &c.ClusterBalances); err != nil {
		return nil, fmt.Errorf("unmarshal cluster balances: %w", err)
	}
	return &c, nil
}

// ClearAllState removes all data and resets sync progress.
func (s *Storage) ClearAllState(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
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
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}

	_, err = tx.ExecContext(ctx, `UPDATE sync_progress SET chain_id = NULL, last_synced_block = 0, updated_at = datetime('now') WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("reset sync progress: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	logger.Info("Database cleared")
	return nil
}

// BeginTx starts a new database transaction.
func (s *Storage) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return &storageTx{tx: tx}, nil
}

// storageTx wraps sql.Tx to implement the Tx interface.
type storageTx struct {
	tx *sql.Tx
}

// Commit commits the transaction.
func (t *storageTx) Commit() error { return t.tx.Commit() }

// Rollback rolls back the transaction.
func (t *storageTx) Rollback() error { return t.tx.Rollback() }

// InsertEvent inserts a contract event within the transaction.
func (t *storageTx) InsertEvent(ctx context.Context, event *ContractEvent) error {
	return insertEvent(ctx, t.tx, event)
}

// UpsertCluster inserts or updates a cluster within the transaction.
func (t *storageTx) UpsertCluster(ctx context.Context, cluster *ClusterRow) error {
	return upsertCluster(ctx, t.tx, cluster)
}

// DeleteCluster deletes a cluster within the transaction.
func (t *storageTx) DeleteCluster(ctx context.Context, clusterID []byte) error {
	return deleteCluster(ctx, t.tx, clusterID)
}

// InsertValidator inserts a validator within the transaction.
func (t *storageTx) InsertValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return insertValidator(ctx, t.tx, clusterID, pubkey)
}

// DeleteValidator deletes a validator within the transaction.
func (t *storageTx) DeleteValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return deleteValidator(ctx, t.tx, clusterID, pubkey)
}

// UpdateLastSyncedBlock updates sync progress within the transaction.
func (t *storageTx) UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error {
	return updateLastSyncedBlock(ctx, t.tx, blockNum)
}

func insertEvent(ctx context.Context, e executor, event *ContractEvent) error {
	query := `
		INSERT INTO contract_events (
			block_number, log_index, event_type, cluster_id,
			block_hash, block_time, transaction_hash, transaction_index, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (block_number, log_index) DO NOTHING
	`
	_, err := e.ExecContext(ctx, query,
		event.BlockNumber, event.LogIndex, event.EventType, event.ClusterID,
		event.BlockHash, event.BlockTime.Format(time.RFC3339), event.TransactionHash, event.TransactionIndex,
		event.Error,
	)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
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
			network_fee_index, idx, is_active, balance
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (cluster_id) DO UPDATE SET
			owner_address = EXCLUDED.owner_address,
			operator_ids = EXCLUDED.operator_ids,
			validator_count = EXCLUDED.validator_count,
			network_fee_index = EXCLUDED.network_fee_index,
			idx = EXCLUDED.idx,
			is_active = EXCLUDED.is_active,
			balance = EXCLUDED.balance
	`
	_, err = e.ExecContext(ctx, query,
		cluster.ClusterID, cluster.OwnerAddress, operatorIDsJSON,
		cluster.ValidatorCount, cluster.NetworkFeeIndex, cluster.Index,
		boolToInt(cluster.IsActive), cluster.Balance.String(),
	)
	if err != nil {
		return fmt.Errorf("upsert cluster: %w", err)
	}
	return nil
}

func deleteCluster(ctx context.Context, e executor, clusterID []byte) error {
	_, err := e.ExecContext(ctx, `DELETE FROM clusters WHERE cluster_id = ?`, clusterID)
	if err != nil {
		return fmt.Errorf("delete cluster: %w", err)
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
		return fmt.Errorf("insert validator: %w", err)
	}
	return nil
}

func deleteValidator(ctx context.Context, e executor, clusterID, pubkey []byte) error {
	_, err := e.ExecContext(ctx,
		`DELETE FROM validators WHERE cluster_id = ? AND validator_pubkey = ?`,
		clusterID, pubkey,
	)
	if err != nil {
		return fmt.Errorf("delete validator: %w", err)
	}
	return nil
}

func updateLastSyncedBlock(ctx context.Context, e executor, blockNum uint64) error {
	_, err := e.ExecContext(ctx, `UPDATE sync_progress SET last_synced_block = ?, updated_at = datetime('now') WHERE id = 1`, blockNum)
	if err != nil {
		return fmt.Errorf("update last synced block: %w", err)
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
		return "", fmt.Errorf("encode operator IDs: %w", err)
	}
	return string(data), nil
}

func decodeOperatorIDs(data string) ([]uint64, error) {
	var ids []uint64
	if err := json.Unmarshal([]byte(data), &ids); err != nil {
		return nil, fmt.Errorf("decode operator IDs: %w", err)
	}
	return ids, nil
}

// SetSyncMode configures SQLite for bulk writes or normal operation.
func (s *Storage) SetSyncMode(bulk bool) error {
	if bulk {
		_, err := s.db.Exec(fmt.Sprintf(`
			PRAGMA temp_store = MEMORY;
			PRAGMA mmap_size = %d;
		`, sqliteMmapSize))
		return err
	}
	_, err := s.db.Exec(`
		PRAGMA temp_store = DEFAULT;
		PRAGMA mmap_size = 0;
	`)
	return err
}

// UpdateClusterIfExists updates cluster data only if the cluster exists.
// Used by head sync to update cluster state without creating new clusters.
func (s *Storage) UpdateClusterIfExists(ctx context.Context, cluster *ClusterRow) error {
	query := `
		UPDATE clusters SET
			network_fee_index = ?,
			idx = ?,
			is_active = ?,
			balance = ?
		WHERE cluster_id = ?
	`
	_, err := s.db.ExecContext(ctx, query,
		cluster.NetworkFeeIndex,
		cluster.Index,
		boolToInt(cluster.IsActive),
		cluster.Balance.String(),
		cluster.ClusterID,
	)
	if err != nil {
		return fmt.Errorf("update cluster: %w", err)
	}
	return nil
}
