-- SSV Oracle Event Syncing Database Schema
--
-- DESIGN PHILOSOPHY:
-- - Store only what oracle needs (not SSV protocol internals)
-- - contract_events is source of truth (never delete)
-- - Keep ALL history (no cleanup) for debugging and testing
-- - Can rebuild from contract_events if needed
--
-- KEY TABLES:
-- - contract_events: Raw events from chain (source of truth)
-- - validator_events: Validator membership changes (Added/Removed)
-- - cluster_events: Cluster operational status changes (Liquidated/Reactivated)
-- - validator_balances: Effective balance snapshots from beacon chain
-- - cluster_state: Current cluster metadata

-- ============================================================================
-- CONTRACT EVENTS (Source of Truth - NEVER DELETE)
-- ============================================================================
-- Stores raw Ethereum logs + parsed events
-- cluster_id is computed from owner + operatorIds and stored for query efficiency

CREATE TABLE IF NOT EXISTS contract_events (
    id BIGSERIAL PRIMARY KEY,

    -- Event identification
    event_type VARCHAR(50) NOT NULL,
    slot BIGINT NOT NULL,
    block_number BIGINT NOT NULL,
    block_hash BYTEA NOT NULL,
    block_time TIMESTAMP NOT NULL,
    transaction_hash BYTEA NOT NULL,
    transaction_index INTEGER NOT NULL,
    log_index INTEGER NOT NULL,

    -- Computed fields (denormalized for query efficiency)
    cluster_id BYTEA,  -- Computed from owner + operatorIds (NULL for unknown/error events)

    -- Event data
    raw_log JSONB NOT NULL,    -- Raw Ethereum log (topics + data)
    raw_event JSONB NOT NULL,  -- Parsed event (owner, operatorIds, cluster, etc.)

    -- Processing
    error TEXT,  -- Parsing errors (if any)

    UNIQUE (block_number, log_index)
);

CREATE INDEX IF NOT EXISTS idx_events_block ON contract_events(block_number);
CREATE INDEX IF NOT EXISTS idx_events_slot ON contract_events(slot);
CREATE INDEX IF NOT EXISTS idx_events_type ON contract_events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_tx ON contract_events(transaction_hash);
CREATE INDEX IF NOT EXISTS idx_events_cluster ON contract_events(cluster_id);

-- ============================================================================
-- VALIDATOR EVENTS (Membership Changes)
-- ============================================================================
-- Tracks validator membership in clusters: Added or Removed
-- Key: (cluster_id, validator_pubkey, slot, log_index) for 100% correctness
-- Using slot (not block_number) because: epoch = slot / slots_per_epoch, enabling epoch-based queries
-- log_index is unique within a block, and each slot has at most one block

CREATE TABLE IF NOT EXISTS validator_events (
    cluster_id BYTEA NOT NULL,
    validator_pubkey BYTEA NOT NULL,
    slot BIGINT NOT NULL,
    log_index INTEGER NOT NULL,

    is_active BOOLEAN NOT NULL,  -- true = Added, false = Removed

    PRIMARY KEY (cluster_id, validator_pubkey, slot, log_index)
);

-- Index for "get all events for a cluster"
CREATE INDEX IF NOT EXISTS idx_validator_events_cluster ON validator_events(cluster_id);
-- Index for "get all events for a validator pubkey"
CREATE INDEX IF NOT EXISTS idx_validator_events_pubkey ON validator_events(validator_pubkey);
-- Index for "get all events up to slot X" (range queries)
CREATE INDEX IF NOT EXISTS idx_validator_events_slot ON validator_events(slot);

-- ============================================================================
-- CLUSTER EVENTS (Operational Status Changes)
-- ============================================================================
-- Tracks cluster operational status: Liquidated or Reactivated
-- Key: (cluster_id, slot, log_index) for 100% correctness
-- Using slot (not block_number) because: epoch = slot / slots_per_epoch, enabling epoch-based queries
-- If no record exists for a cluster, it's considered active (never been liquidated)

