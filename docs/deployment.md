# Deployment Guide

This guide covers deploying the SSV Oracle using Docker.

**Note:** All commands in this guide should be run from the repository root directory.

## Prerequisites

- Docker 20.10+ with Docker Compose
- Ethereum execution client (HTTP RPC endpoint)
- Beacon node (HTTP RPC endpoint)
- Funded wallet with ETH for gas

## Setup

**1. Create configuration files:**

```bash
cp config.yaml.example config.yaml
```

**2. Edit `config.yaml` with your settings:**

See [`config.yaml.example`](../config.yaml.example) for all available options. Key settings for Docker:

- `eth_rpc` - Execution layer HTTP endpoint
- `eth_ws_rpc` - WebSocket endpoint (required for `--updater`)
- `beacon_rpc` - Beacon node HTTP endpoint
- `ssv_contract` - SSV Network contract address
- `ssv_views_contract` - SSV Views contract (required for `--updater`)
- `db_path` - Use `/data/oracle.db` for Docker
- `api_address` - Use `0.0.0.0:8080` to expose outside container

**3. Configure wallet in `config.yaml`:**

Two options:

**Option A: Keystore (recommended for production)**

```yaml
wallet:
  type: "keystore"
  keystore_path: "/config/keystore.json"
  password_env: "KEYSTORE_PASSWORD"
```

Uncomment the keystore volume mount in [`docker-compose.yml`](../docker-compose.yml):
```yaml
volumes:
  - ./keystore.json:/config/keystore.json:ro
```

Uncomment and set password in `docker-compose.yml`:
```yaml
environment:
  - KEYSTORE_PASSWORD=${KEYSTORE_PASSWORD}
```

Then create `.env`:
```bash
echo "KEYSTORE_PASSWORD=your_password_here" > .env
```

**Option B: Private key from environment (development only)**

```yaml
wallet:
  type: "env"
  private_key_env: "PRIVATE_KEY"
```

Uncomment in `docker-compose.yml`:
```yaml
environment:
  - PRIVATE_KEY=${PRIVATE_KEY}
```

Then create `.env`:
```bash
echo "PRIVATE_KEY=0x..." > .env
```

## Running

The repository includes a production-ready [`docker-compose.yml`](../docker-compose.yml) file.

**Build and run locally (default):**

```bash
docker compose up -d --build
```

By default, the docker-compose.yml builds the image locally. It runs with the `--updater` flag enabled.

**Alternative: Use pre-built image from Docker Hub**

If you prefer using a pre-built image, edit `docker-compose.yml`:
```yaml
# Comment out the build section:
# build:
#   context: .
#   args:
#     VERSION: dev
#     GIT_COMMIT: local

# Uncomment this:
image: ssvlabs/ssv-oracle:latest
```

Then run:
```bash
docker compose up -d
```

**View logs:**

```bash
docker compose logs -f
```

**Stop:**

```bash
docker compose down
```

## Using Makefile (Alternative)

For local development without Docker Compose:

```bash
make docker        # Build image
make docker-run    # Run container (uses PRIVATE_KEY env)
make docker-stop   # Stop and remove container
```

Note: `make docker-run` uses environment variable `PRIVATE_KEY` (not recommended for production).

## Updates

To update to a new version:

```bash
docker compose down
docker compose pull    # Pull latest image
docker compose up -d
```

Database migrations run automatically on startup.

## Production Best Practices

1. **Use keystore**, not `PRIVATE_KEY` environment variable
2. **Backup database**: Regularly backup `./data/oracle.db` (see Database section below)
3. **Monitor logs**: Set up log aggregation for production
4. **Pin versions**: In production, use specific tags instead of `:latest` in docker-compose.yml
   ```yaml
   image: ssvlabs/ssv-oracle:v1.0.0
   ```



## Configuration

All configuration is in `config.yaml`. See [`config.yaml.example`](../config.yaml.example) for detailed comments on each setting.

### Key Settings

**Ethereum RPC:**
- `eth_rpc` - HTTP endpoint (required)
- `eth_ws_rpc` - WebSocket endpoint (required when using `--updater`)

**Contracts:**
- `ssv_contract` - SSV Network contract address
- `ssv_views_contract` - SSV Views contract (required for `--updater`)

**Database:**
- `db_path` - SQLite database file path
  - Docker: `/data/oracle.db`
  - Bare-metal: `./data/oracle.db`

**API:**
- `api_address` - HTTP server bind address
  - Localhost only: `127.0.0.1:8080`
  - Exposed (Docker): `0.0.0.0:8080`

**Wallet:**

Two options:

1. **Environment variable** (simple, not recommended for production):
```yaml
wallet:
  type: "env"
  private_key_env: "PRIVATE_KEY"
```

2. **Keystore** (recommended for production):
```yaml
wallet:
  type: "keystore"
  keystore_path: "/path/to/keystore.json"
  password_env: "KEYSTORE_PASSWORD"
```

**Transaction Policy:**
- `gas_buffer_percent` - Extra gas % added to estimates (0-100)
- `max_fee_per_gas_gwei` - Hard cap on gas price
- `pending_timeout_blocks` - Blocks before gas bump
- `gas_bump_percent` - Gas increase per retry (min 10%)
- `max_attempts` - Total submission attempts

**Commit Phases:**

Define when to commit roots on-chain:

```yaml
commit_phases:
  - start_epoch: 0
    interval: 225  # Every 225 epochs
```

Multiple phases supported. Each phase's `start_epoch` must be aligned with previous phase intervals.

---

## Database

The oracle uses SQLite at the configured `db_path` (default: `./data/oracle.db`).

### Files

- `oracle.db` - Main database
- `oracle.db-wal` - Write-ahead log (WAL mode)
- `oracle.db-shm` - Shared memory

### Backup

**Online backup** (while oracle is running):

```bash
sqlite3 data/oracle.db ".backup data/oracle.db.backup"
```

**Offline backup** (oracle stopped):

```bash
cp data/oracle.db data/oracle.db.backup
```

**Docker volume backup:**

```bash
docker run --rm -v ssv-oracle_data:/data -v $(pwd):/backup alpine \
  cp /data/oracle.db /backup/oracle.db.backup
```

### Reset

To start fresh (clears all synced data):

```bash
make db-reset   # Just delete database
make fresh      # Delete DB and restart oracle
```

Or manually:

```bash
rm -f data/oracle.db data/oracle.db-wal data/oracle.db-shm
```

---

## Troubleshooting

| Issue | Solution |
|-------|----------|
| Database errors | `make fresh` or `make fresh-all` to reset |
| Connection failed | Verify RPC endpoints are accessible |
| Beacon not synced | Wait for `curl <beacon_rpc>/eth/v1/node/syncing` to show `is_syncing: false` |
