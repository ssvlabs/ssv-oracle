# Deployment Guide

Deploy the SSV Oracle using Docker Compose.

## Prerequisites

- Docker 20.10+ with Docker Compose
- Ethereum execution client (HTTP RPC)
- Beacon node (HTTP RPC)
- Funded wallet with ETH for gas

## Quick Start

```bash
# 1. Copy config files
cp config.yaml.example config.yaml
cp .env.example .env

# 2. Edit config.yaml with your endpoints and contract addresses
# 3. Set wallet credentials in .env (PRIVATE_KEY or KEYSTORE_PASSWORD)

# 4. Run
docker compose up -d --build

# 5. Verify
curl http://127.0.0.1:8080/api/v1/commit
```

**Hoodi Testnet:** Use `config.hoodi.yaml` directly without copying:
```bash
./ssv-oracle run --config config.hoodi.yaml
# Docker: mount config.hoodi.yaml as /config/config.yaml
```

## Docker Compose

The repository includes [`docker-compose.yml`](../docker-compose.yml).

**Basic commands:**

```bash
docker compose up -d --build    # Build and run
docker compose logs -f          # View logs
docker compose down             # Stop
```

**Enable cluster updater:** Edit `docker-compose.yml` and uncomment:
```yaml
command: ["run", "--config", "/config/config.yaml", "--updater"]
```

**Use keystore wallet:** Uncomment the keystore volume in `docker-compose.yml`:
```yaml
- ./keystore.json:/config/keystore.json:ro
```

**Use pre-built image:** Replace build section with:
```yaml
image: ssvlabs/ssv-oracle:v1.0.0
```

## Configuration

All settings are in `config.yaml`. See [`config.yaml.example`](../config.yaml.example) for details.

| Setting | Description |
|---------|-------------|
| `eth_rpc` | Execution layer HTTP endpoint |
| `eth_ws_rpc` | WebSocket endpoint (required for `--updater`) |
| `beacon_url` | Beacon node HTTP endpoint |
| `mev_rpcs` | MEV RPC URLs for frontrunning protection (optional) |
| `ssv_contract` | SSV Network contract address |
| `ssv_views_contract` | SSV Views contract (required for `--updater`) |
| `db_path` | SQLite path (`/data/oracle.db` for Docker) |
| `api_address` | API bind address (`0.0.0.0:8080` to expose) |
| `metrics_address` | Metrics bind address (default `127.0.0.1:9090`) |

**Wallet options:**

```yaml
# Option A: Private key (development only)
wallet:
  type: "env"
  private_key_env: "PRIVATE_KEY"

# Option B: Keystore (production)
wallet:
  type: "keystore"
  keystore_path: "/config/keystore.json"
  password_env: "KEYSTORE_PASSWORD"
```

**Transaction policy:** `gas_buffer_percent`, `max_fee_per_gas_gwei`, `pending_timeout_blocks`, `gas_bump_percent`, `max_attempts`

**Commit phases:** Define commit schedule with `start_epoch` and `interval`.

## Database

SQLite at `db_path` (default: `./data/oracle.db`).

**Backup:**
```bash
sqlite3 data/oracle.db ".backup data/oracle.db.backup"  # Online
docker cp ssv-oracle:/data/oracle.db ./backup.db        # Docker
```

**Reset:**
```bash
make fresh      # Delete DB and restart
```

## Local Development

```bash
make build      # Build binary
make run        # Oracle only
make run-all    # Oracle + updater
make fresh      # Fresh start
```

## Updates

```bash
docker compose down
docker compose pull         # If using pre-built image
docker compose up -d --build
```

## Troubleshooting

| Issue | Solution |
|-------|----------|
| Database errors | `make fresh` to reset |
| Connection failed | Verify RPC endpoints |
| Beacon not synced | Check `curl <beacon_url>/eth/v1/node/syncing` |
