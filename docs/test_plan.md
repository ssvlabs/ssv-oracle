# Test Plan

---

## Package Coverage Summary

| Package | Test File | Unit Tests | Integration Tests | Status |
|---------|-----------|------------|-------------------|--------|
| `merkle` | `tree_test.go`, `encoding_test.go` | ✅ Complete | N/A | **Good** |
| `wallet` | `signer_test.go` | ✅ Complete | N/A | **Good** |
| `oracle` | **NONE** | ❌ Missing | ❌ Missing | **High Priority** |
| `txmanager` | **NONE** | ❌ Missing | ❌ Missing | **High Priority** |
| `updater` | **NONE** | ❌ Missing | ❌ Missing | **Medium Priority** |
| `contract` | `client_test.go` | ⚠️ Minimal | ❌ Missing | **Needs Work** |
| `pkg/ethsync` | Multiple | ⚠️ Partial | ⚠️ Broken | **Needs Fix** |

---

## Detailed Analysis: What's Missing & What to Delete

### merkle/encoding_test.go

| Function | Tested | Missing Tests | Notes |
|----------|--------|---------------|-------|
| `EncodeMerkleLeaf` | ✅ | None | Good coverage |

**Can Delete:**
- Remove commented-out TODO block (lines 66-74) - either add Solidity vectors or delete
- Remove `t.Logf` statements that don't assert anything (just noise)

### merkle/tree_test.go

| Function | Tested | Missing Tests | Notes |
|----------|--------|---------------|-------|
| `BuildMerkleTree` | ✅ | None | Excellent coverage |
| `BuildMerkleTreeWithProofs` | ✅ | None | Good |
| `GetProof` | ✅ | None | Good |

**Can Delete:**
- `// TODO: Verify against Solidity test` comments (lines 72, 97, 152) - resolve using real tx data below
- Excessive `t.Logf` in passing tests

**Solidity Verification - Real TX References:**
Use these Hoodi testnet transactions to extract real merkle roots and cluster data for cross-validation:
- https://hoodi.etherscan.io/tx/0x31353b67d33892875e9daefecd651722e6451bb238ccc787534e63bb46234e2d
- https://hoodi.etherscan.io/tx/0xaf3984181a18d3c614dc38b44b145167b4a085ff47bc049bda992f30e3759cf3

Extract from these:
1. Committed merkle root
2. Cluster IDs and effective balances (from UpdateClusterBalance calls)
3. Use as test vectors to verify Go implementation matches contract

### pkg/ethsync/types_test.go

| Function | Tested | Missing Tests | Notes |
|----------|--------|---------------|-------|
| `ComputeClusterID` | ✅ | Boundary values (max uint64 operator IDs) | Good |
| `Spec.SlotAt` | ✅ | None | Good |
| `Spec.EpochAtTimestamp` | ✅ | None | Good |

**Missing Tests:**
- `ComputeClusterID` with max uint64 operator IDs
- `ComputeClusterID` with empty operator slice

### pkg/ethsync/storage_test.go

| Function | Tested | Missing Tests | Notes |
|----------|--------|---------------|-------|
| `NewPostgresStorage` | ✅ | None | |
| `GetLastSyncedBlock` | ✅ | None | |
| `UpdateLastSyncedBlock` | ✅ | None | |
| `UpsertCluster` | ✅ | None | |
| `GetCluster` | ✅ | Not found case | |
| `InsertValidator` | ✅ | Invalid pubkey length | |
| `DeleteValidator` | ✅ | None | |
| `GetActiveValidators` | ✅ | Empty result | |
| `InsertPendingCommit` | ❌ | All | Uses wrong method name |
| `UpdateCommitStatus` | ❌ | All | Not tested |
| `GetCommitByBlock` | ❌ | All | Uses wrong method name |
| `ClearAllState` | ❌ | All | Not tested |
| `BeginTx` | ✅ | Rollback case | |

**Must Fix:**
- Line 348: `InsertOracleCommit` → `InsertPendingCommit`
- Line 353: `GetCommitByRound` → doesn't exist, delete or fix
- Line 367: `GetCommitByReferenceBlock` → `GetCommitByBlock`

**Can Delete:**
- `TestPostgresStorage_OracleCommit` - broken, needs rewrite

### pkg/ethsync/client_test.go

