# SSV Oracle

Off-chain oracle that bridges Ethereum's beacon chain and SSV Network smart contracts by tracking validator effective balances and publishing Merkle roots on-chain. It keeps cluster balances in sync to support fair fee accrual in **SSV Staking**, including consolidated validators introduced in Ethereum's Pectra upgrade.

## Features

- **Event-sourced** - Syncs SSV contract events to SQLite for point-in-time queries
- **Epoch-aligned** - Commits only after beacon chain finalization
- **OpenZeppelin-compatible** - StandardMerkleTree format with deterministic ordering
- **Single binary** - Embedded SQLite database
- **HTTP API** - Query committed data and generate merkle proofs

## Getting Started

**For deployment:**
- [Docker deployment](docs/deployment.md)
- [Configuration guide](docs/deployment.md#configuration)
- [Troubleshooting](docs/deployment.md#troubleshooting)

**For API integration:**
- [API reference](docs/api.md)
- [Integration examples](docs/api.md#integration-examples)

**Quick reference:**
- Configuration: [`config.yaml.example`](config.yaml.example)
- Environment: [`.env.example`](.env.example)

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

## Development

**Building and testing:**
```bash
make build      # Build binary
make test       # Run tests
make lint       # Run linters
```

**Running locally:**
```bash
make fresh      # Fresh start (clears DB)
make run        # Continue from existing DB
```

**Development mode** (colored logs):
```bash
DEV=true make run
```

**All targets:**
```bash
make help       # Show all available commands
```
