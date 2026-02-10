package execution

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/ssvlabs/ssv-oracle/eth"
	"github.com/ssvlabs/ssv-oracle/logger"
)

// Client wraps an Ethereum execution client for fetching logs and blocks.
type Client struct {
	client      *ethclient.Client
	rpcClient   *rpc.Client
	batchSize   *AdaptiveBatchSize
	retryConfig eth.RetryConfig
}

// ClientConfig holds configuration for the execution client.
type ClientConfig struct {
	URL          string
	MinBatchSize uint64
	MaxBatchSize uint64
	RetryConfig  *eth.RetryConfig
}

// BlockLogs represents logs from a single block.
type BlockLogs struct {
	BlockNumber uint64
	BlockTime   time.Time
	Logs        []types.Log
}

// FetchLogsCallback is called for each batch of logs.
type FetchLogsCallback func(batchEnd uint64, logs []BlockLogs) error

// batchErrorCategory represents why a batch request failed.
type batchErrorCategory string

const (
	errCategoryNone            batchErrorCategory = ""
	errCategoryBlockRange      batchErrorCategory = "block_range_too_large"
	errCategoryRateLimit       batchErrorCategory = "rate_limited"
	errCategoryPayloadTooLarge batchErrorCategory = "payload_too_large"
	errCategoryTimeout         batchErrorCategory = "timeout"

	// RPC providers reject batch calls above a certain size.
	maxBlockTimestampBatch = 100
)

// New creates a new execution client.
func New(ctx context.Context, cfg ClientConfig) (*Client, error) {
	rpcClient, err := rpc.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dial RPC: %w", err)
	}

	client := ethclient.NewClient(rpcClient)

	version := "unknown"
	if err := rpcClient.CallContext(ctx, &version, "web3_clientVersion"); err != nil {
		version = "unknown"
	}
	logger.Infow("Execution client connected", "version", version, "url", cfg.URL)

	batchSize := NewAdaptiveBatchSize(cfg.MinBatchSize, cfg.MaxBatchSize)
	logger.Infow("Adaptive batch sizing enabled",
		"min", batchSize.min,
		"max", batchSize.max)

	retryConfig := eth.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		retryConfig = *cfg.RetryConfig
	}

	return &Client{
		client:      client,
		rpcClient:   rpcClient,
		batchSize:   batchSize,
		retryConfig: retryConfig,
	}, nil
}

// Close closes the client connection.
func (c *Client) Close() {
	c.client.Close()
}

// GetFinalizedBlock returns the latest finalized block number.
func (c *Client) GetFinalizedBlock(ctx context.Context) (uint64, error) {
	var result *types.Header
	err := eth.WithRetry(ctx, c.retryConfig, func() error {
		var err error
		result, err = c.client.HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
		return err
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("get finalized block: %w", err)
	}
	return result.Number.Uint64(), nil
}

// GetHeadBlock returns the latest block number (head of chain).
func (c *Client) GetHeadBlock(ctx context.Context) (uint64, error) {
	var result *types.Header
	err := eth.WithRetry(ctx, c.retryConfig, func() error {
		var err error
		result, err = c.client.HeaderByNumber(ctx, nil) // nil = latest
		return err
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("get head block: %w", err)
	}
	return result.Number.Uint64(), nil
}

// FetchLogs fetches logs in batches, calling the callback after each batch.
// Uses adaptive batch sizing: decreases on errors, increases on success.
// If topics is non-empty, only logs matching one of the topic0 hashes are returned.
func (c *Client) FetchLogs(
	ctx context.Context,
	address common.Address,
	fromBlock, toBlock uint64,
	topics []common.Hash,
	callback FetchLogsCallback,
) error {
	currentBlock := fromBlock

	for currentBlock <= toBlock {
		batchSize := c.batchSize.Get()
		batchEnd := currentBlock + batchSize - 1
		if batchEnd > toBlock {
			batchEnd = toBlock
		}

		logs, err := c.fetchLogsBatch(ctx, address, currentBlock, batchEnd, topics)
		if err != nil {
			if category := categorizeBatchError(err); category != errCategoryNone {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				newSize := c.batchSize.Decrease()
				logger.Warnw("Reducing batch size",
					"reason", string(category),
					"from", currentBlock,
					"to", batchEnd,
					"newBatchSize", newSize,
					"error", err)
				continue // retry with smaller batch
			}
			return err
		}

		c.batchSize.Increase()

		batchLogs, err := c.packLogs(ctx, logs)
		if err != nil {
			return err
		}

		if err := callback(batchEnd, batchLogs); err != nil {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		currentBlock = batchEnd + 1
	}

	return nil
}

// categorizeBatchError checks if the error suggests the batch size should be reduced.
// Returns the error category, or empty string if the error is not batch-size related.
func categorizeBatchError(err error) batchErrorCategory {
	if err == nil {
		return errCategoryNone
	}
	msg := strings.ToLower(err.Error())

	// Block range errors (RPC limit on block span)
	if strings.Contains(msg, "block range") ||
		strings.Contains(msg, "exceed") ||
		strings.Contains(msg, "query returned more than") {
		return errCategoryBlockRange
	}

	// Rate limiting (429)
	if strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "429") {
		return errCategoryRateLimit
	}

	// Payload too large (413)
	if strings.Contains(msg, "payload too large") ||
		strings.Contains(msg, "request entity too large") ||
		strings.Contains(msg, "413") ||
		strings.Contains(msg, "too large") {
		return errCategoryPayloadTooLarge
	}

	// Timeout (query took too long, likely too much data)
	if strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context deadline") {
		return errCategoryTimeout
	}

	// Generic limit errors
	if strings.Contains(msg, "limit") ||
		strings.Contains(msg, "too many") {
		return errCategoryBlockRange
	}

	return errCategoryNone
}

// packLogs groups logs by block number and fetches timestamps via batch RPC.
func (c *Client) packLogs(ctx context.Context, logs []types.Log) ([]BlockLogs, error) {
	if len(logs) == 0 {
		return nil, nil
	}

	// Extract unique block numbers.
	var uniqueBlocks []uint64
	seen := make(map[uint64]bool)
	for _, log := range logs {
		if !seen[log.BlockNumber] {
			seen[log.BlockNumber] = true
			uniqueBlocks = append(uniqueBlocks, log.BlockNumber)
		}
	}

	blockTimes, err := c.getBlockTimestampsBatch(ctx, uniqueBlocks)
	if err != nil {
		return nil, fmt.Errorf("fetch block timestamps: %w", err)
	}

	return groupLogsByBlock(logs, blockTimes)
}

// groupLogsByBlock sorts logs and groups them by block number with timestamps.
// Logs are sorted by block number, tx index, then log index to preserve event order.
func groupLogsByBlock(logs []types.Log, blockTimes map[uint64]time.Time) ([]BlockLogs, error) {
	if len(logs) == 0 {
		return nil, nil
	}

	// Sort by block number, tx index, then log index.
	// Order is critical: multiple events in the same tx must be processed in order.
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].BlockNumber != logs[j].BlockNumber {
			return logs[i].BlockNumber < logs[j].BlockNumber
		}
		if logs[i].TxIndex != logs[j].TxIndex {
			return logs[i].TxIndex < logs[j].TxIndex
		}
		return logs[i].Index < logs[j].Index
	})

	var result []BlockLogs
	for _, log := range logs {
		if len(result) == 0 || result[len(result)-1].BlockNumber != log.BlockNumber {
			blockTime, ok := blockTimes[log.BlockNumber]
			if !ok {
				return nil, fmt.Errorf("missing block time for block %d", log.BlockNumber)
			}
			result = append(result, BlockLogs{
				BlockNumber: log.BlockNumber,
				BlockTime:   blockTime,
			})
		}
		result[len(result)-1].Logs = append(result[len(result)-1].Logs, log)
	}

	return result, nil
}

