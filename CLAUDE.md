# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## Project Overview

`ssv-oracle` is a Go 1.24 oracle client that publishes Merkle roots of SSV cluster effective balances to an onchain oracle contract.

## Development Commands

```bash
make build      # Build
make test       # Test
make lint       # Lint
make run        # Run oracle
make run-all    # Run oracle with updater
make fresh      # Fresh start (reset DB)
make fresh-all  # Fresh start with updater
```

## Project Structure

```
ssv-oracle/
├── cmd/oracle/         # CLI entry point (cobra)
├── contract/           # Ethereum client & contract interaction
├── merkle/             # Merkle tree (Bitcoin/OpenZeppelin standard)
├── oracle/             # Main oracle loop
├── updater/            # Cluster balance updater
└── pkg/ethsync/        # Event syncing & storage (PostgreSQL)
```

## Key Components

### Event Syncing (pkg/ethsync)
- Syncs SSV contract events to PostgreSQL
- Tracks validator and cluster state
- Schema auto-applies on startup via `//go:embed schema.sql`

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

## Database Schema

Key tables:
- `sync_progress` - Chain ID and last synced block
- `contract_events` - Raw SSV events (append-only)
- `clusters` - Current cluster state (deleted when validator_count = 0)
- `validators` - Validator membership (cascade delete with cluster)
- `oracle_commits` - Commit history with cluster balances for merkle reconstruction

## Configuration

```yaml
eth_rpc: "http://localhost:8545"      # Execution layer RPC (HTTP)
eth_ws_rpc: "ws://localhost:8546"     # Execution layer WebSocket (for updater)
beacon_rpc: "http://localhost:5052"   # Beacon node RPC
ssv_contract: "0x..."                 # SSV Network contract (includes oracle functionality)
```

- Chain ID is auto-detected from RPC
- `eth_ws_rpc` is required when running with `--updater` (event subscriptions need WebSocket)
