package ethsync

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// Default configuration values.

const (
	maxBackoffDelay   = 30 * time.Second
	defaultBatchSize  = 200
	defaultMaxRetries = 3
	defaultRetryDelay = 5 * time.Second
)

// ExecutionClient wraps an Ethereum execution client for fetching logs and blocks.
type ExecutionClient struct {
	client     *ethclient.Client
	rpcClient  *rpc.Client
	batchSize  uint64
	maxRetries int
	retryDelay time.Duration
}

// ExecutionClientConfig holds configuration for the execution client.
type ExecutionClientConfig struct {
	URL        string
	BatchSize  uint64        // Number of blocks to fetch per batch
	MaxRetries int           // Max retry attempts for failed RPC calls
	RetryDelay time.Duration // Delay between retries
}

// NewExecutionClient creates a new execution client.
func NewExecutionClient(cfg ExecutionClientConfig) (*ExecutionClient, error) {
	rpcClient, err := rpc.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to dial RPC: %w", err)
	}

	client := ethclient.NewClient(rpcClient)

	if cfg.BatchSize == 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = defaultRetryDelay
	}

	return &ExecutionClient{
		client:     client,
		rpcClient:  rpcClient,
		batchSize:  cfg.BatchSize,
		maxRetries: cfg.MaxRetries,
		retryDelay: cfg.RetryDelay,
	}, nil
}

// Close closes the client connection.
func (c *ExecutionClient) Close() {
	c.client.Close()
}

// withRetry executes fn with exponential backoff retry.
func (c *ExecutionClient) withRetry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if attempt < c.maxRetries-1 {
			delay := c.retryDelay * time.Duration(1<<attempt)
			if delay > maxBackoffDelay {
				delay = maxBackoffDelay
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("after %d attempts: %w", c.maxRetries, err)
}

// GetFinalizedBlock returns the latest finalized block number.
func (c *ExecutionClient) GetFinalizedBlock(ctx context.Context) (uint64, error) {
	var result *types.Header
	err := c.withRetry(ctx, func() error {
		var err error
		result, err = c.client.HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("failed to get finalized block: %w", err)
	}
	return result.Number.Uint64(), nil
}

// GetBlockByNumber returns a block header by number.
func (c *ExecutionClient) GetBlockByNumber(ctx context.Context, number uint64) (*types.Header, error) {
	var result *types.Header
	err := c.withRetry(ctx, func() error {
		var err error
		result, err = c.client.HeaderByNumber(ctx, new(big.Int).SetUint64(number))
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get block %d: %w", number, err)
	}
	return result, nil
}

// BlockLogs represents logs from a single block.
type BlockLogs struct {
	BlockNumber uint64
	BlockTime   time.Time
	Logs        []types.Log
}

// FetchLogsCallback is called for each batch of logs.
type FetchLogsCallback func(batchEnd uint64, logs []BlockLogs) error

// FetchLogs fetches logs in batches, calling the callback after each batch.
func (c *ExecutionClient) FetchLogs(
	ctx context.Context,
	address common.Address,
	fromBlock, toBlock uint64,
	callback FetchLogsCallback,
) error {
	currentBlock := fromBlock

	for currentBlock <= toBlock {
		// Calculate batch end (use smaller batches for faster progress)
		batchEnd := currentBlock + c.batchSize - 1
		if batchEnd > toBlock {
			batchEnd = toBlock
		}

		// Fetch logs for this batch
		logs, err := c.fetchLogsBatch(ctx, address, currentBlock, batchEnd)
		if err != nil {
			return err
		}

		// Pack logs by block (only blocks with events)
		// Also fetches block timestamps for each unique block
		batchLogs, err := c.packLogs(ctx, logs)
		if err != nil {
			return err
		}

		// Call callback with batch results
		if err := callback(batchEnd, batchLogs); err != nil {
			return err
		}

		// Check context
		if ctx.Err() != nil {
			return ctx.Err()
		}

		currentBlock = batchEnd + 1
	}

	return nil
}

// packLogs groups logs by block number and fetches timestamps via batch RPC.
func (c *ExecutionClient) packLogs(ctx context.Context, logs []types.Log) ([]BlockLogs, error) {
	if len(logs) == 0 {
		return nil, nil
	}

	// Sort logs by block number, then tx index
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].BlockNumber == logs[j].BlockNumber {
			return logs[i].TxIndex < logs[j].TxIndex
		}
		return logs[i].BlockNumber < logs[j].BlockNumber
	})

	// Collect unique block numbers
	uniqueBlocks := make([]uint64, 0)
	blockSet := make(map[uint64]bool)
	for _, log := range logs {
		if !blockSet[log.BlockNumber] {
			blockSet[log.BlockNumber] = true
			uniqueBlocks = append(uniqueBlocks, log.BlockNumber)
		}
	}

	// Fetch all block timestamps in a single batch RPC call
	blockTimes, err := c.getBlockTimestampsBatch(ctx, uniqueBlocks)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch block timestamps: %w", err)
	}

	// Group logs by block
	var result []BlockLogs
	for _, log := range logs {
		// Create new BlockLogs if needed
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
		// Append log to current block
		result[len(result)-1].Logs = append(result[len(result)-1].Logs, log)
	}

	return result, nil
}

// getBlockTimestampsBatch fetches timestamps for multiple blocks in a single batch RPC call.
func (c *ExecutionClient) getBlockTimestampsBatch(ctx context.Context, blockNumbers []uint64) (map[uint64]time.Time, error) {
	if len(blockNumbers) == 0 {
		return make(map[uint64]time.Time), nil
	}

	// Prepare batch elements
	batch := make([]rpc.BatchElem, len(blockNumbers))
	results := make([]*types.Header, len(blockNumbers))

	for i, blockNum := range blockNumbers {
		results[i] = new(types.Header)
		batch[i] = rpc.BatchElem{
			Method: "eth_getBlockByNumber",
			Args:   []any{fmt.Sprintf("0x%x", blockNum), false}, // false = don't include txs
			Result: results[i],
		}
	}

	// Execute batch with retries
	err := c.withRetry(ctx, func() error {
		return c.rpcClient.BatchCallContext(ctx, batch)
	})
	if err != nil {
		return nil, fmt.Errorf("batch RPC failed: %w", err)
	}

	// Process results
	blockTimes := make(map[uint64]time.Time)
	for i, elem := range batch {
		if elem.Error != nil {
			return nil, fmt.Errorf("failed to get block %d: %w", blockNumbers[i], elem.Error)
		}
		if results[i] == nil {
			return nil, fmt.Errorf("nil result for block %d", blockNumbers[i])
		}
		blockTimes[blockNumbers[i]] = time.Unix(int64(results[i].Time), 0).UTC()
	}

	return blockTimes, nil
}

// fetchLogsBatch fetches logs for a single batch with retries.
func (c *ExecutionClient) fetchLogsBatch(
	ctx context.Context,
	address common.Address,
	fromBlock, toBlock uint64,
) ([]types.Log, error) {
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: []common.Address{address},
	}

	var logs []types.Log
	err := c.withRetry(ctx, func() error {
		var err error
		logs, err = c.client.FilterLogs(ctx, query)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs [%d-%d]: %w", fromBlock, toBlock, err)
	}
	return logs, nil
}

// GetChainID returns the chain ID of the connected network.
func (c *ExecutionClient) GetChainID(ctx context.Context) (*big.Int, error) {
	var chainID *big.Int
	err := c.withRetry(ctx, func() error {
		var err error
		chainID, err = c.client.ChainID(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}
	return chainID, nil
}
