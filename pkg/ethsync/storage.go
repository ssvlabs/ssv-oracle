package ethsync

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/lib/pq"

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

// executor is the common interface for *sql.DB and *sql.Tx
type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

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

type ActiveValidator struct {
	ClusterID       []byte
	ValidatorPubkey []byte
}

type ClusterBalance struct {
	ClusterID        []byte
	EffectiveBalance uint64
}

type CommitStatus string

const (
	CommitStatusPending   CommitStatus = "pending"
	CommitStatusConfirmed CommitStatus = "confirmed"
	CommitStatusFailed    CommitStatus = "failed"
)

type OracleCommit struct {
	RoundID         uint64
	TargetEpoch     uint64
	MerkleRoot      []byte
	ReferenceBlock  uint64
	ClusterBalances []ClusterBalance
	Status          CommitStatus
	TxHash          []byte
}

type PostgresStorage struct {
	db *sql.DB
}

//go:embed schema.sql
var schemaSQL string

func NewPostgresStorage(connString string) (*PostgresStorage, error) {
	db, err := sql.Open("postgres", connString)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to apply schema: %w", err)
	}
	logger.Info("Database schema applied")

	return &PostgresStorage{db: db}, nil
}

func (s *PostgresStorage) Close() error {
	return s.db.Close()
}

func (s *PostgresStorage) GetChainID(ctx context.Context) (*uint64, error) {
	var chainID *uint64
	err := s.db.QueryRowContext(ctx, `SELECT chain_id FROM sync_progress WHERE id = 1`).Scan(&chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}
	return chainID, nil
}

func (s *PostgresStorage) SetChainID(ctx context.Context, chainID uint64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sync_progress SET chain_id = $1, updated_at = NOW() WHERE id = 1`, chainID)
	if err != nil {
		return fmt.Errorf("failed to set chain ID: %w", err)
	}
	return nil
}

func (s *PostgresStorage) GetLastSyncedBlock(ctx context.Context) (uint64, error) {
	var blockNum uint64
	err := s.db.QueryRowContext(ctx, `SELECT last_synced_block FROM sync_progress WHERE id = 1`).Scan(&blockNum)
	if err != nil {
		return 0, fmt.Errorf("failed to get last synced block: %w", err)
	}
	return blockNum, nil
}

func (s *PostgresStorage) UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error {
	return updateLastSyncedBlock(ctx, s.db, blockNum)
}

func (s *PostgresStorage) InsertEvent(ctx context.Context, event *ContractEvent) error {
	return insertEvent(ctx, s.db, event)
}

func (s *PostgresStorage) UpsertCluster(ctx context.Context, cluster *ClusterRow) error {
	return upsertCluster(ctx, s.db, cluster)
}

func (s *PostgresStorage) DeleteCluster(ctx context.Context, clusterID []byte) error {
	return deleteCluster(ctx, s.db, clusterID)
}

func (s *PostgresStorage) GetCluster(ctx context.Context, clusterID []byte) (*ClusterRow, error) {
	query := `
		SELECT cluster_id, owner_address, operator_ids, validator_count,
		       network_fee_index, index, is_active, balance, last_updated_slot
		FROM clusters WHERE cluster_id = $1
	`
	var cluster ClusterRow
	var operatorIDs []int64
	var balanceStr string

	err := s.db.QueryRowContext(ctx, query, clusterID).Scan(
		&cluster.ClusterID, &cluster.OwnerAddress, pq.Array(&operatorIDs),
		&cluster.ValidatorCount, &cluster.NetworkFeeIndex, &cluster.Index,
		&cluster.IsActive, &balanceStr, &cluster.LastUpdatedSlot,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	cluster.OperatorIDs = make([]uint64, len(operatorIDs))
	for i, id := range operatorIDs {
		cluster.OperatorIDs[i] = uint64(id)
	}
	cluster.Balance = new(big.Int)
	if _, ok := cluster.Balance.SetString(balanceStr, 10); !ok {
		return nil, fmt.Errorf("invalid balance value: %s", balanceStr)
	}

	return &cluster, nil
}

func (s *PostgresStorage) InsertValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return insertValidator(ctx, s.db, clusterID, pubkey)
}

func (s *PostgresStorage) DeleteValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return deleteValidator(ctx, s.db, clusterID, pubkey)
}

func (s *PostgresStorage) GetActiveValidators(ctx context.Context) ([]ActiveValidator, error) {
	query := `
		SELECT v.cluster_id, v.validator_pubkey
		FROM validators v
		JOIN clusters c ON c.cluster_id = v.cluster_id
		WHERE c.is_active = true
		ORDER BY v.cluster_id, v.validator_pubkey
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get active validators: %w", err)
	}
	defer rows.Close()

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

