package syncer

import (
	"encoding/json"
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

func TestEncodeLogToJSON(t *testing.T) {
	log := &types.Log{
		Address: common.HexToAddress("0x1234567890123456789012345678901234567890"),
		Topics: []common.Hash{
			common.HexToHash("0xabc"),
			common.HexToHash("0xdef"),
		},
		Data: []byte{0x01, 0x02, 0x03},
	}

	result, err := encodeLogToJSON(log)
	if err != nil {
		t.Fatalf("encodeLogToJSON() error = %v", err)
	}

	if len(result) == 0 {
		t.Error("Expected non-empty JSON")
	}

	// Verify JSON structure
	var decoded map[string]any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if decoded["address"] != log.Address.Hex() {
		t.Errorf("Address mismatch: expected %s, got %v", log.Address.Hex(), decoded["address"])
	}

	topics, ok := decoded["topics"].([]any)
	if !ok {
		t.Fatal("Topics should be an array")
	}
	if len(topics) != 2 {
		t.Errorf("Expected 2 topics, got %d", len(topics))
	}
}

func TestEncodeLogToJSON_EmptyLog(t *testing.T) {
	log := &types.Log{
		Address: common.Address{},
		Topics:  []common.Hash{},
		Data:    []byte{},
	}

	result, err := encodeLogToJSON(log)
	if err != nil {
		t.Fatalf("encodeLogToJSON() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	topics, ok := decoded["topics"].([]any)
	if !ok {
		t.Fatal("Topics should be an array")
	}
	if len(topics) != 0 {
		t.Errorf("Expected 0 topics, got %d", len(topics))
	}
}

func TestEncodeEventToJSON(t *testing.T) {
	event := &validatorAddedEvent{
		Owner:       common.HexToAddress("0x1234567890123456789012345678901234567890"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		PublicKey:   []byte{0xaa, 0xbb, 0xcc},
	}

	result, err := encodeEventToJSON(event)
	if err != nil {
		t.Fatalf("encodeEventToJSON() error = %v", err)
	}

	if len(result) == 0 {
		t.Error("Expected non-empty JSON")
	}

	// Verify it's valid JSON
	var decoded map[string]any
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
}

func TestEncodeEventToJSON_NilEvent(t *testing.T) {
	result, err := encodeEventToJSON(nil)
	if err != nil {
		t.Fatalf("encodeEventToJSON(nil) error = %v", err)
	}

	// Should produce "null"
	if string(result) != "null" {
		t.Errorf("Expected 'null', got %s", string(result))
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
