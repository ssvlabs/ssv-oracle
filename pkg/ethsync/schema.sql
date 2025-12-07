-- Sync state: singleton row tracking chain ID and last synced block
CREATE TABLE IF NOT EXISTS sync_progress (
    id SMALLINT PRIMARY KEY DEFAULT 1,
    chain_id BIGINT,
    last_synced_block BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CHECK (id = 1)
);

INSERT INTO sync_progress (id, last_synced_block) VALUES (1, 0) ON CONFLICT DO NOTHING;

-- Raw SSV contract events for audit/debug (append-only)
CREATE TABLE IF NOT EXISTS contract_events (
    block_number BIGINT NOT NULL,
    log_index INT NOT NULL,
    event_type TEXT NOT NULL,
    slot BIGINT NOT NULL,
    block_hash BYTEA NOT NULL,
    block_time TIMESTAMP NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index INT NOT NULL,
    cluster_id BYTEA,
    raw_log JSONB NOT NULL,
    raw_event JSONB NOT NULL,
    error TEXT,
    PRIMARY KEY (block_number, log_index)
);

CREATE INDEX IF NOT EXISTS idx_contract_events_slot ON contract_events(slot);
CREATE INDEX IF NOT EXISTS idx_contract_events_cluster ON contract_events(cluster_id);

-- Current cluster state (upserted on each cluster event)
-- Deleted when validator_count reaches 0
CREATE TABLE IF NOT EXISTS clusters (
    cluster_id BYTEA PRIMARY KEY,
    owner_address BYTEA NOT NULL,
    operator_ids BIGINT[] NOT NULL,
    validator_count INT NOT NULL,
    network_fee_index BIGINT NOT NULL,
    index BIGINT NOT NULL,
    is_active BOOLEAN NOT NULL,
    balance NUMERIC(78,0) NOT NULL,
    last_updated_slot BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_clusters_active ON clusters(is_active) WHERE is_active = true;

-- Validator membership: INSERT on ValidatorAdded, DELETE on ValidatorRemoved
-- CASCADE delete when cluster is removed
CREATE TABLE IF NOT EXISTS validators (
    cluster_id BYTEA NOT NULL REFERENCES clusters(cluster_id) ON DELETE CASCADE,
    validator_pubkey BYTEA NOT NULL,
    PRIMARY KEY (cluster_id, validator_pubkey)
);

CREATE INDEX IF NOT EXISTS idx_validators_cluster ON validators(cluster_id);

-- Oracle commit history with cluster balances for merkle tree reconstruction
CREATE TABLE IF NOT EXISTS oracle_commits (
    round_id BIGINT PRIMARY KEY,
    target_epoch BIGINT NOT NULL,
    merkle_root BYTEA NOT NULL,
    reference_block BIGINT NOT NULL,
    cluster_balances JSONB,
    tx_hash BYTEA NOT NULL,
    committed_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oracle_commits_epoch ON oracle_commits(target_epoch);
CREATE INDEX IF NOT EXISTS idx_oracle_commits_ref_block ON oracle_commits(reference_block);
