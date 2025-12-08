# SSV Staking

We introduce SSV staking V1, a brand new feature that will allow SSV stakers to perform useful work for the SSV network in exchange for rewards.
Stakers will run oracle services that will report the correct effective balance (EB) of each SSV cluster to the SSV Network contract.
This will ensure that all fees are calculated proportionally to the expected rewards of the cluster.

Since updating all clusters is a costly operation, we divide the work between 2 actors:
1. *SSV Oracles* - that can only participate in behalf of stakers. They post a single small commitment of the effective balances of **all** clusters in each phase.
2. *Cluster Updaters* - Permissionless parties that post the actual verifiable EBs. Any data that won't be verified against the commitment will be rejected.

Cluster updaters will only be able to vote on commits that gained some threshold of votes.
The parties will be incentivized to act in a honest manner. The incentives will come from network fees collected from cluster owners and pooled in the SSV Network contract.
Each staker will be able to withdraw its relative part according to the weight of its correct votes.

## V1

In the first release we focus on simplicity:
  - There will only be 4 SSV oracles. 
  - One of the oracles will volunteer to be a Cluster Updater.
  - Stakers will delegate stake to all of them at once. Meaning all oracles will have the same weight.
  - A threshold of 75% of the weight will allow the commitment to be accepted.
  - Stakers will be able to withdraw amount proportional to their stake.
 


## 1. Summary

This document specifies the **offchain oracle client** that periodically publishes a Merkle root of **effective balances of all SSV clusters** to an onchain oracle contract.

The client will:

- Read **timing configuration** (`startEpoch`, `epochInterval`) from a shared oracle timing configuration source.
- Determine the **current target epoch** and corresponding **round**.
- Ensure the **target epoch is finalized**.
- Fetch effective balances for all clusters at the **target epoch**.
- Build a **deterministic Merkle root** with an empty-leaf rule.
- Submit a **commit transaction** and ensure it is successful (with retries).
- Optionally **submit cluster effective balances** directly to the contract.

---

## 2. Timing & Rounds

### 2.1 Timing Configuration

Timing Configuration:

```
Procedure getOracleTimingConfig(referenceEpoch) returns (startEpoch, epochInterval);
```

- `startEpoch` – first epoch at which oracle commitments are defined.
- `epochInterval` – number of epochs between oracle rounds (must be > 0).

The client obtains these values (for example, from an onchain contract or shared configuration) and MUST support dynamic configuration transitions.

For example, given a configuration with algebraic value placeholders:
```yml
# Do not edit default values
- timing-config:
	- firstStartEpoch: x
	- firstInterval: a
	- secondStartEpoch: y
	- secondInterval: b
```

the configuration function behaves as:
```python
if referenceEpoch >= y:
	return (y,b)
else:
	return (x,a)
```



### 2.2 Round & Target Epoch

The client maintains a `round` variable. To compute the target epoch for this round, use:

```text
Procedure getTargetEpoch() {
	targetEpoch = startEpoch + round * epochInterval
  if targetEpoch >= secondStartEpoch:
    targetEpoch = secondStartEpoch
    round = 0
    secondStartEpoch = inf

 return targetEpoch
}
```

After each successful commit for a given `round`, the client increments `round` by 1.

---

## 3. Finalization & Data Source

### 3.1 Finalization Requirement

Every time data is polled for an `epoch` it MUST be finalized. Finality can be checked via the beacon API: `/eth/v1/beacon/states/head/finality_checkpoints`.

If `epoch <= finalizedEpoch` then it is eligible for data polling.
Only epochs calculated as targets will be polled.

### 3.2 Data Sources

The client obtains `(clusterId, effectiveBalance)` for `epoch` from an Ethereum node:
   - Syncs SSV network events to build the mapping from validators to clusters.
   - Fetches the effective balance for SSV validators via `GET /eth/v1/beacon/states/{target_epoch_checkpoint_hash}/validators` API call.

---

## 4. Data Model

### 4.1 Cluster Effective Balance

For each cluster `c`:

- `clusterId` – `bytes32` (canonical cluster identifier).
- `effectiveBalance` – integer `uint64` representing units in gwei.

---

## 5. Merkle Tree Construction

