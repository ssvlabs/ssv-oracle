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
  "epoch": 54321,
  "referenceBlock": 12345678,
  "merkleRoot": "0xabcdef...",
  "txHash": "0x123456..."
}
```

**Fields:**
- `epoch` - Beacon chain epoch for this commit
- `referenceBlock` - Finalized block number used to compute the merkle root
- `merkleRoot` - Merkle root committed on-chain
- `txHash` - Transaction hash of the commit

**Example:**

```bash
curl http://127.0.0.1:8080/api/v1/commit
```

### GET `/api/v1/commit?full=true`

Get the latest commit with full cluster balances and merkle tree layers.

**Response:**

```json
{
  "epoch": 54321,
  "referenceBlock": 12345678,
  "merkleRoot": "0xabcdef...",
  "txHash": "0x123456...",
  "clusters": [
    {
      "clusterId": "0xabc...",
      "effectiveBalance": 320,
      "hash": "0xleaf..."
    }
  ],
  "layers": [
    ["0xleaf1...", "0xleaf2..."],
    ["0xnode1..."],
    ["0xroot..."]
  ]
}
```

**Additional fields:**
- `clusters` - Array of cluster balances used to build the tree
- `layers` - Complete merkle tree structure (leaves to root)

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
  "clusterId": "0xabc...",
  "effectiveBalance": 320,
  "proof": [
    "0xsibling1...",
    "0xsibling2...",
    "0xsibling3..."
  ],
  "merkleRoot": "0xabcdef...",
  "referenceBlock": 12345678
}
```

**Fields:**
- `clusterId` - The requested cluster ID
- `effectiveBalance` - Cluster's effective balance at commit time
- `proof` - Array of sibling hashes for merkle verification
- `merkleRoot` - Merkle root the proof is built against
- `referenceBlock` - Finalized block number used to compute the merkle root

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
curl -s http://127.0.0.1:8080/api/v1/commit | jq -r '.merkleRoot'
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
curl -s 'http://127.0.0.1:8080/api/v1/commit?full=true' | jq '.layers'
```

Useful for:
- Debugging merkle tree construction
- Understanding leaf sorting (by hash, not cluster ID)
- Verifying proof generation logic

### Monitoring Commits

Poll the API to track new commits:

```bash
while true; do
  curl -s http://127.0.0.1:8080/api/v1/commit | jq '{epoch, merkleRoot, txHash}'
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
console.log(`Latest root: ${commit.merkleRoot} at epoch ${commit.epoch}`);

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
print(f"Latest root: {commit['merkleRoot']} at epoch {commit['epoch']}")

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
    "log"
    "net/http"
)

type Commit struct {
    Epoch          uint64 `json:"epoch"`
    ReferenceBlock uint64 `json:"referenceBlock"`
    MerkleRoot     string `json:"merkleRoot"`
    TxHash         string `json:"txHash"`
}

func main() {
    // Get latest commit
    resp, err := http.Get("http://127.0.0.1:8080/api/v1/commit")
    if err != nil {
        log.Fatal(err)
    }
    defer resp.Body.Close()
    
    var commit Commit
    if err := json.NewDecoder(resp.Body).Decode(&commit); err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Latest root: %s at epoch %d\n", commit.MerkleRoot, commit.Epoch)
}
```

## Next Steps

- Deploy oracle: See [Deployment Guide](deployment.md)
- Configure endpoints: Edit `config.yaml`
- Integrate with your application: Use examples above