func (s *PostgresStorage) InsertPendingCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []ClusterBalance) error {
	balancesJSON, err := json.Marshal(clusterBalances)
	if err != nil {
		return fmt.Errorf("failed to marshal cluster balances: %w", err)
	}

	query := `
		INSERT INTO oracle_commits (round_id, target_epoch, merkle_root, reference_block, cluster_balances, status)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err = s.db.ExecContext(ctx, query, roundID, targetEpoch, merkleRoot, referenceBlock, balancesJSON, CommitStatusPending)
	if err != nil {
		return fmt.Errorf("failed to insert oracle commit: %w", err)
	}
	return nil
}

func (s *PostgresStorage) UpdateCommitStatus(ctx context.Context, roundID uint64, status CommitStatus, txHash []byte) error {
	query := `UPDATE oracle_commits SET status = $1, tx_hash = $2 WHERE round_id = $3`
	_, err := s.db.ExecContext(ctx, query, status, txHash, roundID)
	if err != nil {
		return fmt.Errorf("failed to update commit status: %w", err)
	}
	return nil
}

func (s *PostgresStorage) GetCommitByBlock(ctx context.Context, blockNum uint64) (*OracleCommit, error) {
	query := `
		SELECT round_id, target_epoch, merkle_root, reference_block, cluster_balances, status, tx_hash
		FROM oracle_commits WHERE reference_block = $1
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

	tables := []string{"oracle_commits", "validators", "clusters", "contract_events"}
	for _, table := range tables {
		if _, err = tx.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table)); err != nil {
			return fmt.Errorf("failed to truncate %s: %w", table, err)
		}
	}

	_, err = tx.ExecContext(ctx, `UPDATE sync_progress SET chain_id = NULL, last_synced_block = 0, updated_at = NOW() WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("failed to reset sync progress: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	logger.Info("Database cleared")
	return nil
}

func (s *PostgresStorage) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	return &postgresTx{tx: tx}, nil
}

// postgresTx wraps sql.Tx to implement Tx interface
type postgresTx struct {
	tx *sql.Tx
}

func (t *postgresTx) Commit() error   { return t.tx.Commit() }
func (t *postgresTx) Rollback() error { return t.tx.Rollback() }

func (t *postgresTx) InsertEvent(ctx context.Context, event *ContractEvent) error {
	return insertEvent(ctx, t.tx, event)
}

func (t *postgresTx) UpsertCluster(ctx context.Context, cluster *ClusterRow) error {
	return upsertCluster(ctx, t.tx, cluster)
}

func (t *postgresTx) DeleteCluster(ctx context.Context, clusterID []byte) error {
	return deleteCluster(ctx, t.tx, clusterID)
}

func (t *postgresTx) InsertValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return insertValidator(ctx, t.tx, clusterID, pubkey)
}

func (t *postgresTx) DeleteValidator(ctx context.Context, clusterID, pubkey []byte) error {
	return deleteValidator(ctx, t.tx, clusterID, pubkey)
}

func (t *postgresTx) UpdateLastSyncedBlock(ctx context.Context, blockNum uint64) error {
	return updateLastSyncedBlock(ctx, t.tx, blockNum)
}

// Shared implementations

func insertEvent(ctx context.Context, e executor, event *ContractEvent) error {
	query := `
		INSERT INTO contract_events (
			block_number, log_index, event_type, slot, block_hash, block_time,
			transaction_hash, transaction_index, cluster_id, raw_log, raw_event, error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (block_number, log_index) DO NOTHING
	`
	_, err := e.ExecContext(ctx, query,
		event.BlockNumber, event.LogIndex, event.EventType, event.Slot,
		event.BlockHash, event.BlockTime, event.TransactionHash, event.TransactionIndex,
		event.ClusterID, event.RawLog, event.RawEvent, event.Error,
	)
	if err != nil {
		return fmt.Errorf("failed to insert event: %w", err)
	}
	return nil
}

func upsertCluster(ctx context.Context, e executor, cluster *ClusterRow) error {
	query := `
		INSERT INTO clusters (
			cluster_id, owner_address, operator_ids, validator_count,
			network_fee_index, index, is_active, balance, last_updated_slot
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (cluster_id) DO UPDATE SET
			owner_address = EXCLUDED.owner_address,
			operator_ids = EXCLUDED.operator_ids,
			validator_count = EXCLUDED.validator_count,
			network_fee_index = EXCLUDED.network_fee_index,
			index = EXCLUDED.index,
			is_active = EXCLUDED.is_active,
			balance = EXCLUDED.balance,
			last_updated_slot = EXCLUDED.last_updated_slot
	`
	operatorIDs := make([]int64, len(cluster.OperatorIDs))
	for i, id := range cluster.OperatorIDs {
		operatorIDs[i] = int64(id)
	}
	_, err := e.ExecContext(ctx, query,
		cluster.ClusterID, cluster.OwnerAddress, pq.Array(operatorIDs),
		cluster.ValidatorCount, cluster.NetworkFeeIndex, cluster.Index,
		cluster.IsActive, cluster.Balance.String(), cluster.LastUpdatedSlot,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert cluster: %w", err)
	}
	return nil
}

func deleteCluster(ctx context.Context, e executor, clusterID []byte) error {
	_, err := e.ExecContext(ctx, `DELETE FROM clusters WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}
	return nil
}

func insertValidator(ctx context.Context, e executor, clusterID, pubkey []byte) error {
	if len(pubkey) != 48 {
		return fmt.Errorf("invalid validator pubkey length: got %d, expected 48", len(pubkey))
	}
	_, err := e.ExecContext(ctx,
		`INSERT INTO validators (cluster_id, validator_pubkey) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		clusterID, pubkey,
	)
	if err != nil {
		return fmt.Errorf("failed to insert validator: %w", err)
	}
	return nil
}

func deleteValidator(ctx context.Context, e executor, clusterID, pubkey []byte) error {
	_, err := e.ExecContext(ctx,
		`DELETE FROM validators WHERE cluster_id = $1 AND validator_pubkey = $2`,
		clusterID, pubkey,
	)
	if err != nil {
		return fmt.Errorf("failed to delete validator: %w", err)
	}
	return nil
}

func updateLastSyncedBlock(ctx context.Context, e executor, blockNum uint64) error {
	_, err := e.ExecContext(ctx, `UPDATE sync_progress SET last_synced_block = $1, updated_at = NOW() WHERE id = 1`, blockNum)
	if err != nil {
		return fmt.Errorf("failed to update last synced block: %w", err)
	}
	return nil
}
