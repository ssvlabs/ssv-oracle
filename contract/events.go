package contract

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/pkg/logger"
)

// RootCommittedEvent represents a RootCommitted event from the Oracle contract.
type RootCommittedEvent struct {
	MerkleRoot [32]byte
	BlockNum   uint64 // indexed
	Timestamp  uint64 // block.timestamp when commit was made
}

// SubscribeRootCommitted subscribes to RootCommitted events from the oracle contract.
// Returns a channel that receives events and an error channel for subscription errors.
// The caller should handle reconnection on error.
// If fromBlock is nil, subscribes to new events only (from "latest").
func (c *Client) SubscribeRootCommitted(ctx context.Context, fromBlock *uint64) (<-chan *RootCommittedEvent, <-chan error, error) {
	if c.mockMode {
		return nil, nil, fmt.Errorf("SubscribeRootCommitted not available in mock mode")
	}

	eventChan := make(chan *RootCommittedEvent, 10)
	errChan := make(chan error, 1)

	// Get the event signature for RootCommitted
	event, ok := c.contractABI.Events["RootCommitted"]
	if !ok {
		return nil, nil, fmt.Errorf("RootCommitted event not found in ABI")
	}

	// Create filter query
	query := ethereum.FilterQuery{
		Addresses: []common.Address{c.contractAddress},
		Topics:    [][]common.Hash{{event.ID}},
	}
	if fromBlock != nil {
		query.FromBlock = big.NewInt(int64(*fromBlock))
	}

	// Subscribe to logs
	logs := make(chan types.Log, 10) // Buffer to prevent blocking during slow processing
	sub, err := c.ethClient.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to subscribe to logs: %w", err)
	}

	// Process logs in a goroutine
	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer sub.Unsubscribe()

		for {
			select {
			case <-ctx.Done():
				return
			case err := <-sub.Err():
				if err != nil {
					errChan <- err
				}
				return
			case vLog := <-logs:
				parsedEvent, err := c.parseRootCommittedEvent(vLog)
				if err != nil {
					logger.Warnw("Failed to parse RootCommitted event", "error", err)
					continue
				}

				select {
				case eventChan <- parsedEvent:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return eventChan, errChan, nil
}

// parseRootCommittedEvent parses a log into a RootCommittedEvent.
func (c *Client) parseRootCommittedEvent(vLog types.Log) (*RootCommittedEvent, error) {
	event, ok := c.contractABI.Events["RootCommitted"]
	if !ok {
		return nil, fmt.Errorf("RootCommitted event not found in ABI")
	}

	// Parse indexed parameter (blockNum) from topics
	if len(vLog.Topics) < 2 {
		return nil, fmt.Errorf("invalid log: expected 2 topics, got %d", len(vLog.Topics))
	}

	// Topic[0] is the event signature, Topic[1] is the indexed blockNum
	blockNum := new(big.Int).SetBytes(vLog.Topics[1].Bytes()).Uint64()

	// Parse non-indexed parameters from data (merkleRoot, targetEpoch)
	unpacked, err := event.Inputs.NonIndexed().Unpack(vLog.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack event data: %w", err)
	}

	if len(unpacked) != 2 {
		return nil, fmt.Errorf("expected 2 non-indexed params, got %d", len(unpacked))
	}

	// merkleRoot is [32]byte
	merkleRoot, ok := unpacked[0].([32]byte)
	if !ok {
		return nil, fmt.Errorf("merkleRoot is not [32]byte")
	}

	// timestamp is *big.Int (uint256 in Solidity)
	timestampBig, ok := unpacked[1].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("timestamp is not *big.Int")
	}

	return &RootCommittedEvent{
		MerkleRoot: merkleRoot,
		BlockNum:   blockNum,
		Timestamp:  timestampBig.Uint64(),
	}, nil
}
