-- Singleton row tracking sync state
CREATE TABLE IF NOT EXISTS sync_progress (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    chain_id INTEGER,
    last_synced_block INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT OR IGNORE INTO sync_progress (id, last_synced_block) VALUES (1, 0);

-- Raw SSV contract events (append-only audit log)
CREATE TABLE IF NOT EXISTS contract_events (
    block_number INTEGER NOT NULL,
    log_index INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    block_hash BLOB NOT NULL,
    block_time TEXT NOT NULL,
    transaction_hash BLOB NOT NULL,
    transaction_index INTEGER NOT NULL,
    raw_log TEXT NOT NULL,
    raw_event TEXT NOT NULL,
    error TEXT,
    PRIMARY KEY (block_number, log_index)
);

-- Current cluster state (upserted on events, deleted when validator_count = 0)
CREATE TABLE IF NOT EXISTS clusters (
    cluster_id BLOB PRIMARY KEY,
    owner_address BLOB NOT NULL,
    operator_ids TEXT NOT NULL,
    validator_count INTEGER NOT NULL,
    network_fee_index INTEGER NOT NULL,
    idx INTEGER NOT NULL,
    is_active INTEGER NOT NULL CHECK (is_active IN (0, 1)),
    balance TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_clusters_active ON clusters(is_active) WHERE is_active = 1;

-- Validator membership (cascade delete with cluster)
CREATE TABLE IF NOT EXISTS validators (
    cluster_id BLOB NOT NULL REFERENCES clusters(cluster_id) ON DELETE CASCADE,
    validator_pubkey BLOB NOT NULL,
    PRIMARY KEY (cluster_id, validator_pubkey)
);

CREATE INDEX IF NOT EXISTS idx_validators_cluster ON validators(cluster_id);

-- Oracle commit history for merkle proof reconstruction
CREATE TABLE IF NOT EXISTS oracle_commits (
    target_epoch INTEGER PRIMARY KEY,
    merkle_root BLOB NOT NULL,
    reference_block INTEGER NOT NULL UNIQUE,
    cluster_balances TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'failed')),
    tx_hash BLOB,
    committed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_oracle_commits_status ON oracle_commits(status);