CREATE TABLE IF NOT EXISTS cluster_events (
    cluster_id BYTEA NOT NULL,
    slot BIGINT NOT NULL,
    log_index INTEGER NOT NULL,

    is_active BOOLEAN NOT NULL,  -- false = Liquidated, true = Reactivated

    PRIMARY KEY (cluster_id, slot, log_index)
);

-- Index for "get all events for a cluster"
CREATE INDEX IF NOT EXISTS idx_cluster_events_cluster ON cluster_events(cluster_id);
-- Index for "get all events up to slot X" (range queries)
CREATE INDEX IF NOT EXISTS idx_cluster_events_slot ON cluster_events(slot);

-- ============================================================================
-- VALIDATOR BALANCES (Balance Snapshots)
-- ============================================================================
-- Stores effective balance snapshots from beacon chain
-- Key: (cluster_id, validator_pubkey, epoch) - one balance per epoch
-- Only inserted when balance changes (optimization)

CREATE TABLE IF NOT EXISTS validator_balances (
    cluster_id BYTEA NOT NULL,
    validator_pubkey BYTEA NOT NULL,
    epoch BIGINT NOT NULL,

    effective_balance BIGINT NOT NULL,  -- In Gwei

    PRIMARY KEY (cluster_id, validator_pubkey, epoch)
);

CREATE INDEX IF NOT EXISTS idx_validator_balances_cluster ON validator_balances(cluster_id);
CREATE INDEX IF NOT EXISTS idx_validator_balances_pubkey ON validator_balances(validator_pubkey);
CREATE INDEX IF NOT EXISTS idx_validator_balances_epoch ON validator_balances(epoch);

-- ============================================================================
-- CLUSTER STATE (Current Metadata - Updated in Place)
-- ============================================================================
-- Stores current cluster metadata from SSV contract
-- Used for reference/debugging, NOT for historical queries

CREATE TABLE IF NOT EXISTS cluster_state (
    cluster_id BYTEA PRIMARY KEY,

    -- Static metadata (denormalized for query convenience)
    owner_address BYTEA NOT NULL,
    operator_ids BIGINT[] NOT NULL,

    -- Full Cluster struct from SSV contract events
    validator_count INTEGER NOT NULL,
    network_fee_index BIGINT NOT NULL,
    index BIGINT NOT NULL,
    is_active BOOLEAN NOT NULL,  -- Current status (for quick reference)
    balance NUMERIC(78,0) NOT NULL,  -- SSV token balance (uint256)

    -- Tracking
    last_updated_slot BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cluster_state_owner ON cluster_state(owner_address);

-- ============================================================================
-- SYNC PROGRESS
-- ============================================================================

CREATE TABLE IF NOT EXISTS sync_progress (
    id INTEGER PRIMARY KEY DEFAULT 1,
    chain_id BIGINT,  -- Network chain ID (for validation against accidental network changes)
    last_synced_block BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),

    CHECK (id = 1)
);

INSERT INTO sync_progress (id, last_synced_block) VALUES (1, 0) ON CONFLICT DO NOTHING;

-- ============================================================================
-- ORACLE COMMITS
-- ============================================================================

