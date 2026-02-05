# SSV Oracle

Off-chain oracle that publishes Merkle roots of SSV cluster effective balances to the SSV Network contract.

## Features

- **Event-sourced** - Syncs SSV contract events to SQLite for point-in-time queries
- **Epoch-aligned** - Commits only after beacon chain finalization
- **OpenZeppelin-compatible** - StandardMerkleTree format with deterministic ordering
- **Single binary** - Embedded SQLite database
- **HTTP API** - Query committed data and generate merkle proofs

## Quick Start

**Prerequisites:** Go 1.25+, Ethereum execution client, Beacon node, funded wallet

```bash
# Setup
cp .env.example .env && cp config.yaml.example config.yaml
# Edit config.yaml with your endpoints and contract addresses

# Run
make fresh      # Fresh start (clears DB)
make fresh-all  # Fresh start with updater
make run        # Oracle only
make run-all    # Oracle + cluster updater
```

**CLI:**
```bash
make build
./ssv-oracle run --config config.yaml                    # Oracle only
./ssv-oracle run --config config.yaml --updater          # With cluster updater
./ssv-oracle run --config config.yaml --fresh            # Clear DB first
./ssv-oracle run --config config.yaml --fresh --updater  # Both
```

For Docker deployment, see the [Deployment Guide](docs/deployment.md).

## How It Works

### Oracle

The oracle is event-driven, reacting to beacon chain finalization:

1. Subscribe to beacon node finalized checkpoint events (SSE)
2. Sync SSV contract events up to the finalized block
3. Fetch validator effective balances from beacon chain
4. Build Merkle tree aggregated by cluster
5. Submit root to SSV Network contract

**Finalization:** When beacon reports `checkpoint.Epoch = N`, epoch `N-1` is fully finalized. The oracle uses this to determine which targets can be committed.

### Cluster Updater

Runs alongside the oracle (`--updater` flag) to update individual cluster balances on-chain:

1. Listen for `RootCommitted` events
2. Rebuild Merkle tree from stored balances
3. For each cluster with changed balance, submit proof via `UpdateClusterBalance`

Clusters with unchanged balances are skipped to save gas.

## Database

SQLite at `./data/oracle.db`. Reset with `make db-reset`, `make fresh`, or `make fresh-all`.

**Backup:**
```bash
sqlite3 data/oracle.db ".backup data/oracle.db.backup"
```

## API

The oracle exposes an HTTP API for querying committed data and generating merkle proofs.

**Endpoints:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/commit` | Latest confirmed commit metadata |
| GET | `/api/v1/commit?full=true` | Include clusters and tree layers |
| GET | `/api/v1/proof/{clusterId}` | Merkle proof for a cluster |
| GET | `/` | Tree visualization UI |

**Configuration:**
```yaml
api_address: "127.0.0.1:8080"  # Default: localhost only
```

To expose externally, use `0.0.0.0:8080` (ensure firewall/proxy protection).

**Example:**
```bash
# Get latest commit
curl http://127.0.0.1:8080/api/v1/commit

# Get merkle proof for a cluster
curl http://127.0.0.1:8080/api/v1/proof/0x1234...

# Open tree visualization
open http://127.0.0.1:8080
```

For detailed API documentation, see the [API Reference](docs/api.md).

## Development

Run `make` to see all available targets.

Set `DEV=true` env for colored console output.

## Troubleshooting

| Issue | Solution |
|-------|----------|
| Database errors | `make fresh` or `make fresh-all` to reset |
| Connection failed | Verify RPC endpoints are accessible |
| Beacon not synced | Wait for `curl <beacon_url>/eth/v1/node/syncing` to show `is_syncing: false` |

