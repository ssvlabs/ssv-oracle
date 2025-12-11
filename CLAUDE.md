# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## Project Overview

`ssv-oracle` is a Go 1.25 oracle client that publishes Merkle roots of SSV cluster effective balances to an onchain oracle contract.

## Development Commands

```bash
make build      # Build
make test       # Test
make lint       # Lint
make run        # Run oracle
make run-all    # Run oracle with updater
make fresh      # Fresh start (reset DB)
make fresh-all  # Fresh start with updater
make db-reset   # Remove SQLite database files
make clean      # Remove build artifacts and database
```

## Project Structure

```
ssv-oracle/
├── cmd/oracle/         # CLI entry point (cobra)
├── contract/           # Ethereum client & contract interaction
├── merkle/             # Merkle tree (Bitcoin/OpenZeppelin standard)
├── oracle/             # Main oracle loop
├── updater/            # Cluster balance updater
├── wallet/             # Transaction signing (env, keystore)
├── txmanager/          # Transaction lifecycle (gas, retries, cancellation)
├── pkg/ethsync/        # Event syncing & storage (SQLite)
└── data/               # SQLite database files (gitignored)
```

## Key Components

### Event Syncing (pkg/ethsync)
- Syncs SSV contract events to SQLite
- Tracks validator and cluster state
- Schema auto-applies on startup via `//go:embed schema.sql`
- Uses WAL mode for better concurrency

### Oracle Loop (oracle/)
1. Sync events incrementally
2. Calculate target epoch from commit phases
3. Wait for epoch finalization via beacon API
4. Fetch effective balances from beacon
5. Build Merkle tree
6. Commit root to SSV Network contract

### Cluster Updater (updater/)
Listens for RootCommitted events and updates cluster balances on-chain:
1. Listen for RootCommitted events from SSV Network contract
2. Rebuild merkle tree from stored cluster balances
3. Validate computed root matches committed root
4. Generate merkle proof for each cluster
5. Call UpdateClusterBalance on contract with proof

### Merkle Tree (merkle/)
- Leaf: `keccak256(abi.encode(clusterId, effectiveBalance))`
- Sort leaves by clusterId (bytes comparison)
- Duplicate last node if odd count (Bitcoin standard)
- Sort siblings before hashing (OpenZeppelin standard)
- Empty tree: `keccak256("")`
- `BuildMerkleTreeWithProofs`: stores layers for proof generation
- `GetProof`: returns sibling hashes from leaf to root

### Cluster ID (pkg/ethsync)
```go
keccak256(abi.encodePacked(owner, uint256(op1), uint256(op2), ...))
```
- Operator IDs sorted ascending
- Each operator ID is 32-byte uint256

## Database (SQLite)

Single-file database at `./data/oracle.db` with WAL mode enabled.

### Key Tables
- `sync_progress` - Chain ID and last synced block
- `contract_events` - Raw SSV events (append-only)
- `clusters` - Current cluster state (deleted when validator_count = 0)
- `validators` - Validator membership (cascade delete with cluster)
- `oracle_commits` - Commit history with cluster balances for merkle reconstruction

### Database Files
- `oracle.db` - Main database file
- `oracle.db-wal` - Write-ahead log (WAL mode)
- `oracle.db-shm` - Shared memory file (WAL mode)

### Backup
```bash
# When DB is idle
cp data/oracle.db data/oracle.db.backup

# Online backup
sqlite3 data/oracle.db ".backup data/oracle.db.backup"
```

## Configuration

```yaml
eth_rpc: "http://localhost:8545"      # Execution layer RPC (HTTP)
eth_ws_rpc: "ws://localhost:8546"     # Execution layer WebSocket (for updater)
beacon_rpc: "http://localhost:5052"   # Beacon node RPC
ssv_contract: "0x..."                 # SSV Network contract (includes oracle functionality)
ssv_views_contract: "0x..."           # SSV Network Views contract (for getBalance)
db_path: "./data/oracle.db"           # SQLite database path
```

- Chain ID is auto-detected from RPC
- `eth_ws_rpc` is required when running with `--updater` (event subscriptions need WebSocket)

### Wallet Configuration

The oracle supports multiple signing backends via the `wallet` config section:

```yaml
wallet:
  type: "env"                        # "env" or "keystore"
  private_key_env: "PRIVATE_KEY"     # For type: env

  # For type: keystore
  # keystore_path: "/path/to/keystore.json"
  # password_env: "KEYSTORE_PASSWORD"  # Password from env var
  # password_file: "/path/to/password.txt"  # Or password from file
```

**Signer Types:**
- `env`: Read private key from environment variable (simple, for development)
- `keystore`: Use encrypted keystore file with password (recommended for production)

### Transaction Policy (txmanager/)

Automatic transaction management with gas bumping, retries, and cancellation:

```yaml
tx_policy:
  gas_buffer_percent: 20        # Add 20% to gas estimates
  max_fee_per_gas: "100 gwei"   # Hard cap on gas price
  pending_timeout_blocks: 10    # Blocks before bumping
  gas_bump_percent: 10          # Minimum bump for RBF (EIP-1559 requires ≥10%)
  max_retries: 3                # Attempts before cancellation
  retry_delay: 5s               # Delay between retries
```

**Lifecycle:** estimate gas → submit tx → monitor blocks → bump if stuck → cancel if max reached
