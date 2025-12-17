# SSV Oracle

Off-chain oracle client that publishes Merkle roots of SSV cluster effective balances to an on-chain oracle contract.

## Features

- **Event-sourced architecture** - Syncs SSV contract events to SQLite for point-in-time queries
- **Epoch-aligned timing** - Waits for epoch finalization before committing roots
- **OpenZeppelin-compatible Merkle trees** - Deterministic root computation with standardized sibling ordering
- **Beacon chain integration** - Fetches validator effective balances directly from consensus layer
- **Unified contract** - Uses the SSV Network contract itself (no separate oracle contract),
  with oracle functionality integrated directly
- **Single binary deployment** - SQLite database requires no external services

## Quick Start

### Prerequisites

- Go 1.25+
- Ethereum execution client (HTTP RPC, WebSocket for `--updater`)
- Beacon node (REST API endpoint)

### Installation

```bash
# Copy configuration templates
cp .env.example .env
cp config.yaml.example config.yaml

# Edit config.yaml with your endpoints
# - eth_rpc: Execution layer HTTP RPC
# - eth_ws_rpc: Execution layer WebSocket (required for --updater)
# - beacon_rpc: Beacon node API
# - ssv_contract: SSV Network contract address (includes oracle functionality)
# - ssv_views_contract: SSV Network Views contract (required for --updater)

# Load environment variables
source .env

# Start with fresh database
make fresh
```

## Usage

```bash
make              # Show available targets
make run          # Run oracle
make run-all      # Run oracle + cluster updater
make fresh        # Fresh start (reset DB)
make fresh-all    # Fresh start with updater
make test         # Run tests
make lint         # Run linters
make docker       # Build Docker image
make clean        # Remove build artifacts and database
make db-reset     # Remove SQLite database files
```

**CLI flags:**
```bash
./ssv-oracle run --config config.yaml            # Oracle only
./ssv-oracle run --config config.yaml --updater  # Oracle + updater
./ssv-oracle run --config config.yaml --fresh    # Clear DB and start fresh
```

## Configuration

Edit `config.yaml`:

```yaml
# Network
eth_rpc: "http://localhost:8545"
eth_ws_rpc: "ws://localhost:8546"  # Required for --updater
beacon_rpc: "http://localhost:5052"

# Contract
ssv_contract: "0x..."
ssv_views_contract: "0x..."  # Required for --updater (SSV Network Views contract)

# Syncing
sync_from_block: 17507487  # SSV contract deployment block (mainnet example)
sync_batch_size: 200

# Database (SQLite)
db_path: "./data/oracle.db"

# Wallet
wallet:
  type: "env"  # env | keystore
  private_key_env: "PRIVATE_KEY"
```

### Wallet Configuration

The oracle supports multiple signing backends:

| Type | Description | Use Case |
|------|-------------|----------|
| `env` | Private key from environment variable | Development |
| `keystore` | Encrypted keystore file | Production |

**Keystore example:**
```yaml
wallet:
  type: "keystore"
  keystore_path: "/path/to/UTC--...--keystore.json"
  password_env: "KEYSTORE_PASSWORD"   # or
  password_file: "/path/to/password.txt"
```

### Transaction Policy

The oracle includes automatic transaction management with gas optimization, retries, and cancellation:

```yaml
tx_policy:
  gas_buffer_percent: 20        # Add 20% to estimated gas
  max_fee_per_gas: "100 gwei"   # Never exceed this gas price
  pending_timeout_blocks: 10    # Blocks before bumping gas
  gas_bump_percent: 10          # Minimum 10% for replace-by-fee
  max_retries: 3                # Attempts before cancellation
  retry_delay: 5s               # Delay between retries
```

| Setting | Description |
|---------|-------------|
| `gas_buffer_percent` | Extra gas added to estimates to prevent out-of-gas errors |
| `max_fee_per_gas` | Hard cap on gas price (supports "gwei", "wei" suffixes) |
| `pending_timeout_blocks` | Blocks to wait before bumping a stuck transaction |
| `gas_bump_percent` | Percentage increase when bumping (EIP-1559 requires ≥10%) |
| `max_retries` | Maximum retry attempts before cancelling and giving up |
| `retry_delay` | Wait time between retry attempts |

**Automatic cancellation:** When max retries are exhausted or max gas price is reached, the oracle sends a 0-value self-transfer to free the nonce and prevent stuck transactions.

## Oracle Cycle

The oracle is event-driven, reacting to beacon chain finalization:

1. **Subscribe** - Connect to beacon node SSE for finalized checkpoint events
2. **On finalization** - When a new epoch finalizes, check if a target epoch is ready
3. **Sync events** - Fetch SSV contract events up to the finalized block
4. **Fetch balances** - Query beacon chain for validator effective balances
5. **Build Merkle tree** - Aggregate balances by cluster, construct tree
6. **Commit root** - Submit transaction with Merkle root and reference block

