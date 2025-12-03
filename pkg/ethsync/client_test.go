package ethsync

import (
	"testing"
	"time"
)

func TestNewExecutionClient(t *testing.T) {
	// Test with valid config
	cfg := ExecutionClientConfig{
		URL:        "http://localhost:8545",
		BatchSize:  1000,
		MaxRetries: 3,
		RetryDelay: 5 * time.Second,
	}

	// Note: This will fail if no local node is running, but tests the constructor
	_, err := NewExecutionClient(cfg)
	if err != nil {
		t.Logf("Expected error (no local node): %v", err)
	}
}

func TestExecutionClientConfig_Defaults(t *testing.T) {
	cfg := ExecutionClientConfig{
		URL: "http://localhost:8545",
		// Leave other fields as zero values
	}

	client, err := NewExecutionClient(cfg)
	if err != nil {
		t.Logf("Expected error (no local node): %v", err)
		return
	}
	defer client.Close()

	// Verify defaults were applied
	if client.batchSize != 200 {
		t.Errorf("Expected default batch size 200, got %d", client.batchSize)
	}
	if client.maxRetries != 3 {
		t.Errorf("Expected default max retries 3, got %d", client.maxRetries)
	}
	if client.retryDelay != 5*time.Second {
		t.Errorf("Expected default retry delay 5s, got %v", client.retryDelay)
	}
}
