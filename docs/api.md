# API Reference

The oracle exposes an HTTP API for querying committed data and generating merkle proofs.

## Configuration

Configure the API server in `config.yaml`:

```yaml
api_address: "127.0.0.1:8080"  # Default: localhost only
```

- Default `127.0.0.1:8080` - Accessible only from localhost
- Use `0.0.0.0:8080` - Accessible from all interfaces

## Endpoints

### GET `/api/v1/commit`

Get the latest confirmed commit metadata.

**Response:**

```json
{
  "block_number": 12345678,
  "epoch": 54321,
  "root": "0xabcdef...",
  "tx_hash": "0x123456...",
  "timestamp": 1234567890
}
```

**Fields:**
- `block_number` - Execution layer block number where commit occurred
- `epoch` - Beacon chain epoch for this commit
- `root` - Merkle root committed on-chain
- `tx_hash` - Transaction hash of the commit
- `timestamp` - Block timestamp (Unix seconds)

**Example:**

```bash
curl http://127.0.0.1:8080/api/v1/commit
```

### GET `/api/v1/commit?full=true`

Get the latest commit with full cluster balances and merkle tree layers.

**Response:**

```json
{
  "block_number": 12345678,
  "epoch": 54321,
  "root": "0xabcdef...",
  "tx_hash": "0x123456...",
  "timestamp": 1234567890,
  "clusters": [
    {
      "cluster_id": "0xabc...",
      "effective_balance": 32000000000
    }
  ],
  "tree_layers": [
    ["0xleaf1...", "0xleaf2..."],
    ["0xnode1..."],
    ["0xroot..."]
  ]
}
```

**Additional fields:**
- `clusters` - Array of cluster balances used to build the tree
- `tree_layers` - Complete merkle tree structure (leaves to root)

**Example:**

```bash
curl http://127.0.0.1:8080/api/v1/commit?full=true
```

### GET `/api/v1/proof/{clusterId}`

Get the merkle proof for a specific cluster.

**Parameters:**
- `clusterId` - Cluster ID (hex string with 0x prefix)

**Response:**

```json
{
  "cluster_id": "0xabc...",
  "effective_balance": 32000000000,
  "proof": [
    "0xsibling1...",
    "0xsibling2...",
    "0xsibling3..."
  ]
}
```

**Fields:**
- `cluster_id` - The requested cluster ID
- `effective_balance` - Cluster's effective balance at commit time
- `proof` - Array of sibling hashes for merkle verification

**Example:**

```bash
curl http://127.0.0.1:8080/api/v1/proof/0x1234...
```

**Error response (cluster not found):**

```json
{
  "error": "cluster not found"
}
```

### GET `/`

Web UI for merkle tree visualization.

Open in browser:
```bash
open http://127.0.0.1:8080
```

The UI displays:
- Current merkle root
- Tree structure (visual)
- Cluster balances
- Interactive proof generation

## Use Cases

### Verifying Committed Root

Check what root was committed in the latest transaction:

```bash
curl -s http://127.0.0.1:8080/api/v1/commit | jq -r '.root'
```

Compare with on-chain data from SSV Network contract.

### Generating Merkle Proofs

Get proof for a specific cluster to verify balance on-chain:

```bash
CLUSTER_ID="0xabc123..."
curl -s http://127.0.0.1:8080/api/v1/proof/$CLUSTER_ID | jq
```

Use the proof array to verify the cluster's balance against the committed root.

### Debugging Tree Structure

Inspect the full tree structure to understand leaf ordering:

```bash
curl -s 'http://127.0.0.1:8080/api/v1/commit?full=true' | jq '.tree_layers'
```

Useful for:
- Debugging merkle tree construction
- Understanding leaf sorting (by hash, not cluster ID)
- Verifying proof generation logic

### Monitoring Commits

Poll the API to track new commits:

```bash
while true; do
  curl -s http://127.0.0.1:8080/api/v1/commit | jq '{epoch, root, tx_hash}'
  sleep 60
done
```

Or set up monitoring/alerting based on commit frequency and success.

## Integration Examples

### JavaScript/TypeScript

```javascript
// Get latest commit
const response = await fetch('http://127.0.0.1:8080/api/v1/commit');
const commit = await response.json();
console.log(`Latest root: ${commit.root} at epoch ${commit.epoch}`);

// Get merkle proof
const clusterId = '0xabc123...';
const proofResponse = await fetch(`http://127.0.0.1:8080/api/v1/proof/${clusterId}`);
const proofData = await proofResponse.json();
console.log('Proof:', proofData.proof);
```

### Python

```python
import requests

# Get latest commit
response = requests.get('http://127.0.0.1:8080/api/v1/commit')
commit = response.json()
print(f"Latest root: {commit['root']} at epoch {commit['epoch']}")

# Get merkle proof
cluster_id = '0xabc123...'
proof_response = requests.get(f'http://127.0.0.1:8080/api/v1/proof/{cluster_id}')
proof_data = proof_response.json()
print(f"Proof: {proof_data['proof']}")
```

### Go

```go
package main

import (
    "encoding/json"
    "fmt"
    "net/http"
)

type Commit struct {
    BlockNumber uint64 `json:"block_number"`
    Epoch       uint64 `json:"epoch"`
    Root        string `json:"root"`
    TxHash      string `json:"tx_hash"`
    Timestamp   int64  `json:"timestamp"`
}

func main() {
    // Get latest commit
    resp, _ := http.Get("http://127.0.0.1:8080/api/v1/commit")
    defer resp.Body.Close()
    
    var commit Commit
    json.NewDecoder(resp.Body).Decode(&commit)
    fmt.Printf("Latest root: %s at epoch %d\n", commit.Root, commit.Epoch)
}
```

## Next Steps

- Deploy oracle: See [Deployment Guide](deployment.md)
- Configure endpoints: Edit `config.yaml`
- Integrate with your application: Use examples above
