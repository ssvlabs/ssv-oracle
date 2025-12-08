# SSV Oracle

Off-chain oracle client that publishes Merkle roots of SSV cluster effective balances to an on-chain oracle contract.

## Features

- **Event-sourced architecture** - Syncs SSV contract events to PostgreSQL for point-in-time queries
- **Epoch-aligned timing** - Waits for epoch finalization before committing roots
- **OpenZeppelin-compatible Merkle trees** - Deterministic root computation with standardized sibling ordering
- **Beacon chain integration** - Fetches validator effective balances directly from consensus layer
- **Unified contract** - Uses SSV Network contract with integrated oracle functionality

## Quick Start

### Prerequisites

- Go 1.24+
- Docker (for PostgreSQL)
- Ethereum execution client (RPC endpoint)
- Beacon node (REST API endpoint)

### Installation

```bash
# Copy configuration templates
cp .env.example .env
cp config.yaml.example config.yaml

# Edit config.yaml with your endpoints
# - eth_rpc: Execution layer RPC
# - beacon_rpc: Beacon node API
# - ssv_contract: SSV Network contract address (includes oracle functionality)

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
eth_rpc: "http://localhost:8545"       # HTTP RPC for transactions
eth_ws_rpc: "ws://localhost:8546"      # WebSocket for subscriptions (required for --updater)
beacon_rpc: "http://localhost:5052"

# Contract (SSV Network with integrated oracle functionality)
ssv_contract: "0x..."

# Syncing
sync_from_block: 17507487  # SSV contract deployment block (mainnet example)
sync_batch_size: 200
sync_max_retries: 3

# Database
db_host: "localhost"
db_port: 5432
db_name: "ssv_oracle"
db_user: "oracle"
db_password_env: "DB_PASSWORD"

# Oracle
private_key_env: "PRIVATE_KEY"
```

## Oracle Cycle

The oracle executes the following steps each round:

1. **Sync events** - Fetch SSV contract events up to finalized epoch
2. **Calculate round** - Determine current round from finalized epoch and config
3. **Fetch balances** - Query beacon chain for validator effective balances
4. **Build Merkle tree** - Aggregate balances by cluster, construct tree
5. **Commit root** - Submit transaction with Merkle root and metadata
6. **Wait for confirmation** - Ensure transaction is mined successfully

## Cluster Updater

The updater runs alongside the oracle (enabled with `--updater` flag) and updates individual cluster balances on-chain:

1. **Listen for commits** - Subscribes to RootCommitted events from SSV Network contract
2. **Rebuild merkle tree** - Reconstructs tree from stored cluster balances
3. **Validate root** - Ensures computed root matches the committed root
4. **Check balances** - Reads current on-chain balance for each cluster (skips unchanged)
5. **Submit proofs** - Calls `UpdateClusterBalance` with merkle proof for each changed cluster

**Gas optimization:** The updater checks each cluster's current on-chain balance before submitting. Clusters with unchanged balances are skipped, saving gas.

**Fail-fast behavior:** If either the oracle or updater encounters a fatal error, both stop. This ensures consistency - other oracles in the network can process commits if one instance fails.

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

## Database Schema

PostgreSQL tables:
- `sync_progress` - Sync state and chain ID validation
- `contract_events` - Raw SSV contract events (audit log)
- `clusters` - Current cluster state
- `validators` - Validator membership (cluster_id, pubkey)
- `oracle_commits` - History of committed roots with cluster balances

## Project Structure

```
ssv-oracle/
├── cmd/oracle/         CLI application (Cobra)
├── contract/           Ethereum client & contract ABI
├── merkle/             Merkle tree construction & encoding
├── oracle/             Oracle cycle logic
├── updater/            Cluster balance updater
└── pkg/ethsync/        Event syncing, beacon client, storage
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

**Database connection failed**
```bash
docker ps  # Check if PostgreSQL is running
make fresh  # Reset and restart
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
