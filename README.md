# SSV Oracle

Off-chain oracle that publishes Merkle roots of SSV cluster effective balances to the SSV Network contract.

## Features

- **Event-sourced** - Syncs SSV contract events to SQLite for point-in-time queries
- **Epoch-aligned** - Commits only after beacon chain finalization
- **OpenZeppelin-compatible** - StandardMerkleTree format with deterministic ordering
- **Single binary** - Embedded SQLite database

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

## Development

Run `make` to see all available targets.

Set `DEV=true` env for colored console output.

## Troubleshooting

| Issue | Solution |
|-------|----------|
| Database errors | `make fresh` or `make fresh-all` to reset |
| Connection failed | Verify RPC endpoints are accessible |
| Beacon not synced | Wait for `curl <beacon_rpc>/eth/v1/node/syncing` to show `is_syncing: false` |
