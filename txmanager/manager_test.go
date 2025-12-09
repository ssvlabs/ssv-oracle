package txmanager

import (
	"fmt"
	"strings"
	"testing"
)

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

func TestDecodeErrorSelector(t *testing.T) {
	// Set up error selectors for testing
	SetErrorSelectors(map[string]string{
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
			got := decodeErrorSelector(tt.input)
			if got != tt.expected {
				t.Errorf("decodeErrorSelector(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

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
			got := extractRevertReason(err)
			if got != tt.expected {
				t.Errorf("extractRevertReason() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSetErrorSelectors(t *testing.T) {
	// Clear any existing selectors
	SetErrorSelectors(nil)

	// Verify empty map behavior
	got := decodeErrorSelector("0xaabbccdd")
	if got != "0xaabbccdd" {
		t.Errorf("Expected unchanged selector with nil map, got %q", got)
	}

	// Set new selectors
	SetErrorSelectors(map[string]string{
		"aabbccdd": "TestError()",
	})

	got = decodeErrorSelector("0xaabbccdd")
	if got != "TestError()" {
		t.Errorf("Expected 'TestError()', got %q", got)
	}

	// Override with new map
	SetErrorSelectors(map[string]string{
		"11223344": "OtherError()",
	})

	// Old selector should not work
	got = decodeErrorSelector("0xaabbccdd")
	if got != "0xaabbccdd" {
		t.Errorf("Expected old selector to not be found, got %q", got)
	}

	// New selector should work
	got = decodeErrorSelector("0x11223344")
	if got != "OtherError()" {
		t.Errorf("Expected 'OtherError()', got %q", got)
	}
}
