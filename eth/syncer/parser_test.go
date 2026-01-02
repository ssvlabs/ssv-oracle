package syncer

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestNewParser(t *testing.T) {
	parser := newParser()
	if parser == nil {
		t.Fatal("newParser() returned nil")
	}
}

func TestParseLog_EmptyTopics(t *testing.T) {
	parser := newParser()

	log := &types.Log{
		Topics: []common.Hash{},
	}

	_, _, err := parser.parseLog(log)
	if err == nil {
		t.Error("parseLog() should error on empty topics")
	}
}

func TestParseLog_UnknownSignature(t *testing.T) {
	parser := newParser()

	log := &types.Log{
		Topics: []common.Hash{
			common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		},
	}

	_, _, err := parser.parseLog(log)
	if err == nil {
		t.Error("parseLog() should error on unknown event signature")
	}
	if !errors.Is(err, errUnknownEvent) {
		t.Errorf("parseLog() error should wrap errUnknownEvent, got: %v", err)
	}
}

func TestParseLog_MissingOwnerTopic(t *testing.T) {
	parser := newParser()

	// ValidatorAdded signature but no owner topic
	log := &types.Log{
		Topics: []common.Hash{
			eventSigValidatorAdded,
		},
		Data: []byte{},
	}

	_, _, err := parser.parseLog(log)
	if err == nil {
		t.Error("parseLog() should error on missing owner topic")
	}
}

func TestEventSignatures(t *testing.T) {
	// Verify event signatures are non-zero and unique
	signatures := []common.Hash{
		eventSigValidatorAdded,
		eventSigValidatorRemoved,
		eventSigClusterLiquidated,
		eventSigClusterReactivated,
		eventSigClusterWithdrawn,
		eventSigClusterDeposited,
		eventSigClusterMigratedToETH,
		eventSigClusterBalanceUpdated,
	}

	seen := make(map[common.Hash]bool)
	for i, sig := range signatures {
		if sig == (common.Hash{}) {
			t.Errorf("Signature %d is zero", i)
		}
		if seen[sig] {
			t.Errorf("Signature %d is duplicate: %s", i, sig.Hex())
		}
		seen[sig] = true
	}
}

func TestEventSignatures_MatchABI(t *testing.T) {
	// Verify our hardcoded event signatures match the ABI
	// This catches ABI changes that would break event parsing
	parser := newParser()

	tests := []struct {
		name     string
		expected common.Hash
	}{
		{eventValidatorAdded, eventSigValidatorAdded},
		{eventValidatorRemoved, eventSigValidatorRemoved},
		{eventClusterLiquidated, eventSigClusterLiquidated},
		{eventClusterReactivated, eventSigClusterReactivated},
		{eventClusterWithdrawn, eventSigClusterWithdrawn},
		{eventClusterDeposited, eventSigClusterDeposited},
		{eventClusterMigratedToETH, eventSigClusterMigratedToETH},
		{eventClusterBalanceUpdated, eventSigClusterBalanceUpdated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, ok := parser.abi.Events[tt.name]
			if !ok {
				t.Fatalf("Event %s not found in ABI", tt.name)
			}

			if event.ID != tt.expected {
				t.Errorf("Event %s signature mismatch:\n  ABI:      %s\n  hardcoded: %s",
					tt.name, event.ID.Hex(), tt.expected.Hex())
			}
		})
	}
}