// getBlockTimestampsBatch fetches timestamps for multiple blocks using chunked batch RPC calls.
func (c *Client) getBlockTimestampsBatch(ctx context.Context, blockNumbers []uint64) (map[uint64]time.Time, error) {
	if len(blockNumbers) == 0 {
		return make(map[uint64]time.Time), nil
	}

	blockTimes := make(map[uint64]time.Time, len(blockNumbers))

	for start := 0; start < len(blockNumbers); start += maxBlockTimestampBatch {
		end := start + maxBlockTimestampBatch
		if end > len(blockNumbers) {
			end = len(blockNumbers)
		}
		chunk := blockNumbers[start:end]

		batch := make([]rpc.BatchElem, len(chunk))
		results := make([]*types.Header, len(chunk))

		for i, blockNum := range chunk {
			results[i] = new(types.Header)
			batch[i] = rpc.BatchElem{
				Method: "eth_getBlockByNumber",
				Args:   []any{fmt.Sprintf("0x%x", blockNum), false},
				Result: results[i],
			}
		}

		err := eth.WithRetry(ctx, c.retryConfig, func() error {
			return c.rpcClient.BatchCallContext(ctx, batch)
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("batch RPC: %w", err)
		}

		for i, elem := range batch {
			if elem.Error != nil {
				return nil, fmt.Errorf("get block %d: %w", chunk[i], elem.Error)
			}
			if results[i] == nil {
				return nil, fmt.Errorf("nil result for block %d", chunk[i])
			}
			blockTimes[chunk[i]] = time.Unix(int64(results[i].Time), 0).UTC()
		}
	}

	return blockTimes, nil
}

func (c *Client) fetchLogsBatch(
	ctx context.Context,
	address common.Address,
	fromBlock, toBlock uint64,
	topics []common.Hash,
) ([]types.Log, error) {
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: []common.Address{address},
	}

	// Add topic filter if provided (filters by topic0 = event signature)
	if len(topics) > 0 {
		query.Topics = [][]common.Hash{topics}
	}

	var logs []types.Log
	err := eth.WithRetry(ctx, c.retryConfig, func() error {
		var err error
		logs, err = c.client.FilterLogs(ctx, query)
		return err
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch logs [%d-%d]: %w", fromBlock, toBlock, err)
	}
	return logs, nil
}

// GetChainID returns the chain ID of the connected network.
func (c *Client) GetChainID(ctx context.Context) (*big.Int, error) {
	var chainID *big.Int
	err := eth.WithRetry(ctx, c.retryConfig, func() error {
		var err error
		chainID, err = c.client.ChainID(ctx)
		return err
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("get chain ID: %w", err)
	}
	return chainID, nil
}
