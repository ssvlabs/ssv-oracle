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

Get the latest confirmed commit metadata. Use `?epoch=N` to query a specific epoch.

**Query parameters:**
- `epoch` *(optional)* - Target epoch to retrieve. Omit for latest confirmed commit.
- `full` *(optional)* - Set to `true` to include clusters, tree layers, balance diff, and cluster info.

**Response:**

```json
{
  "epoch": 54321,
  "status": "confirmed",
  "referenceBlock": 12345678,
  "merkleRoot": "0xabcdef...",
  "txHash": "0x123456...",
  "previousEpoch": 54200,
  "nextEpoch": 54442
}
```

**Fields:**
- `epoch` - Beacon chain epoch for this commit
- `status` - Commit status: `pending`, `confirmed`, or `failed`
- `referenceBlock` - Finalized block number used to compute the merkle root
- `merkleRoot` - Merkle root committed on-chain
- `txHash` - Transaction hash (empty string if not yet submitted)
- `previousEpoch` - Previous commit epoch, or `null` at the first commit
- `nextEpoch` - Next commit epoch, or `null` at the latest commit

**Examples:**

```bash
# Latest confirmed commit
curl http://127.0.0.1:8080/api/v1/commit

# Commit for a specific epoch
curl http://127.0.0.1:8080/api/v1/commit?epoch=54321

# Full details with clusters, tree layers, and balance changes
curl 'http://127.0.0.1:8080/api/v1/commit?full=true&epoch=54321'
```

#### Full mode (`full=true`)

When `full=true` is set, the response includes additional fields:

```json
{
  "epoch": 54321,
  "status": "confirmed",
  "referenceBlock": 12345678,
  "merkleRoot": "0xabcdef...",
  "txHash": "0x123456...",
  "previousEpoch": 54200,
  "nextEpoch": 54442,
  "clusters": [
    {
      "clusterId": "0xabc...",
      "effectiveBalance": 320,
      "hash": "0xleaf...",
      "ownerAddress": "0x1234...",
      "operatorIds": [1, 2, 3, 4]
    }
  ],
  "layers": [
    ["0xparent1...", "0xparent2..."],
    ["0xroot..."]
  ],
  "totalEffectiveBalance": 12800,
  "balanceDiff": {
    "previousEpoch": 54200,
    "changed": [
      { "clusterId": "0xabc...", "oldBalance": 310, "newBalance": 320 }
    ],
    "added": [
      { "clusterId": "0xdef...", "balance": 320 }
    ],
    "removed": [
      { "clusterId": "0x999...", "balance": 160 }
    ]
  }
}
```

**Additional fields:**
- `clusters` - Array of cluster balances with leaf hashes, owner address, and operator IDs
- `layers` - Inner merkle tree layers (excludes leaves, which are in `clusters`)
- `totalEffectiveBalance` - Sum of all cluster effective balances
- `balanceDiff` - Cluster changes compared to the previous commit (omitted if no changes or no previous commit)
  - `changed` - Clusters with different balances in both commits
  - `added` - Clusters present in current but not previous commit
  - `removed` - Clusters present in previous but not current commit

### GET `/api/v1/proof/{clusterId}`

Get the merkle proof for a specific cluster.

**Parameters:**
- `clusterId` - Cluster ID (hex string with 0x prefix)

**Query parameters:**
- `epoch` *(optional)* - Target epoch for the proof. Omit for latest confirmed commit.

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
# Proof from latest confirmed commit
curl http://127.0.0.1:8080/api/v1/proof/0x1234...

# Proof from a specific epoch
curl 'http://127.0.0.1:8080/api/v1/proof/0x1234...?epoch=54321'
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
- Commit status, epoch navigation, and reference block
- Merkle tree structure (interactive visualization)
- Cluster balances with search by cluster ID, owner address, or operator IDs
- Balance changes between commits
- Merkle proof generation for selected clusters

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

### Navigating Commit History

Browse commits by epoch:

```bash
# Get a specific epoch
curl -s 'http://127.0.0.1:8080/api/v1/commit?epoch=54321' | jq '{epoch, status, previousEpoch, nextEpoch}'

# Get full details with balance changes
curl -s 'http://127.0.0.1:8080/api/v1/commit?full=true&epoch=54321' | jq '.balanceDiff'
```

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
  curl -s http://127.0.0.1:8080/api/v1/commit | jq '{epoch, status, merkleRoot, txHash}'
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
console.log(`Latest root: ${commit.merkleRoot} at epoch ${commit.epoch} (${commit.status})`);

// Get commit with full details
const fullResponse = await fetch('http://127.0.0.1:8080/api/v1/commit?full=true');
const full = await fullResponse.json();
console.log(`Clusters: ${full.clusters.length}, Total balance: ${full.totalEffectiveBalance}`);

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
print(f"Latest root: {commit['merkleRoot']} at epoch {commit['epoch']} ({commit['status']})")

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
    Epoch          uint64  `json:"epoch"`
    Status         string  `json:"status"`
    ReferenceBlock uint64  `json:"referenceBlock"`
    MerkleRoot     string  `json:"merkleRoot"`
    TxHash         string  `json:"txHash"`
    PreviousEpoch  *uint64 `json:"previousEpoch"`
    NextEpoch      *uint64 `json:"nextEpoch"`
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
    fmt.Printf("Latest root: %s at epoch %d (%s)\n", commit.MerkleRoot, commit.Epoch, commit.Status)
}
```

## Next Steps

- Deploy oracle: See [Deployment Guide](deployment.md)
- Configure endpoints: Edit `config.yaml`
- Integrate with your application: Use examples above
