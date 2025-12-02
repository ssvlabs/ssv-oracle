# SSV Cluster Effective Balance Oracle – Offchain Client Spec

## 1. Scope

This document specifies the **offchain oracle client** that periodically publishes a Merkle root of **effective balances of all SSV clusters** to an onchain oracle contract.

Out of scope: onchain logic for thresholds, weighted majority, and fee distribution.

The client must:

- Read **timing configuration** (`startEpoch`, `epoch_interval`) from the contract.
- Determine the **current target epoch** and corresponding **roundId**.
- Ensure the **target epoch is finalized**.
- Fetch effective balances changes of all clusters from the previous target epoch to the new one.
- Build a **deterministic Merkle root** with an empty leaf rule.
- Submit a **commit transaction** and ensure it is successful (with retries).

---

## 2. Timing & Rounds

### 2.1 Timing Configuration

Timing Configuration:

```go
function getOracleTimingConfig(uint64 referenceEpoch)
    returns (uint64 startEpoch, uint64 epochInterval);
```

- `startEpoch` – first epoch at which oracle commitments are defined.
- `epochInterval` – how many epochs between oracle rounds (must be > 0).

The client reads these values at startup from a configuration YAML.
The client should support a dynamic transition of configuration changes.

So given a configuration:
```yml
# Do not edit default values
- timing-config:
	- firstStartEpoch: x
	- firstInterval: a
	- secondStartEpoch: y
	- secondInterval: b
```

Then the following logic should be performed:
```python
if referenceEpoch >= y:
	return (y,b)
else:
	return (x,a)
```



### 2.2 Round & Target Epoch

The client maintains a `round` variable. To compute the target epoch for this round, use:

```go
function getTargetEpoch(round) {
	return startEpoch + round * epochInterval
}
```

The client should use this formula to find the `targetEpoch` associated with whatever `currentRound` it is currently working on.

---

## 3. Finalization & Data Source

### 3.1 Finalization Requirement

Every time data is polled for an `epoch` it must be finalized. Finality check can be done with a simple beacon api check: `/eth/v1/beacon/states/finalized/finality_checkpoints`.

If `epoch <= finalizedEpoch` then it is eligible for data polling. 

### 3.2 Data Sources

The client obtains `(clusterId, effectiveBalance)` for `epoch` from:

1. **SSV node API (primary)**
   e.g. a call conceptually like:
   ```text
   getEffectiveBalanceForEachClusters(targetEpoch)
   ```
   returning:
   ```json
   [
     { "clusterId": "0x...", "effectiveBalance": "123456" },
     ...
   ]
   ```

2. **Ethereum node (optional / verification / fallback)**
   - Reads SSV protocol contracts for:
     - Cluster registry.
     - Cluster effective balances at `targetEpoch` (or corresponding `referenceBlock`).
   - Can be used to verify a random subset of entries to detect misbehavior of SSV nodes.

Primary source of truth is **onchain SSV state**; SSV node is an index.

---

## 4. Data Model

### 4.1 Cluster Effective Balance

For each cluster `c`:

- `clusterId` – `bytes32` (canonical cluster identifier).
- `effectiveBalance` – integer (e.g. `uint64` / `uint256`), with agreed units (e.g. Gwei or ETH @marco ?). 

---

## 5. Merkle Tree Construction

### 5.1 Leaf Encoding


For each cluster:

`leaf_c = keccak256(abi.encode(clusterId, effectiveBalance))`

- `clusterId` encoded as `bytes32`. It should be caluclated like in the contract: `clusterId = keccak256(abi.encodePacked(msg.sender, operatorIds));`
- `effectiveBalance` encoded as fixed-width integer (`uint64`).

The exact encoding (types & order) is **part of the protocol** and must be identical across implementations.

### 5.2 Ordering

- Collect all leaves.
- Sort by `clusterId` ascending (as `bytes32`).
- Construct `leaves[]` in that order.

This guarantees deterministic leaf ordering.

### 5.3 Odd-Leaf Handling (Empty Leaf)