### Beacon Chain Finalization

Understanding Ethereum's finalization is critical for this codebase:

- **Finalized checkpoint epoch** = the epoch boundary checkpoint
  (slot = epoch × SLOTS_PER_EPOCH, even if the slot was missed)
- **Fully finalized epoch** = `checkpoint.Epoch - 1` (all slots in that epoch are finalized)

When the beacon node reports `checkpoint.Epoch = 100`, it means epoch 99 is fully finalized. The oracle uses `checkpoint.Epoch - 1` to determine which target epochs can be committed.

```
Checkpoint epoch: 100  →  Fully finalized: 99  →  Can commit targets ≤ 99
```

## Cluster Updater

The updater runs alongside the oracle (enabled with `--updater` flag) and updates individual cluster balances on-chain:

1. **Listen for commits** - Subscribes to RootCommitted events from SSV Network contract
2. **Rebuild merkle tree** - Reconstructs tree from stored cluster balances
3. **Validate root** - Ensures computed root matches the committed root
4. **Check balances** - Reads current on-chain balance for each cluster (skips unchanged)
5. **Submit proofs** - Calls `UpdateClusterBalance` with merkle proof for each changed cluster

**Gas optimization:** The updater checks each cluster's current on-chain balance before submitting. Clusters with unchanged balances are skipped, saving gas.

**Fail-fast behavior:** If either the oracle or updater encounters a fatal error, both stop
to avoid partial updates and inconsistent Merkle state. Other oracle instances in the network
can safely continue processing commits.

## Merkle Tree Specification

The oracle builds an OpenZeppelin-compatible Merkle tree:

**Leaf encoding:**
```solidity
leaf = keccak256(abi.encode(clusterId, effectiveBalance))
```

**Tree construction:**
- Sort leaves by `clusterId` (ascending byte order)
- Duplicate last leaf if odd count
- Sort sibling pairs before hashing: `parent = keccak256(min(left, right) || max(left, right))`

**Cluster ID computation:**
```solidity
clusterId = keccak256(abi.encodePacked(owner, uint256(op1), uint256(op2), ...))
```
where operator IDs are sorted ascending.

## Database

SQLite database at `./data/oracle.db` with WAL mode for better concurrency.

**Tables:**
- `sync_progress` - Sync state and chain ID validation
- `contract_events` - Raw SSV contract events (audit log)
- `clusters` - Current cluster state
- `validators` - Validator membership (cluster_id, pubkey)
- `oracle_commits` - History of committed roots with cluster balances

**Database files (WAL mode):**
- `oracle.db` - Main database
- `oracle.db-wal` - Write-ahead log
- `oracle.db-shm` - Shared memory

**Backup:**
```bash
# When DB is idle
cp data/oracle.db data/oracle.db.backup

# Online backup (safe)
sqlite3 data/oracle.db ".backup data/oracle.db.backup"
```

## Project Structure

```
ssv-oracle/
├── cmd/oracle/         CLI application (Cobra)
├── contract/           Ethereum client & contract ABI
├── eth/                Ethereum-related packages
│   ├── beacon/         Beacon chain client (finality, validators)
│   ├── execution/      Execution layer client (logs, blocks)
│   └── syncer/         Event syncing & parsing
├── logger/             Zap-based structured logging
├── merkle/             Merkle tree construction & encoding
├── oracle/             Oracle cycle logic
├── storage/            SQLite storage layer
├── txmanager/          Transaction lifecycle (gas, retries)
├── updater/            Cluster balance updater
├── wallet/             Transaction signing (env, keystore)
└── data/               SQLite database files (gitignored)
```

## Logging

The oracle uses structured logging (zap). Configure via environment variables:

| Variable | Values | Default | Description |
|----------|--------|---------|-------------|
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` (prod) / `debug` (dev) | Minimum log level |
| `DEV` | `true`, `false` | `false` | Development mode: colored output, human-readable timestamps |

**Examples:**
```bash
# Production (JSON logs, info level)
LOG_LEVEL=info ./ssv-oracle run

# Development (colored console, debug level)
DEV=true ./ssv-oracle run

# Production with debug logging
LOG_LEVEL=debug ./ssv-oracle run
```

## Development

```bash
make build    # Build binary
make test     # Run tests
make lint     # Run linters (vet, fmt, golangci-lint)

# Run specific test
go test -run TestMerkleTree ./merkle
```

## Troubleshooting

**Database errors**
```bash
make db-reset  # Remove SQLite files and start fresh
make fresh     # Reset and restart
```

**Execution client connection failed**
- Verify `eth_rpc` endpoint is accessible
- Check chain ID matches expected network

**Beacon node sync incomplete**
```bash
curl <beacon_rpc>/eth/v1/node/syncing
# Wait for "is_syncing": false
```

**Initial sync is slow**
- Normal behavior (depends on block range)
- Adjust `sync_batch_size` in config (default: 200)
- Subsequent runs use incremental sync