CREATE TABLE IF NOT EXISTS oracle_commits (
    round_id BIGINT PRIMARY KEY,
    target_epoch BIGINT NOT NULL,
    merkle_root BYTEA NOT NULL,
    reference_block BIGINT NOT NULL,

    tx_hash BYTEA,
    tx_status VARCHAR(20),
    submitted_at TIMESTAMP NOT NULL DEFAULT NOW(),
    confirmed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_commits_epoch ON oracle_commits(target_epoch);
CREATE INDEX IF NOT EXISTS idx_commits_status ON oracle_commits(tx_status);
CREATE INDEX IF NOT EXISTS idx_commits_ref_block ON oracle_commits(reference_block);

-- ============================================================================
-- QUERY FUNCTIONS
-- ============================================================================

-- Get cluster effective balances for merkle tree at specific epoch
-- Returns only clusters with at least 1 active validator
--
-- Parameters:
--   p_target_epoch: The epoch to evaluate state and get balances for
--   p_slots_per_epoch: Number of slots per epoch (from beacon spec)
--
CREATE OR REPLACE FUNCTION get_cluster_effective_balances(p_target_epoch BIGINT, p_slots_per_epoch BIGINT)
RETURNS TABLE (
    cluster_id BYTEA,
    total_effective_balance BIGINT,
    validator_count BIGINT
) AS $$
DECLARE
    v_last_slot BIGINT := (p_target_epoch + 1) * p_slots_per_epoch - 1;  -- Last slot of target epoch
BEGIN
    RETURN QUERY
    WITH latest_validator_event AS (
        -- Get latest is_active state for each validator at end of target epoch
        SELECT DISTINCT ON (ve.cluster_id, ve.validator_pubkey)
            ve.cluster_id,
            ve.validator_pubkey,
            ve.is_active
        FROM validator_events ve
        WHERE ve.slot <= v_last_slot
        ORDER BY ve.cluster_id, ve.validator_pubkey, ve.slot DESC, ve.log_index DESC
    ),
    latest_cluster_event AS (
        -- Get latest is_active state for each cluster at end of target epoch
        SELECT DISTINCT ON (ce.cluster_id)
            ce.cluster_id,
            ce.is_active
        FROM cluster_events ce
        WHERE ce.slot <= v_last_slot
        ORDER BY ce.cluster_id, ce.slot DESC, ce.log_index DESC
    ),
    latest_balance AS (
        -- Get latest balance for each validator at target epoch
        SELECT DISTINCT ON (vb.cluster_id, vb.validator_pubkey)
            vb.cluster_id,
            vb.validator_pubkey,
            vb.effective_balance
        FROM validator_balances vb
        WHERE vb.epoch <= p_target_epoch
        ORDER BY vb.cluster_id, vb.validator_pubkey, vb.epoch DESC
    )
    SELECT
        v.cluster_id,
        COALESCE(SUM(COALESCE(b.effective_balance, 0)), 0)::BIGINT as total_effective_balance,
        COUNT(*)::BIGINT as validator_count
    FROM latest_validator_event v
    LEFT JOIN latest_cluster_event c ON c.cluster_id = v.cluster_id
    LEFT JOIN latest_balance b ON b.cluster_id = v.cluster_id AND b.validator_pubkey = v.validator_pubkey
    WHERE v.is_active = true
      AND COALESCE(c.is_active, true) = true  -- Default to active if never liquidated
    GROUP BY v.cluster_id
    ORDER BY v.cluster_id;
END;
$$ LANGUAGE plpgsql STABLE;

-- Get all active validators with their cluster IDs (for balance storage)
-- Returns (cluster_id, validator_pubkey) pairs for all active validators
--
-- Parameters:
--   p_at_epoch: The epoch to evaluate state at
--   p_slots_per_epoch: Number of slots per epoch (from beacon spec)
--
CREATE OR REPLACE FUNCTION get_active_validators_with_clusters(p_at_epoch BIGINT, p_slots_per_epoch BIGINT)
RETURNS TABLE (
    cluster_id BYTEA,
    validator_pubkey BYTEA
) AS $$
DECLARE
    v_last_slot BIGINT := (p_at_epoch + 1) * p_slots_per_epoch - 1;  -- Last slot of target epoch
BEGIN
    RETURN QUERY
    WITH latest_validator_event AS (
        SELECT DISTINCT ON (ve.cluster_id, ve.validator_pubkey)
            ve.cluster_id,
            ve.validator_pubkey,
            ve.is_active
        FROM validator_events ve
        WHERE ve.slot <= v_last_slot
        ORDER BY ve.cluster_id, ve.validator_pubkey, ve.slot DESC, ve.log_index DESC
    ),
    latest_cluster_event AS (
        SELECT DISTINCT ON (ce.cluster_id)
            ce.cluster_id,
            ce.is_active
        FROM cluster_events ce
        WHERE ce.slot <= v_last_slot
        ORDER BY ce.cluster_id, ce.slot DESC, ce.log_index DESC
    )
    SELECT v.cluster_id, v.validator_pubkey
    FROM latest_validator_event v
    LEFT JOIN latest_cluster_event c ON c.cluster_id = v.cluster_id
    WHERE v.is_active = true
      AND COALESCE(c.is_active, true) = true
    ORDER BY v.cluster_id, v.validator_pubkey;
END;
$$ LANGUAGE plpgsql STABLE;

-- Check if ready to commit for target epoch
--
-- NOTE: With the optimization of not storing 0 balances for validators never on beacon,
-- missing balance records are now treated as 0 (implicit). This function now simply
-- checks that we have at least some active validators in the system.
--
-- Parameters:
--   p_target_epoch: The epoch to check readiness for
--   p_slots_per_epoch: Number of slots per epoch (from beacon spec)
--
CREATE OR REPLACE FUNCTION is_ready_to_commit(p_target_epoch BIGINT, p_slots_per_epoch BIGINT)
RETURNS BOOLEAN AS $$
DECLARE
    v_last_slot BIGINT := (p_target_epoch + 1) * p_slots_per_epoch - 1;  -- Last slot of target epoch
BEGIN
    -- Returns TRUE if there are any active validators (we can compute a merkle tree)
    -- Missing balance records are treated as 0, so no need to check for them
    RETURN EXISTS (
        WITH latest_validator_event AS (
            SELECT DISTINCT ON (ve.cluster_id, ve.validator_pubkey)
                ve.cluster_id,
                ve.validator_pubkey,
                ve.is_active
            FROM validator_events ve
            WHERE ve.slot <= v_last_slot
            ORDER BY ve.cluster_id, ve.validator_pubkey, ve.slot DESC, ve.log_index DESC
        ),
        latest_cluster_event AS (
            SELECT DISTINCT ON (ce.cluster_id)
                ce.cluster_id,
                ce.is_active
            FROM cluster_events ce
            WHERE ce.slot <= v_last_slot
            ORDER BY ce.cluster_id, ce.slot DESC, ce.log_index DESC
        )
        SELECT 1
        FROM latest_validator_event v
        LEFT JOIN latest_cluster_event c ON c.cluster_id = v.cluster_id
        WHERE v.is_active = true
          AND COALESCE(c.is_active, true) = true
    );
END;
$$ LANGUAGE plpgsql STABLE;

-- ============================================================================
-- NOTES
-- ============================================================================

-- TABLES OVERVIEW:
--
--   contract_events     - Raw events from chain (source of truth, never delete)
--   validator_events    - Validator membership: Added (is_active=true), Removed (is_active=false)
--   cluster_events      - Cluster status: Liquidated (is_active=false), Reactivated (is_active=true)
--   validator_balances  - Effective balance snapshots from beacon chain
--   cluster_state       - Current cluster metadata (for reference only)
--
-- EVENT PROCESSING:
--
--   ValidatorAdded:
--     1. INSERT into contract_events
--     2. INSERT into validator_events (is_active=true)
--     3. UPSERT cluster_state
--
--   ValidatorRemoved:
--     1. INSERT into contract_events
--     2. INSERT into validator_events (is_active=false)
--     3. UPSERT cluster_state
--
--   ClusterLiquidated:
--     1. INSERT into contract_events
--     2. INSERT into cluster_events (is_active=false)
--     3. UPSERT cluster_state
--
--   ClusterReactivated:
--     1. INSERT into contract_events
--     2. INSERT into cluster_events (is_active=true)
--     3. UPSERT cluster_state
--
--   ClusterDeposited/Withdrawn:
--     1. INSERT into contract_events
--     2. UPSERT cluster_state (balance changed)
--
-- BALANCE FETCHING:
--
--   1. Get active validators: SELECT * FROM get_active_validators_with_clusters(target_epoch, slots_per_epoch)
--   2. Fetch from beacon: POST /eth/v1/beacon/states/finalized/validators
--   3. For each validator with changed balance:
--      INSERT INTO validator_balances (cluster_id, validator_pubkey, epoch, effective_balance)
--
-- MERKLE ROOT COMPUTATION:
--
--   1. Check ready: SELECT is_ready_to_commit(target_epoch, slots_per_epoch)
--   2. Get balances: SELECT * FROM get_cluster_effective_balances(target_epoch, slots_per_epoch)
--   3. Build merkle tree from cluster balances
