package contract

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/logger"
)

const eventRootCommitted = "RootCommitted"

// RootCommittedEvent represents a RootCommitted event from the contract.
type RootCommittedEvent struct {
	MerkleRoot [32]byte
	BlockNum   uint64
	TxHash     common.Hash
}

// SubscribeRootCommitted subscribes to RootCommitted events.
// Returns event and error channels. Caller should handle reconnection on error.
// Requires WebSocket client (eth_ws_rpc in config).
func (c *Client) SubscribeRootCommitted(ctx context.Context, fromBlock *uint64) (<-chan *RootCommittedEvent, <-chan error, error) {
	if c.wsClient == nil {
		return nil, nil, fmt.Errorf("WebSocket client not configured (set eth_ws_rpc in config)")
	}

	event, ok := SSVNetworkABI.Events[eventRootCommitted]
	if !ok {
		return nil, nil, fmt.Errorf("%s event not found in ABI", eventRootCommitted)
	}

	query := ethereum.FilterQuery{
		Addresses: []common.Address{c.contractAddress},
		Topics:    [][]common.Hash{{event.ID}},
	}
	if fromBlock != nil {
		query.FromBlock = big.NewInt(int64(*fromBlock))
	}

	logs := make(chan types.Log, 10)
	sub, err := c.wsClient.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe to logs: %w", err)
	}

	eventChan := make(chan *RootCommittedEvent, 10)
	errChan := make(chan error, 1)

	go c.processRootCommittedLogs(ctx, sub, logs, eventChan, errChan)

	return eventChan, errChan, nil
}

func (c *Client) processRootCommittedLogs(
	ctx context.Context,
	sub ethereum.Subscription,
	logs <-chan types.Log,
	eventChan chan<- *RootCommittedEvent,
	errChan chan<- error,
) {
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
		case vLog, ok := <-logs:
			if !ok {
				logger.Debug("RootCommitted subscription channel closed")
				return
			}
			if vLog.Removed {
				logger.Debugw("Reorged RootCommitted event",
					"txHash", vLog.TxHash.Hex(),
					"blockNumber", vLog.BlockNumber)
				continue
			}
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
}

func (c *Client) parseRootCommittedEvent(vLog types.Log) (*RootCommittedEvent, error) {
	if len(vLog.Topics) < 3 {
		return nil, fmt.Errorf("invalid log: expected 3 topics, got %d", len(vLog.Topics))
	}

	var merkleRoot [32]byte
	copy(merkleRoot[:], vLog.Topics[1].Bytes())

	blockNum := new(big.Int).SetBytes(vLog.Topics[2].Bytes()).Uint64()

	return &RootCommittedEvent{
		MerkleRoot: merkleRoot,
		BlockNum:   blockNum,
		TxHash:     vLog.TxHash,
	}, nil
}