| Function | Tested | Missing Tests | Notes |
|----------|--------|---------------|-------|
| `NewExecutionClient` | ⚠️ | Proper constructor test | Only logs error |
| `ExecutionClientConfig` defaults | ⚠️ | Needs mock | Requires live node |
| `withRetry` | ❌ | All | Not tested |
| `GetFinalizedBlock` | ❌ | All | Needs mock |
| `FetchLogs` | ❌ | All | Needs mock |
| `packLogs` | ❌ | All | Not tested |

**Can Delete:**
- Current tests are essentially no-ops (log errors, don't fail)

### contract/client_test.go

| Function | Tested | Missing Tests | Notes |
|----------|--------|---------------|-------|
| ABI loading | ✅ | None | |
| `Cluster` struct | ✅ | None | Trivial |
| `CommitRoot` | ❌ | All | Skipped |
| `UpdateClusterBalance` | ❌ | All | Skipped |
| `GetClusterEffectiveBalance` | ❌ | All | Returns stub |

**Can Delete:**
- `TestCluster` - trivial, doesn't test anything useful
- `TestClient_PlaceholderForFutureTests` - placeholder with t.Skip

### wallet/signer_test.go

| Function | Tested | Missing Tests | Notes |
|----------|--------|---------------|-------|
| `NewEnvSigner` | ✅ | None | Excellent |
| `NewKeystoreSigner` | ✅ | None | Excellent |
| `NewSigner` (factory) | ✅ | None | Good |
| `Sign` | ✅ | None | Good |
| `Close` | ✅ | None | Good |

**No changes needed** - this is the best test file.

### oracle/ (NO TESTS)

| Function | Missing Tests |
|----------|---------------|
| `CommitPhase.TargetEpoch` | All |
| `ValidatePhases` | All |
| `GetPhaseForEpoch` | All |
| `NextTargetEpoch` | All - **Critical, spec-alignment** |
| `RoundInPhase` | All |
| `Oracle.Run` | Integration (mock beacon/storage) |
| `Oracle.processNextCommit` | Integration |
| `Oracle.waitForFinalization` | Unit with mock time |
| `Oracle.fetchClusterBalances` | Unit with mock |

### txmanager/ (NO TESTS)

| Function | Missing Tests |
|----------|---------------|
| `TxPolicy.Validate` | All |
| `TxPolicy.ParseMaxFeePerGas` | All |
| `TxManager.SendTransaction` | Integration (mock client) |
| `TxManager.getGasPrice` | Unit |
| `TxManager.waitForReceipt` | Unit with mock |
| `TxManager.cancelTx` | Unit with mock |
| `extractRevertReason` | All |
| `decodeErrorSelector` | All |
| `RevertError.Error` | All |
| `IsRevertError` | All |

### updater/ (NO TESTS)

| Function | Missing Tests | Notes |
|----------|---------------|-------|
| `New` | Constructor validation | Trivial, low priority |
| `Updater.Run` | Integration with mock | Complex, needs event subscription mock |
| `Updater.processCommit` | Unit with mock storage | Test root validation, cluster iteration |
| `Updater.processCluster` | Unit with mock contract | Test proof generation, balance check, tx submission |

**Key Test Scenarios:**
- `processCommit`: root mismatch detection
- `processCommit`: empty cluster list handling
- `processCluster`: cluster not found in storage
- `processCluster`: balance unchanged skip logic
- `processCluster`: proof generation and submission

### pkg/ethsync/syncer.go (NO TESTS)

| Function | Missing Tests |
|----------|---------------|
| `NewEventSyncer` | All |
| `SyncToFinalized` | Integration |
| `SyncToBlock` | Integration |
| `processBlockLogs` | Unit |
| `processLog` | Unit |
| `updateState` | Unit |
| `handleValidatorAdded` | Unit |
| `handleValidatorRemoved` | Unit |
| `upsertClusterFromEvent` | Unit |
| `computeClusterIDFromEvent` | Unit |

### pkg/ethsync/parser.go (NO TESTS)

| Function | Missing Tests |
|----------|---------------|
| `NewEventParser` | All |
| `ParseLog` | All |
| `parseValidatorAdded` | All |
| `parseValidatorRemoved` | All |
| `parseClusterLiquidated` | All |
| `parseClusterReactivated` | All |
| `parseClusterWithdrawn` | All |
| `parseClusterDeposited` | All |
| `parseClusterBalanceUpdated` | All |
| `EncodeEventToJSON` | All |
| `EncodeLogToJSON` | All |

### pkg/ethsync/beacon.go (NO TESTS)

| Function | Missing Tests |
|----------|---------------|
| `NewBeaconClient` | All |
| `GetSpec` | All (needs mock) |
| `GetFinalizedCheckpoint` | All (needs mock) |
| `GetFinalizedValidatorBalances` | All (needs mock) |

### contract/events.go (NO TESTS)

| Function | Missing Tests |
|----------|---------------|
| `SubscribeRootCommitted` | All (needs WS mock) |
| `parseRootCommittedEvent` | All |

---

## Summary: What to Delete

```
merkle/encoding_test.go:
  - Lines 66-74: Commented TODO block

merkle/tree_test.go:
  - Lines 72, 97, 152: TODO comments (resolve or remove)

pkg/ethsync/storage_test.go:
  - Lines 325-377: TestPostgresStorage_OracleCommit (broken)

pkg/ethsync/client_test.go:
  - Consider rewriting entirely (current tests don't fail on errors)

contract/client_test.go:
  - Lines 20-34: TestCluster (trivial)
  - Lines 36-44: TestClient_PlaceholderForFutureTests (placeholder)
```

---

## Current State

### Existing Tests

| Package | File | Coverage | Issues |
|---------|------|----------|--------|
| `merkle` | `tree_test.go` | ✅ Good | TODOs for Solidity verification |
| `merkle` | `encoding_test.go` | ✅ Good | TODOs for Solidity verification |
| `pkg/ethsync` | `types_test.go` | ✅ Good | None |
| `pkg/ethsync` | `storage_test.go` | ⚠️ Integration | Requires DB, may have stale method refs |
| `pkg/ethsync` | `client_test.go` | ⚠️ Minimal | Only constructor tests |
| `contract` | `client_test.go` | ⚠️ Minimal | Only ABI loading, skipped tests |
| `wallet` | `signer_test.go` | ✅ Good | None |

### Missing Tests

| Package | Files | Priority |
|---------|-------|----------|
| `oracle` | `timing.go`, `oracle.go` | **High** |
| `txmanager` | `manager.go`, `config.go` | **High** |
| `updater` | `updater.go` | Medium |
| `pkg/ethsync` | `syncer.go`, `parser.go`, `beacon.go` | Medium |

---

## Phase 1: Fix Existing Tests

### 1.1 Fix storage_test.go

**Issue:** References non-existent methods and has stale API.

```go
// Line 348: InsertOracleCommit doesn't exist - should be InsertPendingCommit
// Line 353: GetCommitByRound doesn't exist
// Line 367: GetCommitByReferenceBlock doesn't exist - should be GetCommitByBlock
```

**Action:** Update to match current storage.go API.

### 1.2 Remove TODOs or Add Test Vectors

**Files:** `merkle/encoding_test.go`, `merkle/tree_test.go`

**Issue:** Multiple `// TODO: Verify against Solidity test` comments.

**Options:**
1. Add hardcoded test vectors from Solidity (if available)
2. Remove TODOs and rely on determinism tests
3. Add cross-validation script that runs Foundry tests

**Recommendation:** Add test vectors if Solidity tests exist, otherwise remove TODOs.

---

## Phase 2: Add oracle Package Tests

### 2.1 timing_test.go (High Priority)

Test the commit phase and round calculation logic.

```go
package oracle

import "testing"

func TestCommitPhase_TargetEpoch(t *testing.T) {
    phase := CommitPhase{StartEpoch: 100, Interval: 10}

    tests := []struct {
        round    uint64
        expected uint64
    }{
        {0, 100},
        {1, 110},
        {5, 150},
    }

    for _, tt := range tests {
        got := phase.TargetEpoch(tt.round)
        if got != tt.expected {
            t.Errorf("TargetEpoch(%d) = %d, want %d", tt.round, got, tt.expected)
        }
    }
}

func TestValidatePhases(t *testing.T) {
    tests := []struct {
        name    string
        phases  []CommitPhase
        wantErr bool
    }{
        {
            name:    "valid single phase",
            phases:  []CommitPhase{{StartEpoch: 100, Interval: 10}},
            wantErr: false,
        },
        {
            name:    "valid multiple phases",
            phases:  []CommitPhase{{StartEpoch: 100, Interval: 10}, {StartEpoch: 200, Interval: 5}},
            wantErr: false,
        },
        {
            name:    "empty phases",
            phases:  []CommitPhase{},
            wantErr: true,
        },
        {
            name:    "zero interval",
            phases:  []CommitPhase{{StartEpoch: 100, Interval: 0}},
            wantErr: true,
        },
        {
            name:    "unsorted phases",
            phases:  []CommitPhase{{StartEpoch: 200, Interval: 10}, {StartEpoch: 100, Interval: 5}},
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidatePhases(tt.phases)
            if (err != nil) != tt.wantErr {
                t.Errorf("ValidatePhases() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestGetPhaseForEpoch(t *testing.T) {
    phases := []CommitPhase{
        {StartEpoch: 100, Interval: 10},
        {StartEpoch: 200, Interval: 5},
    }

    tests := []struct {
        epoch         uint64
        expectedStart uint64
    }{
        {50, 100},   // Before first phase -> returns first
        {100, 100},  // Exactly at first phase start
        {150, 100},  // Between phases
        {200, 200},  // Exactly at second phase start
        {300, 200},  // After second phase
    }

    for _, tt := range tests {
        phase := GetPhaseForEpoch(phases, tt.epoch)
        if phase.StartEpoch != tt.expectedStart {
            t.Errorf("GetPhaseForEpoch(%d) = phase starting at %d, want %d",
                tt.epoch, phase.StartEpoch, tt.expectedStart)
        }
    }
}

func TestNextTargetEpoch(t *testing.T) {
    phases := []CommitPhase{
        {StartEpoch: 100, Interval: 10},
    }

    tests := []struct {
        name           string
        finalizedEpoch uint64
        expected       uint64
    }{
        {"before start", 50, 100},
        {"at start", 100, 100},
        {"just after start", 101, 110},
        {"after first target finalized", 111, 120},
        {"mid interval", 115, 120},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := NextTargetEpoch(phases, tt.finalizedEpoch)
            if got != tt.expected {
                t.Errorf("NextTargetEpoch(%d) = %d, want %d",
                    tt.finalizedEpoch, got, tt.expected)
            }
        })
    }
}

func TestNextTargetEpoch_PhaseTransition(t *testing.T) {
    phases := []CommitPhase{
        {StartEpoch: 100, Interval: 10},
        {StartEpoch: 150, Interval: 5},
    }

    tests := []struct {
        name           string
        finalizedEpoch uint64
        expected       uint64
    }{
        {"before transition", 135, 140},
        {"at transition boundary", 145, 150}, // Should jump to new phase
        {"after transition", 155, 160},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := NextTargetEpoch(phases, tt.finalizedEpoch)
            if got != tt.expected {
                t.Errorf("NextTargetEpoch(%d) = %d, want %d",
                    tt.finalizedEpoch, got, tt.expected)
            }
        })
    }
}

func TestRoundInPhase(t *testing.T) {
    phase := CommitPhase{StartEpoch: 100, Interval: 10}

    tests := []struct {
        targetEpoch uint64
        expected    uint64
    }{
        {100, 0},
        {110, 1},
        {120, 2},
        {150, 5},
        {50, 0}, // Before phase start
    }

    for _, tt := range tests {
        got := RoundInPhase(phase, tt.targetEpoch)
        if got != tt.expected {
            t.Errorf("RoundInPhase(%d) = %d, want %d", tt.targetEpoch, got, tt.expected)
        }
    }
}
```

---

## Phase 3: Add txmanager Package Tests

### 3.1 config_test.go

```go
package txmanager

import (
    "testing"
    "time"
)

func TestTxPolicy_Validate(t *testing.T) {
    tests := []struct {
        name    string
        policy  *TxPolicy
        wantErr bool
    }{
        {
            name: "valid policy",
            policy: &TxPolicy{
                GasBufferPercent:     20,
                MaxFeePerGas:         "100 gwei",
                PendingTimeoutBlocks: 10,
                GasBumpPercent:       10,
                MaxRetries:           3,
                RetryDelay:           5 * time.Second,
            },
            wantErr: false,
        },
        {
            name: "gas bump too low",
            policy: &TxPolicy{
                GasBufferPercent:     20,
                MaxFeePerGas:         "100 gwei",
                PendingTimeoutBlocks: 10,
                GasBumpPercent:       5, // Must be >= 10 for EIP-1559
                MaxRetries:           3,
                RetryDelay:           5 * time.Second,
            },
            wantErr: true,
        },
        {
            name: "zero retries",
            policy: &TxPolicy{
                GasBufferPercent:     20,
                MaxFeePerGas:         "100 gwei",
                PendingTimeoutBlocks: 10,
                GasBumpPercent:       10,
                MaxRetries:           0,
                RetryDelay:           5 * time.Second,
            },
            wantErr: true,
        },
        {
            name: "missing max fee",
            policy: &TxPolicy{
                GasBufferPercent:     20,
                PendingTimeoutBlocks: 10,
                GasBumpPercent:       10,
                MaxRetries:           3,
            },
            wantErr: true,
        },
        {
            name: "gas buffer too high",
            policy: &TxPolicy{
                GasBufferPercent:     150, // Max is 100
                MaxFeePerGas:         "100 gwei",
                PendingTimeoutBlocks: 10,
                GasBumpPercent:       10,
                MaxRetries:           3,
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.policy.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}

func TestTxPolicy_ParseMaxFeePerGas(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected int64 // in wei
        wantErr  bool
    }{
        {"100 gwei", "100 gwei", 100_000_000_000, false},
        {"1 gwei", "1 gwei", 1_000_000_000, false},
        {"0.5 gwei", "0.5 gwei", 500_000_000, false},
        {"1 wei", "1 wei", 1, false},
        {"plain number as wei", "1000000000", 1_000_000_000, false},
        {"invalid format", "invalid", 0, true},
        {"empty string", "", 0, false}, // Empty returns nil, no error
        {"unknown unit", "100 foo", 0, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            policy := &TxPolicy{MaxFeePerGas: tt.input}
            got, err := policy.ParseMaxFeePerGas()
            if (err != nil) != tt.wantErr {
                t.Errorf("ParseMaxFeePerGas() error = %v, wantErr %v", err, tt.wantErr)
            }
            if !tt.wantErr && got != nil && got.Int64() != tt.expected {
                t.Errorf("ParseMaxFeePerGas() = %d, want %d", got.Int64(), tt.expected)
            }
        })
    }
}
```

### 3.2 manager_test.go

```go
package txmanager

import (
    "fmt"
    "strings"
    "testing"
)

func TestExtractRevertReason(t *testing.T) {
    // Set up error selectors for testing
    SetErrorSelectors(map[string]string{
        "12345678": "CustomError()",
    })

    tests := []struct {
        name     string
        errMsg   string
        expected string
    }{
        {
            name:     "standard revert message",
            errMsg:   "execution reverted: insufficient balance",
            expected: "insufficient balance",
        },
        {
            name:     "known error selector",
            errMsg:   "execution reverted: 0x12345678",
            expected: "CustomError()",
        },
        {
            name:     "unknown error",
            errMsg:   "some other error",
            expected: "some other error",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := fmt.Errorf(tt.errMsg)
            got := extractRevertReason(err)
            if got != tt.expected {
                t.Errorf("extractRevertReason() = %q, want %q", got, tt.expected)
            }
        })
    }
}

func TestRevertError(t *testing.T) {
    t.Run("simulated revert", func(t *testing.T) {
        err := &RevertError{Reason: "test reason", Simulated: true}
        if !strings.Contains(err.Error(), "call reverted") {
            t.Errorf("Expected 'call reverted' in error, got %q", err.Error())
        }
    })

    t.Run("tx revert with hash", func(t *testing.T) {
        err := &RevertError{Reason: "test reason", TxHash: "0x1234567890"}
        if !strings.Contains(err.Error(), "0x12345678") {
            t.Errorf("Expected truncated tx hash in error, got %q", err.Error())
        }
    })

    t.Run("revert without hash", func(t *testing.T) {
        err := &RevertError{Reason: "test reason"}
        if !strings.Contains(err.Error(), "reverted: test reason") {
            t.Errorf("Expected 'reverted: test reason' in error, got %q", err.Error())
        }
    })
}

func TestIsRevertError(t *testing.T) {
    t.Run("is revert error", func(t *testing.T) {
        revertErr := &RevertError{Reason: "test"}
        got, ok := IsRevertError(revertErr)
        if !ok {
            t.Error("Expected IsRevertError to return true for RevertError")
        }
        if got.Reason != "test" {
            t.Errorf("Expected reason 'test', got %q", got.Reason)
        }
    })

    t.Run("wrapped revert error", func(t *testing.T) {
        revertErr := &RevertError{Reason: "inner"}
        wrappedErr := fmt.Errorf("outer: %w", revertErr)
        got, ok := IsRevertError(wrappedErr)
        if !ok {
            t.Error("Expected IsRevertError to return true for wrapped RevertError")
        }
        if got.Reason != "inner" {
            t.Errorf("Expected reason 'inner', got %q", got.Reason)
        }
    })

    t.Run("not revert error", func(t *testing.T) {
        normalErr := fmt.Errorf("normal error")
        _, ok := IsRevertError(normalErr)
        if ok {
            t.Error("Expected IsRevertError to return false for normal error")
        }
    })
}

func TestDecodeErrorSelector(t *testing.T) {
    SetErrorSelectors(map[string]string{
        "aabbccdd": "KnownError()",
    })

    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"known selector", "0xaabbccdd", "KnownError()"},
        {"unknown selector", "0x11223344", "0x11223344"},
        {"not a selector", "plain text", "plain text"},
        {"too short", "0x1234", "0x1234"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := decodeErrorSelector(tt.input)
            if got != tt.expected {
                t.Errorf("decodeErrorSelector(%q) = %q, want %q", tt.input, got, tt.expected)
            }
        })
    }
}
```

---

## Phase 4: Add ethsync Package Tests

### 4.1 parser_test.go

```go
package ethsync

import (
    "testing"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/types"
)

func TestEventParser_ParseLog(t *testing.T) {
    parser, err := NewEventParser()
    if err != nil {
        t.Fatalf("NewEventParser() error = %v", err)
    }

    t.Run("empty topics", func(t *testing.T) {
        log := &types.Log{Topics: []common.Hash{}}
        _, _, err := parser.ParseLog(log)
        if err == nil {
            t.Error("Expected error for empty topics")
        }
    })

    t.Run("unknown event signature", func(t *testing.T) {
        log := &types.Log{
            Topics: []common.Hash{common.HexToHash("0x1234")},
        }
        _, _, err := parser.ParseLog(log)
        if err == nil {
            t.Error("Expected error for unknown event signature")
        }
    })
}

func TestEncodeLogToJSON(t *testing.T) {
    log := &types.Log{
        Address: common.HexToAddress("0x1234567890123456789012345678901234567890"),
        Topics: []common.Hash{
            common.HexToHash("0xabc"),
        },
        Data: []byte{0x01, 0x02, 0x03},
    }

    result, err := EncodeLogToJSON(log)
    if err != nil {
        t.Fatalf("EncodeLogToJSON() error = %v", err)
    }

    if len(result) == 0 {
        t.Error("Expected non-empty JSON")
    }
}
```

### 4.2 syncer_test.go (Unit tests with mocks)

```go
package ethsync

import (
    "context"
    "testing"
)

// mockStorage implements the storage interface for testing
type mockStorage struct {
    lastSyncedBlock uint64
    // ... other fields
}

func (m *mockStorage) GetLastSyncedBlock(ctx context.Context) (uint64, error) {
    return m.lastSyncedBlock, nil
}

// ... implement other interface methods

func TestEventSyncer_NewEventSyncer(t *testing.T) {
    t.Run("nil spec", func(t *testing.T) {
        cfg := EventSyncerConfig{
            Spec: nil,
        }
        _, err := NewEventSyncer(cfg)
        if err == nil {
            t.Error("Expected error for nil spec")
        }
    })
}
```

---

## Phase 5: Add updater Package Tests

### 5.1 updater_test.go

```go
package updater

import (
    "bytes"
    "context"
    "fmt"
    "math/big"
    "testing"

    "github.com/ethereum/go-ethereum/core/types"

    "ssv-oracle/merkle"
    "ssv-oracle/pkg/ethsync"
)

// mockStorage implements the storage interface for testing.
type mockStorage struct {
    clusters map[string]*ethsync.ClusterRow
    commits  map[uint64]*ethsync.OracleCommit
}

func newMockStorage() *mockStorage {
    return &mockStorage{
        clusters: make(map[string]*ethsync.ClusterRow),
        commits:  make(map[uint64]*ethsync.OracleCommit),
    }
}

func (m *mockStorage) GetCluster(ctx context.Context, clusterID []byte) (*ethsync.ClusterRow, error) {
    return m.clusters[string(clusterID)], nil
}

func (m *mockStorage) GetCommitByBlock(ctx context.Context, blockNum uint64) (*ethsync.OracleCommit, error) {
    return m.commits[blockNum], nil
}

// mockContractClient implements contract client interface for testing.
type mockContractClient struct {
    effectiveBalances map[[32]byte]uint64
    updateCalls       []updateCall
    updateErr         error
}

type updateCall struct {
    ClusterID        [32]byte
    EffectiveBalance uint64
}

func (m *mockContractClient) GetClusterEffectiveBalance(ctx context.Context, clusterID [32]byte) (uint64, error) {
    return m.effectiveBalances[clusterID], nil
}

func (m *mockContractClient) UpdateClusterBalance(ctx context.Context, blockNum uint64, owner interface{}, operatorIDs []uint64, cluster interface{}, balance *big.Int, proof [][32]byte) (*types.Receipt, error) {
    if m.updateErr != nil {
        return nil, m.updateErr
    }
    var id [32]byte
    // Would need to compute cluster ID here in real test
    m.updateCalls = append(m.updateCalls, updateCall{
        ClusterID:        id,
        EffectiveBalance: balance.Uint64(),
    })
    return &types.Receipt{
        TxHash:      [32]byte{0x01},
        BlockNumber: big.NewInt(int64(blockNum)),
    }, nil
}

func TestProcessCommit_EmptyClusters(t *testing.T) {
    storage := newMockStorage()
    u := &Updater{storage: storage}

    commit := &ethsync.OracleCommit{
        RoundID:         1,
        TargetEpoch:     100,
        MerkleRoot:      merkle.BuildMerkleTree(nil)[:], // Empty tree root
        ReferenceBlock:  1000,
        ClusterBalances: nil, // Empty
    }

    err := u.processCommit(context.Background(), commit)
    if err != nil {
        t.Errorf("processCommit() with empty clusters should not error, got: %v", err)
    }
}

func TestProcessCommit_RootMismatch(t *testing.T) {
    storage := newMockStorage()
    u := &Updater{storage: storage}

    clusterBalances := []ethsync.ClusterBalance{
        {ClusterID: make([]byte, 32), EffectiveBalance: 32000000000},
    }

    commit := &ethsync.OracleCommit{
        RoundID:         1,
        TargetEpoch:     100,
        MerkleRoot:      make([]byte, 32), // Wrong root (all zeros)
        ReferenceBlock:  1000,
        ClusterBalances: clusterBalances,
    }

    err := u.processCommit(context.Background(), commit)
    if err == nil {
        t.Error("processCommit() should error on root mismatch")
    }
    if err != nil && !bytes.Contains([]byte(err.Error()), []byte("root mismatch")) {
        t.Errorf("Expected 'root mismatch' error, got: %v", err)
    }
}

func TestProcessCommit_ValidRoot(t *testing.T) {
    storage := newMockStorage()

    // Create a cluster
    clusterID := [32]byte{0x01}
    storage.clusters[string(clusterID[:])] = &ethsync.ClusterRow{
        ClusterID:      clusterID[:],
        OwnerAddress:   make([]byte, 20),
        OperatorIDs:    []uint64{1, 2, 3, 4},
        ValidatorCount: 1,
        IsActive:       true,
        Balance:        big.NewInt(1000),
    }

    // Build tree with same data
    clusterMap := map[[32]byte]uint64{
        clusterID: 32000000000,
    }
    tree := merkle.BuildMerkleTreeWithProofs(clusterMap)

    clusterBalances := []ethsync.ClusterBalance{
        {ClusterID: clusterID[:], EffectiveBalance: 32000000000},
    }

    commit := &ethsync.OracleCommit{
        RoundID:         1,
        TargetEpoch:     100,
        MerkleRoot:      tree.Root[:],
        ReferenceBlock:  1000,
        ClusterBalances: clusterBalances,
    }

    // Note: This test is incomplete because we can't easily mock contractClient
    // In a real implementation, we'd inject the mock via dependency injection
    _ = storage
    _ = commit
    t.Skip("Needs contract client mock injection")
}
```

---

## Phase 6: Integration Tests

### 6.1 Update storage_test.go

Fix broken method references and ensure tests match current API.

### 6.2 Add Makefile target

```makefile
test-unit: ## Run unit tests
	go test ./... -short

test-integration: ## Run integration tests (requires DB)
	go test ./... -tags=integration

test-all: ## Run all tests
	go test ./... -tags=integration
```

---

## Implementation Checklist

### Phase 1: Fix Existing Tests
- [ ] Fix `storage_test.go` method references (InsertOracleCommit → InsertPendingCommit, etc.)
- [ ] Add Solidity test vectors to merkle tests (from Hoodi tx references)
- [ ] Remove stale TODO comments in merkle tests
- [ ] Delete broken/trivial tests in contract/client_test.go

### Phase 2: Oracle Tests (HIGH PRIORITY)
- [ ] Create `oracle/timing_test.go`
- [ ] Add `TestCommitPhase_TargetEpoch`
- [ ] Add `TestValidatePhases` (empty, zero interval, unsorted)
- [ ] Add `TestGetPhaseForEpoch` (before, at, between, after phases)
- [ ] Add `TestNextTargetEpoch` (before start, at start, mid-interval)
- [ ] Add `TestNextTargetEpoch_PhaseTransition`
- [ ] Add `TestRoundInPhase`

### Phase 3: TxManager Tests (HIGH PRIORITY)
- [ ] Create `txmanager/config_test.go`
- [ ] Add `TestTxPolicy_Validate` (valid, invalid gas bump, zero retries, etc.)
- [ ] Add `TestTxPolicy_ParseMaxFeePerGas` (gwei, wei, invalid, empty)
- [ ] Create `txmanager/manager_test.go`
- [ ] Add `TestExtractRevertReason`
- [ ] Add `TestRevertError` (simulated, with hash, without hash)
- [ ] Add `TestIsRevertError` (direct, wrapped, not revert)
- [ ] Add `TestDecodeErrorSelector`

### Phase 4: EthSync Tests
- [ ] Create `pkg/ethsync/parser_test.go`
- [ ] Add `TestEventParser_ParseLog` (empty topics, unknown signature)
- [ ] Add `TestEncodeLogToJSON`

### Phase 5: Updater Tests (MEDIUM PRIORITY)
- [ ] Create `updater/updater_test.go`
- [ ] Add `TestProcessCommit_EmptyClusters`
- [ ] Add `TestProcessCommit_RootMismatch`
- [ ] Add mock interfaces for storage and contract client

### Phase 6: Integration Tests
- [ ] Fix storage integration tests to match current API
- [ ] Add Makefile test targets (test-unit, test-integration, test-all)

---

## Best Practices Applied

### Go Testing Best Practices

| Practice | Description | Applied In |
|----------|-------------|------------|
| **Table-driven tests** | Use `[]struct{...}` for multiple test cases | All test files |
| **Subtests** | Use `t.Run()` for named test cases | All test functions |
| **Test naming** | `TestFunctionName_Scenario` pattern | All tests |
| **Error paths** | Test both success and failure cases | All validation tests |
| **Mocking** | Use interfaces for dependency injection | updater, oracle tests |
| **Build tags** | `//go:build integration` for DB tests | storage_test.go |
| **Parallel tests** | `t.Parallel()` for independent tests | Where safe |
| **Test helpers** | Extract common setup to helpers | Mock factories |

### Testing Philosophy

1. **Unit tests first** - Test pure logic without external dependencies
2. **Mock external services** - Ethereum RPC, Beacon API, PostgreSQL
3. **Test boundaries** - Focus on input validation and edge cases
4. **Deterministic tests** - No flaky tests, no time-based failures
5. **Fast feedback** - Unit tests should run in < 1 second
6. **Real data validation** - Use Hoodi testnet tx data for cross-validation

### Code Quality Standards

- **100% coverage on critical paths** - merkle tree, timing calculations
- **Regression tests** - Add test for every bug fix
- **Documentation** - Test names should describe expected behavior
- **No magic numbers** - Use named constants in test data

### Alignment with Industry Standards

| Standard | Implementation |
|----------|----------------|
| **OpenZeppelin Merkle** | Tree tests verify OZ compatibility |
| **EIP-1559** | TxPolicy tests verify 10% minimum bump |
| **Go testing conventions** | t.Run, table tests, subtests |
| **Error wrapping** | Use `%w` for error chains, test with `errors.As` |
