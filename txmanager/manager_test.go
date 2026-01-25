package txmanager

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// testTxManager creates a minimal TxManager for testing methods that don't need network calls.
func testTxManager() *TxManager {
	return &TxManager{
		errorSelectors: make(map[string]string),
	}
}

func TestRevertError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *RevertError
		contains string
	}{
		{
			name:     "simulated revert",
			err:      &RevertError{Reason: "test reason", Simulated: true},
			contains: "call reverted",
		},
		{
			name:     "tx revert with hash",
			err:      &RevertError{Reason: "test reason", TxHash: "0x1234567890abcdef"},
			contains: "0x12345678",
		},
		{
			name:     "revert without hash",
			err:      &RevertError{Reason: "test reason"},
			contains: "reverted: test reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if !strings.Contains(got, tt.contains) {
				t.Errorf("RevertError.Error() = %q, want to contain %q", got, tt.contains)
			}
		})
	}
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

	t.Run("nil error", func(t *testing.T) {
		_, ok := IsRevertError(nil)
		if ok {
			t.Error("Expected IsRevertError to return false for nil error")
		}
	})
}

func TestTxManager_DecodeErrorSelector(t *testing.T) {
	m := testTxManager()
	m.SetErrorSelectors(map[string]string{
		"aabbccdd": "KnownError()",
		"12345678": "AnotherError(uint256)",
	})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"known selector", "0xaabbccdd", "KnownError()"},
		{"another known selector", "0x12345678", "AnotherError(uint256)"},
		{"unknown selector", "0x11223344", "0x11223344"},
		{"not a selector - plain text", "plain text", "plain text"},
		{"too short hex", "0x1234", "0x1234"},
		{"just prefix", "0x", "0x"},
		{"empty string", "", ""},
		{"selector with extra data", "0xaabbccdd1122334455", "KnownError()"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.decodeErrorSelector(tt.input)
			if got != tt.expected {
				t.Errorf("decodeErrorSelector(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTxManager_ExtractRevertReason(t *testing.T) {
	m := testTxManager()
	m.SetErrorSelectors(map[string]string{
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
			name:     "known error selector in message",
			errMsg:   "execution reverted: 0x12345678",
			expected: "CustomError()",
		},
		{
			name:     "unknown error",
			errMsg:   "some other error",
			expected: "some other error",
		},
		{
			name:     "empty revert reason",
			errMsg:   "execution reverted:",
			expected: "execution reverted:",
		},
		{
			name:     "revert with spaces",
			errMsg:   "execution reverted:   error with spaces   ",
			expected: "error with spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("%s", tt.errMsg)
			got := m.ExtractRevertReason(err)
			if got != tt.expected {
				t.Errorf("ExtractRevertReason() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestTxManager_SetErrorSelectors(t *testing.T) {
	m := testTxManager()

	// Verify empty map behavior
	got := m.decodeErrorSelector("0xaabbccdd")
	if got != "0xaabbccdd" {
		t.Errorf("Expected unchanged selector with empty map, got %q", got)
	}

	// Set new selectors
	m.SetErrorSelectors(map[string]string{
		"aabbccdd": "TestError()",
	})

	got = m.decodeErrorSelector("0xaabbccdd")
	if got != "TestError()" {
		t.Errorf("Expected 'TestError()', got %q", got)
	}

	// Override with new map
	m.SetErrorSelectors(map[string]string{
		"11223344": "OtherError()",
	})

	// Old selector should not work
	got = m.decodeErrorSelector("0xaabbccdd")
	if got != "0xaabbccdd" {
		t.Errorf("Expected old selector to not be found, got %q", got)
	}

	// New selector should work
	got = m.decodeErrorSelector("0x11223344")
	if got != "OtherError()" {
		t.Errorf("Expected 'OtherError()', got %q", got)
	}
}

func TestIsInsufficientFunds(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"insufficient funds", fmt.Errorf("insufficient funds for gas"), true},
		{"insufficient balance", fmt.Errorf("insufficient balance"), true},
		{"uppercase", fmt.Errorf("INSUFFICIENT FUNDS"), true},
		{"mixed case", fmt.Errorf("Insufficient Balance for transfer"), true},
		{"unrelated error", fmt.Errorf("nonce too low"), false},
		{"empty error", fmt.Errorf(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInsufficientFunds(tt.err)
			if got != tt.expected {
				t.Errorf("isInsufficientFunds() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsNonceTooLow(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"nonce too low", fmt.Errorf("nonce too low"), true},
		{"uppercase", fmt.Errorf("NONCE TOO LOW"), true},
		{"mixed case", fmt.Errorf("Nonce Too Low: expected 5 got 3"), true},
		{"unrelated error", fmt.Errorf("insufficient funds"), false},
		{"empty error", fmt.Errorf(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNonceTooLow(tt.err)
			if got != tt.expected {
				t.Errorf("isNonceTooLow() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsTxAlreadyKnown(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"already known", fmt.Errorf("already known"), true},
		{"uppercase", fmt.Errorf("ALREADY KNOWN"), true},
		{"in context", fmt.Errorf("tx already known in mempool"), true},
		{"unrelated error", fmt.Errorf("nonce too low"), false},
		{"empty error", fmt.Errorf(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTxAlreadyKnown(tt.err)
			if got != tt.expected {
				t.Errorf("isTxAlreadyKnown() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsUnderpriced(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"replacement underpriced", fmt.Errorf("replacement transaction underpriced"), true},
		{"max fee too low", fmt.Errorf("max fee per gas less than block base fee"), true},
		{"generic underpriced", fmt.Errorf("transaction underpriced"), true},
		{"uppercase", fmt.Errorf("REPLACEMENT TRANSACTION UNDERPRICED"), true},
		{"unrelated error", fmt.Errorf("nonce too low"), false},
		{"empty error", fmt.Errorf(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnderpriced(tt.err)
			if got != tt.expected {
				t.Errorf("isUnderpriced() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsContractRevert(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"execution reverted", fmt.Errorf("execution reverted: reason"), true},
		{"uppercase execution reverted", fmt.Errorf("EXECUTION REVERTED: reason"), true},
		{"execution reverted no reason", fmt.Errorf("execution reverted:"), true},
		{"network error", fmt.Errorf("connection refused"), false},
		{"timeout error", fmt.Errorf("context deadline exceeded"), false},
		{"rpc error", fmt.Errorf("rpc error: code = 503"), false},
		{"empty error", fmt.Errorf(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsContractRevert(tt.err)
			if got != tt.expected {
				t.Errorf("IsContractRevert() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestErrorTypes(t *testing.T) {
	t.Run("errBaseFeeExceedsMax is distinguishable", func(t *testing.T) {
		err := fmt.Errorf("test: %w", errBaseFeeExceedsMax)
		if !errors.Is(err, errBaseFeeExceedsMax) {
			t.Error("Expected errors.Is to match errBaseFeeExceedsMax")
		}
		if errors.Is(err, errMaxGasReached) {
			t.Error("errBaseFeeExceedsMax should not match errMaxGasReached")
		}
	})

	t.Run("all error types are distinct", func(t *testing.T) {
		errs := []error{
			errMaxGasReached,
			errMaxAttemptsExhausted,
			errNonceTooLow,
			errInsufficientFunds,
			errBaseFeeExceedsMax,
		}

		for i, err1 := range errs {
			for j, err2 := range errs {
				if i != j && errors.Is(err1, err2) {
					t.Errorf("Error %v should not match %v", err1, err2)
				}
			}
		}
	})
}