A Merkle tree must be constructed so it is compatible with the logic used by [OpenZeppelin Merkle Tree](https://docs.openzeppelin.com/contracts-cairo/alpha/api/merkle-tree).

### 5.1 Leaf Encoding

For each cluster:

`leaf_c = keccak256(abi.encode(clusterId, effectiveBalance))`

- `clusterId` encoded as `bytes32`. It should be calculated as in the contract: `clusterId = keccak256(abi.encodePacked(msg.sender, operatorIds));`
- `effectiveBalance` encoded as fixed-width integer (`uint64`).

The exact encoding (types & order) is **part of the protocol** and MUST be identical across implementations and the onchain contract.

### 5.2 Ordering

- Collect all leaves.
- Sort by `clusterId` ascending (as `bytes32`).
- Construct `leaves[]` in that order.

This ordering guarantees a deterministic Merkle tree.

### 5.3 Empty Tree
If there are zero clusters, the Merkle root is defined as:
`merkleRoot = keccak256([]byte{})`
(i.e., keccak256 of zero bytes, resulting in `0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470`).


---

## 6. Contract Interface

### 6.1 Commit Root

The oracle client calls the oracle contract:

```solidity
function commitRoot(
    bytes32 merkleRoot,
    uint64  blockNum
) external;
```
It fires upon success:
```solidity
event RootCommitted(bytes32 merkleRoot, uint64 blockNum);
```

- `merkleRoot` – Merkle root of all cluster effective balances for `targetEpoch`.
- `blockNum` – The blockNumber that maps to the checkpoint of the `targetEpoch`.


Contract responsibilities:

- Require a **threshold** of oracle commits per `blockNum`.
- Handle storage and further use of that root.


### 6.2 UpdateClusterBalance

The contract supports per-cluster updates:

```solidity
/**
 * @notice Update a cluster's effective balance and trigger index updates
 * @param blockNum Block number that matches a committed root
 * @param clusterOwner Owner address of the cluster
 * @param operatorIds Array of operator IDs in the cluster
 * @param cluster Current cluster state (provided by oracle)
 * @param effectiveBalance Total cluster effective balance in wei (sum of all validators)
 * @param merkleProof Merkle proof validating the EB value
 */
function UpdateClusterBalance(
    uint64 blockNum,
    address clusterOwner,
    uint64[] operatorIds,
    Cluster cluster,
    uint64 effectiveBalance,
    bytes32[] calldata merkleProof
) external;
```

The client must provide the contract with cluster data.
The contract is able to independently validate it.

The client shall have tooling to generate Merkle proofs. This feature will be useful for fee collectors, but not all oracle clients will use it.

---

## 7. Client Architecture

### 7.1 Components

1. **Scheduler**
   - Triggers the main loop at a fixed wall-clock interval.
   - Ensures no overlapping runs.

2. **Config & Timing Manager**
   - Reads `startEpoch` and `epochInterval` from the configuration source.
   - Caches the values and refreshes them periodically or upon error.
   - Computes `(round, targetEpoch)` according to the configured timing rules.

3. **Finalization & Epoch Manager**
   - Queries beacon/consensus RPC to get `finalizedEpoch`.
   - Checks `isFinalized(targetEpoch)` (or verifies `targetEpoch <= finalizedEpoch`).
   - Resolves `referenceBlock` (or slot) corresponding to the Ethereum checkpoint of `targetEpoch`.

4. **Data Fetcher**
   - Syncs SSV contract events in order to reconstruct cluster data.
   - Calls the beacon node API to calculate `(clusterId, effectiveBalance)` for `targetEpoch`.

5. **Merkle Builder**
   - Sorts and encodes cluster data.
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
   - Ensures the private key is never exposed in raw form in logs or configuration.

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
  - `ssv_network_contract_address`
- Wallet:
  - `keystore_path` or `private_key_env`
- TX policy:
  - `tx_inclusion_timeout_blocks`
  - `max_retry_attempts`
  - `gas_bump_factor` (e.g. 1.1)
  - `max_gas_price`


---

## 8. Protocol Flow (Per Loop)

1. **Fetch timing config**
   - Call `getOracleTimingConfig(lastTargetEpoch)` → `(startEpoch, epochInterval)`.
   - If `epochInterval == 0`, log an error and abort (misconfiguration).

2. **Calculate current round**
   - If can not be fetched from memory:
    - Obtain `latestFinalizedEpoch` from the beacon node.
    - Compute `round = ceil((latestFinalizedEpoch - startEpoch) / epochInterval)`.

3. **Compute targetEpoch & roundId**
   - Compute:
     ```text
     targetEpoch = startEpoch + round * epochInterval
     ```
   - Check if `targetEpoch` is finalized via consensus node before proceeding.

4. **Idempotency check (already committed?)**
   - If epoch finalized, find the checkpoint's `BlockNum`
   - From local DB and/or onchain state, check:
     - If this oracle address already has a successful commit for a block number greater than or equal to `blockNum`.
   - If yes, abort this cycle (nothing to do).

   ```python
   if targetEpoch.Checkpoint.BlockNum <= committedBlockNum: return
   ```

5. **Fetch cluster balances**
   - For `targetEpoch` (finalized and calculated per round):
     - Get full list of `(clusterId, effectiveBalance)`.
     - Get `BlockNum` of finalized checkpoint.
   

6. **Build Merkle root**
   - Encode and sort as in section 5

7. **Construct and sign TX**
   - Call `commitRoot`.
   - Estimate gas and set EIP-1559 parameters (maxFee, maxPriorityFee).
   - Sign with the oracle key.   

8. **Broadcast TX**
    - Send TX to Ethereum node.
    - Persist `{round, targetEpoch, merkleRoot, referenceBlock, txHash, retryCount=0}` locally.

9. **Track TX and ensure success**
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

10. **Optional: update cluster balance**
    - Listen to the `RootCommitted(merkleRoot, blockNum, block.timestamp)` event and validate the correct `merkleRoot` is constructed for `blockNum`.
    - Call `UpdateClusterBalance` per cluster in internal configuration.
    - Use the same practices as in steps 7–9 to ensure a successful transaction.

---

## 9. Security & Correctness Notes

- **Determinism**
  - `startEpoch`, `epochInterval` are read from the same source for all oracles.
  - `targetEpoch`, `round`, leaf encoding, sorting rule, and empty leaf definition must be globally agreed.
- **Finalization safety**
  - Using only finalized epochs avoids reorg issues.
- **Data correctness**
  - Ultimate source of truth: onchain validator balances alongside SSV contract data and events.
- **Key management**
  - Keys should be stored and used via secure mechanisms (HSM, KMS, or encrypted keystores).
- **Liveness**
  - Retry + gas bump policy should be tuned so at least one TX from each oracle is likely to make it onchain per round.
