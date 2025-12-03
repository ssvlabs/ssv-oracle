# SSV Oracle

Off-chain oracle client that publishes Merkle roots of SSV cluster effective balances to an on-chain oracle contract.

## Features

- **Event-sourced architecture** - Syncs SSV contract events to PostgreSQL for point-in-time queries
- **Epoch-aligned timing** - Waits for epoch finalization before committing roots
- **OpenZeppelin-compatible Merkle trees** - Deterministic root computation with standardized sibling ordering
- **Beacon chain integration** - Fetches validator effective balances directly from consensus layer
- **Mock mode** - PoC testing without real contract deployment

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
# - ssv_contract: SSV contract address
# - oracle_contract: Oracle contract address (or 0x0... for mock mode)

# Load environment variables
source .env

# Start with fresh database
make fresh
```

## Usage

```bash
make fresh       # First run: reset DB and sync from genesis
make start       # Normal run: resume from last synced block
make test        # Run tests
```

**Additional commands:**
```bash
make db-shell    # Open PostgreSQL shell
make db-logs     # View database logs
make docker-down # Stop all services
```

## Configuration

Edit `config.yaml`:

```yaml
# Network
eth_rpc: "http://localhost:8545"
beacon_rpc: "http://localhost:5052"

# Contracts
ssv_contract: "0x..."
oracle_contract: "0x..."  # Use 0x0000... for mock mode

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
mock_mode: true           # Set false for production
```

## Oracle Cycle

The oracle executes the following steps each round:

1. **Sync events** - Fetch SSV contract events up to finalized epoch
2. **Calculate round** - Determine current round from finalized epoch and config
3. **Fetch balances** - Query beacon chain for validator effective balances
4. **Build Merkle tree** - Aggregate balances by cluster, construct tree
5. **Commit root** - Submit transaction with Merkle root and metadata
6. **Wait for confirmation** - Ensure transaction is mined successfully

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
- `contract_events` - Raw SSV contract events (source of truth)
- `validator_events` - Validator membership changes (Added/Removed)
- `cluster_events` - Cluster status changes (Liquidated/Reactivated)
- `validator_balances` - Effective balance snapshots (epoch-based)
- `cluster_state` - Current cluster metadata
- `oracle_commits` - History of committed roots
- `sync_progress` - Sync state and chain ID validation

## Project Structure

```
ssv-oracle/
├── cmd/oracle/         CLI application (Cobra)
├── contract/           Ethereum client & contract ABI
├── merkle/             Merkle tree construction & encoding
├── oracle/             Oracle cycle logic
└── pkg/ethsync/        Event syncing, beacon client, storage
```

## Development

```bash
# Run tests
go test ./...

# Run specific test
go test -run TestMerkleTree ./merkle

# Format code
go fmt ./...

# Lint
go vet ./...
```

## Troubleshooting

**Database connection failed**
```bash
docker ps  # Check if PostgreSQL is running
make docker-down && make fresh  # Restart services
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
