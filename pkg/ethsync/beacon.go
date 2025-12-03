package ethsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// BeaconClient queries the Ethereum beacon chain API.
type BeaconClient struct {
	url        string
	httpClient *http.Client
	maxRetries int
	retryDelay time.Duration
	spec       *Spec // Cached spec (populated on first GetSpec call)
}

// BeaconClientConfig holds configuration for the beacon client.
type BeaconClientConfig struct {
	URL        string
	Timeout    time.Duration
	MaxRetries int
	RetryDelay time.Duration
}

// NewBeaconClient creates a new beacon client.
func NewBeaconClient(cfg BeaconClientConfig) *BeaconClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 5 * time.Second
	}

	return &BeaconClient{
		url: cfg.URL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		maxRetries: cfg.MaxRetries,
		retryDelay: cfg.RetryDelay,
	}
}

// FinalityCheckpoints represents the beacon chain finality checkpoints.
type FinalityCheckpoints struct {
	Data struct {
		PreviousJustified struct {
			Epoch string `json:"epoch"`
			Root  string `json:"root"`
		} `json:"previous_justified"`
		CurrentJustified struct {
			Epoch string `json:"epoch"`
			Root  string `json:"root"`
		} `json:"current_justified"`
		Finalized struct {
			Epoch string `json:"epoch"`
			Root  string `json:"root"`
		} `json:"finalized"`
	} `json:"data"`
}

// GetSpec fetches beacon chain spec parameters and returns a Spec struct.
// The spec is cached after the first call.
func (c *BeaconClient) GetSpec(ctx context.Context) (*Spec, error) {
	if c.spec != nil {
		return c.spec, nil
	}

	genesisTime, err := c.GetGenesisTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get genesis time: %w", err)
	}

	url := fmt.Sprintf("%s/eth/v1/config/spec", c.url)

	// Use interface{} because beacon spec contains mixed types (strings and arrays)
	var response struct {
		Data map[string]interface{} `json:"data"`
	}

	if err := c.doRequest(ctx, url, &response); err != nil {
		return nil, fmt.Errorf("failed to get spec: %w", err)
	}

	var slotsPerEpoch uint64
	if val, ok := response.Data["SLOTS_PER_EPOCH"]; ok {
		if strVal, ok := val.(string); ok {
			if _, err := fmt.Sscanf(strVal, "%d", &slotsPerEpoch); err != nil {
				return nil, fmt.Errorf("failed to parse SLOTS_PER_EPOCH: %w", err)
			}
		} else {
			return nil, fmt.Errorf("SLOTS_PER_EPOCH is not a string")
		}
	} else {
		return nil, fmt.Errorf("SLOTS_PER_EPOCH not found in spec")
	}

	var secondsPerSlot uint64
	if val, ok := response.Data["SECONDS_PER_SLOT"]; ok {
		if strVal, ok := val.(string); ok {
			if _, err := fmt.Sscanf(strVal, "%d", &secondsPerSlot); err != nil {
				return nil, fmt.Errorf("failed to parse SECONDS_PER_SLOT: %w", err)
			}
		} else {
			return nil, fmt.Errorf("SECONDS_PER_SLOT is not a string")
		}
	} else {
		return nil, fmt.Errorf("SECONDS_PER_SLOT not found in spec")
	}

	c.spec = &Spec{
		GenesisTime:   genesisTime,
		SlotsPerEpoch: slotsPerEpoch,
		SlotDuration:  time.Duration(secondsPerSlot) * time.Second,
	}

	return c.spec, nil
}

// GetGenesisTime returns the beacon chain genesis time.
func (c *BeaconClient) GetGenesisTime(ctx context.Context) (time.Time, error) {
	url := fmt.Sprintf("%s/eth/v1/beacon/genesis", c.url)

	var response struct {
		Data struct {
			GenesisTime string `json:"genesis_time"`
		} `json:"data"`
	}

	err := c.doRequest(ctx, url, &response)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get genesis: %w", err)
	}

	var genesisTimestamp int64
	if _, err := fmt.Sscanf(response.Data.GenesisTime, "%d", &genesisTimestamp); err != nil {
		return time.Time{}, fmt.Errorf("failed to parse genesis time: %w", err)
	}

	return time.Unix(genesisTimestamp, 0).UTC(), nil
}

// FinalizedCheckpoint contains finalization info including the reference block.
type FinalizedCheckpoint struct {
	Epoch    uint64 // The finalized epoch
	BlockNum uint64 // Execution block number of the checkpoint block
}

// GetFinalizedCheckpoint returns the finalized checkpoint with its execution block number.
// Uses head state to get the most up-to-date finalization info.
func (c *BeaconClient) GetFinalizedCheckpoint(ctx context.Context) (*FinalizedCheckpoint, error) {
	url := fmt.Sprintf("%s/eth/v1/beacon/states/head/finality_checkpoints", c.url)

	var checkpoints FinalityCheckpoints
	err := c.doRequest(ctx, url, &checkpoints)
	if err != nil {
		return nil, fmt.Errorf("failed to get finality checkpoints: %w", err)
	}

	var epoch uint64
	if _, err := fmt.Sscanf(checkpoints.Data.Finalized.Epoch, "%d", &epoch); err != nil {
		return nil, fmt.Errorf("failed to parse finalized epoch: %w", err)
	}

	// Get execution block number from checkpoint root
	blockNum, err := c.getExecutionBlockFromRoot(ctx, checkpoints.Data.Finalized.Root)
	if err != nil {
		return nil, fmt.Errorf("failed to get execution block from checkpoint root: %w", err)
	}

	return &FinalizedCheckpoint{
		Epoch:    epoch,
		BlockNum: blockNum,
	}, nil
}