Simply duplicate the last leaf. Ensure OpenZepplin compatibility like in [the following library]( https://github.com/cbergoon/merkletree).

### 5.4 Parent Computation

Build a binary Merkle tree:

```text
parent = keccak256(left || right)
```

where `left` and `right` are 32-byte child hashes. The final single hash is `merkleRoot`.

---

## 6. Contract Interface

### 6.1 Commit Root

The oracle client calls the oracle contract:

```solidity
function commitRoot{
    bytes32 merkleRoot,
    uint64  blockNum,
) external;
```

- `merkleRoot` – Merkle root of all cluster effective balances for `targetEpoch`.
- `blockNum` – The blockNumber that maps to the checkpoint of the `targetEpoch`.

**Edge condition** - If all blocks in the epoch are missing, then skip the epoch by passing `merkleRoot = 0` and `blockNum = 0`


Contract responsibilities (out of scope for client):

- Require a **threshold** of oracle commits per `blockNum`.
- Perform **weighted majority** to decide the canonical root.
- Handle storage and further use of that root.

---

## 7. Client Architecture

### 7.1 Components

1. **Scheduler**
   - Triggers the main loop at a fixed wall-clock interval (e.g. every N seconds).
   - Ensures no overlapping runs.

2. **Config & Timing Manager**
   - Reads `startEpoch` and `epoch_interval` from the oracle contract.
   - Caches the values and refreshes them periodically or upon error.
   - Computes `(roundId, targetEpoch)` based on `finalizedEpoch` and config.

3. **Finalization & Epoch Manager**
   - Queries beacon/consensus RPC to get `finalizedEpoch`.
   - Checks `isFinalized(targetEpoch)` (or verifies `targetEpoch <= finalizedEpoch`).
   - Resolves `referenceBlock` (or slot) corresponding to `targetEpoch`.

4. **Data Fetcher**
   - Calls SSV node API to obtain `(clusterId, effectiveBalance)` for `targetEpoch`.
   - Optionally verifies a subset directly from SSV contracts via Ethereum RPC.

5. **Merkle Builder**
   - Normalizes, sorts, and encodes cluster data.
   - Applies empty-leaf rule.
   - Produces `merkleRoot`, and optionally a structure for proof generation.

6. **Onchain Client**
   - Ethereum RPC/websocket client.
   - ABI bindings for:
     - `getOracleTimingConfig`
     - `commitRoot`
   - Manages nonces, gas price (EIP-1559), chain ID, etc.
   - Tracks TX lifecycle and implements retry logic.

7. **Wallet / Key Management**
   - Local keystore / HSM / KMS / remote signer.
   - Signs EIP-1559 transactions.
   - Ensures private key is never exposed raw in logs/config.

8. **Persistence & Monitoring**
   - Local DB or KV store:
     - Last successfully committed `(roundId, targetEpoch, merkleRoot)`.
     - TX hashes and final statuses.
   - Logging + metrics:
     - Commit attempts, successes, failures, RPC errors, data mismatches.

### 7.2 Example Configuration

- Network:
  - `eth_rpc_url`
  - `beacon_rpc_url`
  - `ssv_node_rpc_url`
  - `oracle_contract_address`
- Wallet:
  - `keystore_path` or `private_key_env`
- TX policy:
  - `tx_inclusion_timeout_blocks`
  - `max_retry_attempts`
  - `gas_bump_factor` (e.g. 1.1)
  - `max_gas_price`
- Behavior:
  - `skip_if_root_unchanged` (optional optimization)

---

## 8. Protocol Flow (Per Loop)

1. **Fetch timing config**
   - Call `getOracleTimingConfig(lastTargetEpoch)` → `(startEpoch, epochInterval)`.
   - If `epochInterval == 0`, log error and abort (misconfiguration).

2. **Calculate Current Round**:
        a. Finding `latestFinalizedEpoch` from beacon node.
		    b. `if LatestFinalized<=initialEpoch: round = 0`
        c. Calculate `round = RoundUp((latestFinalized-initialEpoch)/epochInterval)`.

4. **Compute targetEpoch & roundId**
   - Compute:
     ```text
     targetEpoch = startEpoch + round * epochInterval
     ```
   - Check if `targetEpoch` is finalized via consensus node before proceeding.

5. **Idempotency check (already committed?)**
   - If epoch finalized, find the checkpoint's `BlockNum`
   - From local DB and/or onchain state, check:
     - If this oracle address already has a successful commit for a block number greater than or equal to `blockNum`.
   - If yes, abort this cycle (nothing to do).

   ```python
   if targetEpoch.Checkpoint.BlockNum <= committedBlockNum: return
   ```

6. **Fetch cluster balances**
   - For `targetEpoch` (finalized and calculated per round):
     - Get full list of `(clusterId, effectiveBalance)`.
     - Get `BlockNum` of finalized checkpoint.
   

7. **Build Merkle root**
   - Encode leaves as in section 5
   - Sort by `clusterId`.
   - If number of leaves is odd, append `emptyLeaf`.
   - Build Merkle tree and compute `merkleRoot`.


8. **Construct and sign TX**
   - Encode:
     ```solidity
     commitRoot(merkleRoot, targetEpoch, referenceBlockNum)
     ```
   - Estimate gas and set EIP-1559 parameters (maxFee, maxPriorityFee).
   - Sign with the oracle key.

9. **Broadcast TX**
    - Send TX to Ethereum node.
    - Persist `{round, targetEpoch, merkleRoot, referenceBlock, txHash, retryCount=0}` locally.

10. **Track TX and ensure success**
    - Poll for receipt until:
      - TX is mined, or
      - `tx_inclusion_timeout_blocks` reached.
    - If **status == 1** (success):
      - Mark `(blocknum, targetEpoch)` as successfully committed.
    - Else (reverted, dropped, or timeout):
      1. Check onchain if a commit for this oracle and `blockNum` already exists (in case the first TX was replaced by another one).
      2. If not committed and `retryCount < max_retry_attempts`:
         - Bump gas (e.g. multiply `maxFee` and/or `maxPriorityFee` by `gas_bump_factor`).
         - Resubmit a new TX and update `retryCount`.
      3. If still not committed after max retries:
         - Log permanent failure for this round. Wait for manual intervention. 

---

## 9. Security & Correctness Notes

- **Determinism**
  - `startEpoch`, `epoch_interval` are read from the same contract for all oracles.
  - `targetEpoch`, `round`, leaf encoding, sorting rule, and empty leaf definition must be globally agreed.
- **Finalization safety**
  - Using only epochs derived from `finalizedEpoch` avoids reorg issues.
- **Data correctness**
  - Onchain SSV contracts are ultimate source of truth.
  - SSV node data should be sanity-checked regularly.
- **Key management**
  - Keys should be stored and used via secure mechanisms (HSM, KMS, or encrypted keystores).
- **Liveness**
  - Retry + gas bump policy should be tuned so at least one TX from each oracle is likely to make it onchain per round.



# Cluster Updater

Cluster Updater is a separate role from the Effective Balance Oracle. 

It has the following flow:

1. Builds merkle trees like the oracle.
2. Listen to `RootCommitted(merkleRoot, blockNum, block.timestamp)` event and validate the correct `merkleRoot` is constructed for `blockNum`.
3. Call one of the following contract functions:
    1. Call `updateClusterBalance` per cluster in internal configuration.
    2. Call `BulkClustersBalancesUpdate` depending on internal configuration.

## Contract interface

### UpdateClusterBalance

The contract may support per-cluster updates:

```solidity
/**
 * @notice Update a cluster's effective balance and trigger index updates
 * @param blockNum RBlock number that matches a committed root
 * @param clusterOwner Owner address of the cluster
 * @param operatorIds Array of operator IDs in the cluster
 * @param cluster Current cluster state (provided by oracle)
 * @param effectiveBalance Total cluster EB in wei (sum of all validators)
 * @param merkleProof Merkle proof validating the EB value
 */
function UpdateClusterBalance(
    uint64 blockNum,
    address clusterOwner,
    uint64[] operatorIds,
    Cluster cluster,
    uint256 effectiveBalance,
    bytes32[] calldata merkleProof
) external;
```

The oracle must provide the contract with cluster data.
The contract is able to independently validate it.

The oracle client may optionally have tooling to generate Merkle proofs but is not required to do so. This feature will be useful for fee collectors.

## BulkClustersBalancesUpdate

TBD
```
