package ethsync

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestNewEventParser(t *testing.T) {
	parser, err := NewEventParser()
	if err != nil {
		t.Fatalf("NewEventParser() error = %v", err)
	}
	if parser == nil {
		t.Fatal("NewEventParser() returned nil")
	}
}

func TestEventParser_ParseLog_EmptyTopics(t *testing.T) {
	parser, _ := NewEventParser()

	log := &types.Log{
		Topics: []common.Hash{},
	}

	_, _, err := parser.ParseLog(log)
	if err == nil {
		t.Error("ParseLog() should error on empty topics")
	}
}

func TestEventParser_ParseLog_UnknownSignature(t *testing.T) {
	parser, _ := NewEventParser()

	log := &types.Log{
		Topics: []common.Hash{
			common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		},
	}

	_, _, err := parser.ParseLog(log)
	if err == nil {
		t.Error("ParseLog() should error on unknown event signature")
	}
}

func TestEventParser_ParseLog_MissingOwnerTopic(t *testing.T) {
	parser, _ := NewEventParser()

	// ValidatorAdded signature but no owner topic
	log := &types.Log{
		Topics: []common.Hash{
			EventSigValidatorAdded,
		},
		Data: []byte{},
	}

	_, _, err := parser.ParseLog(log)
	if err == nil {
		t.Error("ParseLog() should error on missing owner topic")
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

	result, err := EncodeLogToJSON(log)
	if err != nil {
		t.Fatalf("EncodeLogToJSON() error = %v", err)
	}

	if len(result) == 0 {
		t.Error("Expected non-empty JSON")
	}

	// Verify JSON structure
	var decoded map[string]interface{}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if decoded["address"] != log.Address.Hex() {
		t.Errorf("Address mismatch: expected %s, got %v", log.Address.Hex(), decoded["address"])
	}

	topics, ok := decoded["topics"].([]interface{})
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

	result, err := EncodeLogToJSON(log)
	if err != nil {
		t.Fatalf("EncodeLogToJSON() error = %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	topics, ok := decoded["topics"].([]interface{})
	if !ok {
		t.Fatal("Topics should be an array")
	}
	if len(topics) != 0 {
		t.Errorf("Expected 0 topics, got %d", len(topics))
	}
}

func TestEncodeEventToJSON(t *testing.T) {
	event := &ValidatorAddedEvent{
		Owner:       common.HexToAddress("0x1234567890123456789012345678901234567890"),
		OperatorIDs: []uint64{1, 2, 3, 4},
		PublicKey:   []byte{0xaa, 0xbb, 0xcc},
	}

	result, err := EncodeEventToJSON(event)
	if err != nil {
		t.Fatalf("EncodeEventToJSON() error = %v", err)
	}

	if len(result) == 0 {
		t.Error("Expected non-empty JSON")
	}

	// Verify it's valid JSON
	var decoded map[string]interface{}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
}

func TestEncodeEventToJSON_NilEvent(t *testing.T) {
	result, err := EncodeEventToJSON(nil)
	if err != nil {
		t.Fatalf("EncodeEventToJSON(nil) error = %v", err)
	}

	// Should produce "null"
	if string(result) != "null" {
		t.Errorf("Expected 'null', got %s", string(result))
	}
}

func TestEventSignatures(t *testing.T) {
	// Verify event signatures are non-zero and unique
	signatures := []common.Hash{
		EventSigValidatorAdded,
		EventSigValidatorRemoved,
		EventSigClusterLiquidated,
		EventSigClusterReactivated,
		EventSigClusterWithdrawn,
		EventSigClusterDeposited,
		EventSigClusterBalanceUpdated,
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