// getExecutionBlockFromRoot returns the execution block number for a beacon block root.
func (c *BeaconClient) getExecutionBlockFromRoot(ctx context.Context, blockRoot string) (uint64, error) {
	url := fmt.Sprintf("%s/eth/v2/beacon/blocks/%s", c.url, blockRoot)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Data struct {
			Message struct {
				Body struct {
					ExecutionPayload struct {
						BlockNumber string `json:"block_number"`
					} `json:"execution_payload"`
				} `json:"body"`
			} `json:"message"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	var blockNum uint64
	if _, err := fmt.Sscanf(response.Data.Message.Body.ExecutionPayload.BlockNumber, "%d", &blockNum); err != nil {
		return 0, fmt.Errorf("failed to parse block number: %w", err)
	}

	return blockNum, nil
}

const (
	// validatorBatchSize is the max number of validators per beacon API request.
	validatorBatchSize = 1000
	// maxParallelRequests limits concurrent beacon API requests.
	maxParallelRequests = 5
)

// GetFinalizedValidatorBalances fetches effective balances for validators from the finalized state.
// pubkeys is a list of validator public keys (48 bytes each).
// Returns a map of pubkey (hex with 0x prefix) -> effective balance in Gwei.
//
// Requests are batched (1000 per request) with limited parallelism (5 concurrent).
func (c *BeaconClient) GetFinalizedValidatorBalances(ctx context.Context, pubkeys [][]byte) (map[string]uint64, error) {
	if len(pubkeys) == 0 {
		return make(map[string]uint64), nil
	}

	// Split into batches
	var batches [][][]byte
	for i := 0; i < len(pubkeys); i += validatorBatchSize {
		end := i + validatorBatchSize
		if end > len(pubkeys) {
			end = len(pubkeys)
		}
		batches = append(batches, pubkeys[i:end])
	}

	// Fetch batches with limited parallelism
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxParallelRequests)

	var mu sync.Mutex
	merged := make(map[string]uint64, len(pubkeys))

	for _, batch := range batches {
		g.Go(func() error {
			balances, err := c.fetchValidatorBatchFinalized(ctx, batch)
			if err != nil {
				return err
			}
			mu.Lock()
			for k, v := range balances {
				merged[k] = v
			}
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return merged, nil
}

// fetchValidatorBatchFinalized fetches effective balances for a single batch of validators from finalized state.
func (c *BeaconClient) fetchValidatorBatchFinalized(ctx context.Context, pubkeys [][]byte) (map[string]uint64, error) {
	url := fmt.Sprintf("%s/eth/v1/beacon/states/finalized/validators", c.url)

	// Use POST request with validator IDs to fetch only the validators we need
	ids := make([]string, len(pubkeys))
	for i, pubkey := range pubkeys {
		ids[i] = fmt.Sprintf("0x%x", pubkey)
	}

	requestBody := struct {
		IDs []string `json:"ids"`
	}{
		IDs: ids,
	}

	var response struct {
		Data []struct {
			Index     string `json:"index"`
			Balance   string `json:"balance"`
			Status    string `json:"status"`
			Validator struct {
				Pubkey                     string `json:"pubkey"`
				EffectiveBalance           string `json:"effective_balance"`
				WithdrawalCredentials      string `json:"withdrawal_credentials"`
				Slashed                    bool   `json:"slashed"`
				ActivationEligibilityEpoch string `json:"activation_eligibility_epoch"`
				ActivationEpoch            string `json:"activation_epoch"`
				ExitEpoch                  string `json:"exit_epoch"`
				WithdrawableEpoch          string `json:"withdrawable_epoch"`
			} `json:"validator"`
		} `json:"data"`
	}

	err := c.doPostRequest(ctx, url, requestBody, &response)
	if err != nil {
		return nil, fmt.Errorf("failed to get validators: %w", err)
	}

	// Extract effective balances
	// Use lowercase pubkey as key for consistent lookups
	result := make(map[string]uint64)
	for _, validator := range response.Data {
		var effectiveBalance uint64
		if _, err := fmt.Sscanf(validator.Validator.EffectiveBalance, "%d", &effectiveBalance); err != nil {
			return nil, fmt.Errorf("failed to parse effective balance for %s: %w", validator.Validator.Pubkey, err)
		}

		// Normalize pubkey to lowercase for consistent map lookups
		pubkeyLower := strings.ToLower(validator.Validator.Pubkey)
		result[pubkeyLower] = effectiveBalance
	}

	return result, nil
}

// doRequest performs an HTTP GET request with retries.
func (c *BeaconClient) doRequest(ctx context.Context, url string, result interface{}) error {
	var lastErr error

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < c.maxRetries-1 {
				time.Sleep(c.retryDelay)
				continue
			}
			break
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
			if attempt < c.maxRetries-1 {
				time.Sleep(c.retryDelay)
				continue
			}
			break
		}

		if readErr != nil {
			return fmt.Errorf("failed to read response body: %w", readErr)
		}

		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	}

	return fmt.Errorf("request failed after %d attempts: %w", c.maxRetries, lastErr)
}

// doPostRequest performs an HTTP POST request with JSON body and retries.
func (c *BeaconClient) doPostRequest(ctx context.Context, url string, body interface{}, result interface{}) error {
	var lastErr error

	for attempt := 0; attempt < c.maxRetries; attempt++ {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < c.maxRetries-1 {
				time.Sleep(c.retryDelay)
				continue
			}
			break
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
			if attempt < c.maxRetries-1 {
				time.Sleep(c.retryDelay)
				continue
			}
			break
		}

		if readErr != nil {
			return fmt.Errorf("failed to read response body: %w", readErr)
		}

		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}

		return nil
	}

	return fmt.Errorf("POST request failed after %d attempts: %w", c.maxRetries, lastErr)
}
