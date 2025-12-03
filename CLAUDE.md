# CLAUDE.md

This file provides guidance to Claude Code when working with this repository.

## Project Overview

`ssv-oracle` is a Go 1.24 oracle client that publishes Merkle roots of SSV cluster effective balances to an onchain oracle contract.

## Development Commands

```bash
go build ./...          # Build
go test ./...           # Test
go fmt ./...            # Format
go vet ./...            # Lint
make fresh              # Fresh start (clear DB)
make start-oracle       # Start oracle (resume from last state)
make start-updater      # Start cluster updater
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
2. Get timing config (startEpoch, epochInterval)
3. Calculate target epoch and round ID
4. Check finalization via beacon API
5. Fetch effective balances from beacon
6. Build Merkle tree
7. Commit root to contract (mock mode for PoC)

### Cluster Updater (updater/)
Listens for RootCommitted events and updates cluster balances on-chain:
1. Listen for commits (PostgreSQL NOTIFY in mock mode, contract events in real mode)
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
- `contract_events` - Raw SSV events (never deleted)
- `validator_events` - Validator membership changes
- `cluster_events` - Cluster operational status changes
- `validator_balances` - Effective balance snapshots
- `cluster_state` - Current cluster metadata

Key functions:
- `get_cluster_effective_balances(epoch)` - For Merkle tree
- `get_active_validators_with_clusters(epoch)` - For balance fetching
- `is_ready_to_commit(epoch)` - Check readiness

## Configuration

```yaml
eth_rpc: "http://localhost:8545"      # Execution layer RPC
beacon_rpc: "http://localhost:5052"   # Beacon node RPC
ssv_contract: "0x..."                 # SSV contract address
mock_mode: true                       # PoC - no real contract calls
```

Chain ID is auto-detected from RPC.

## Mock Mode (PoC)

Currently runs in mock mode:
- Syncs real SSV events
- Fetches real validator balances
- Builds real Merkle trees
- Logs commits instead of sending transactions
- Stores state in PostgreSQL

Set `mock_mode: false` when oracle contract is deployed.
