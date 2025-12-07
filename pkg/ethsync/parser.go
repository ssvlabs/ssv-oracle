package ethsync

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/contract"
)

// EventParser parses SSV contract events.
type EventParser struct {
	abi abi.ABI
}

// NewEventParser creates a new event parser using the shared SSVNetwork ABI.
func NewEventParser() (*EventParser, error) {
	return &EventParser{abi: contract.SSVNetworkABI}, nil
}

// ParseLog parses an Ethereum log into a structured event.
// Returns the event type name and parsed data as interface{}.
func (p *EventParser) ParseLog(log *types.Log) (string, interface{}, error) {
	if len(log.Topics) == 0 {
		return "", nil, fmt.Errorf("log has no topics")
	}

	eventSig := log.Topics[0]

	switch eventSig {
	case EventSigValidatorAdded:
		event, err := p.parseValidatorAdded(log)
		return EventValidatorAdded, event, err
	case EventSigValidatorRemoved:
		event, err := p.parseValidatorRemoved(log)
		return EventValidatorRemoved, event, err
	case EventSigClusterLiquidated:
		event, err := p.parseClusterLiquidated(log)
		return EventClusterLiquidated, event, err
	case EventSigClusterReactivated:
		event, err := p.parseClusterReactivated(log)
		return EventClusterReactivated, event, err
	case EventSigClusterWithdrawn:
		event, err := p.parseClusterWithdrawn(log)
		return EventClusterWithdrawn, event, err
	case EventSigClusterDeposited:
		event, err := p.parseClusterDeposited(log)
		return EventClusterDeposited, event, err
	default:
		return "", nil, fmt.Errorf("unknown event signature: %s", eventSig.Hex())
	}
}

func (p *EventParser) parseValidatorAdded(log *types.Log) (*ValidatorAddedEvent, error) {
	event := &ValidatorAddedEvent{}

	// Owner is indexed (topic[1])
	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	// Unpack non-indexed fields (operatorIds, publicKey, shares, cluster)
	var result struct {
		OperatorIds []uint64
		PublicKey   []byte
		Shares      []byte
		Cluster     struct {
			ValidatorCount  uint32
			NetworkFeeIndex uint64
			Index           uint64
			Active          bool
			Balance         *big.Int
		}
	}

	err := p.abi.UnpackIntoInterface(&result, "ValidatorAdded", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ValidatorAdded: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.PublicKey = result.PublicKey
	event.Shares = result.Shares
	event.Cluster = Cluster{
		ValidatorCount:  result.Cluster.ValidatorCount,
		NetworkFeeIndex: result.Cluster.NetworkFeeIndex,
		Index:           result.Cluster.Index,
		Active:          result.Cluster.Active,
		Balance:         result.Cluster.Balance,
	}

	return event, nil
}

func (p *EventParser) parseValidatorRemoved(log *types.Log) (*ValidatorRemovedEvent, error) {
	event := &ValidatorRemovedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		PublicKey   []byte
		Cluster     struct {
			ValidatorCount  uint32
			NetworkFeeIndex uint64
			Index           uint64
			Active          bool
			Balance         *big.Int
		}
	}

	err := p.abi.UnpackIntoInterface(&result, "ValidatorRemoved", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ValidatorRemoved: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.PublicKey = result.PublicKey
	event.Cluster = Cluster{
		ValidatorCount:  result.Cluster.ValidatorCount,
		NetworkFeeIndex: result.Cluster.NetworkFeeIndex,
		Index:           result.Cluster.Index,
		Active:          result.Cluster.Active,
		Balance:         result.Cluster.Balance,
	}

	return event, nil
}

func (p *EventParser) parseClusterLiquidated(log *types.Log) (*ClusterLiquidatedEvent, error) {
	event := &ClusterLiquidatedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Cluster     struct {
			ValidatorCount  uint32
			NetworkFeeIndex uint64
			Index           uint64
			Active          bool
			Balance         *big.Int
		}
	}

	err := p.abi.UnpackIntoInterface(&result, "ClusterLiquidated", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterLiquidated: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Cluster = Cluster{
		ValidatorCount:  result.Cluster.ValidatorCount,
		NetworkFeeIndex: result.Cluster.NetworkFeeIndex,
		Index:           result.Cluster.Index,
		Active:          result.Cluster.Active,
		Balance:         result.Cluster.Balance,
	}

	return event, nil
}

func (p *EventParser) parseClusterReactivated(log *types.Log) (*ClusterReactivatedEvent, error) {
	event := &ClusterReactivatedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Cluster     struct {
			ValidatorCount  uint32
			NetworkFeeIndex uint64
			Index           uint64
			Active          bool
			Balance         *big.Int
		}
	}

	err := p.abi.UnpackIntoInterface(&result, "ClusterReactivated", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterReactivated: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Cluster = Cluster{
		ValidatorCount:  result.Cluster.ValidatorCount,
		NetworkFeeIndex: result.Cluster.NetworkFeeIndex,
		Index:           result.Cluster.Index,
		Active:          result.Cluster.Active,
		Balance:         result.Cluster.Balance,
	}

	return event, nil
}

func (p *EventParser) parseClusterWithdrawn(log *types.Log) (*ClusterWithdrawnEvent, error) {
	event := &ClusterWithdrawnEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Value       *big.Int
		Cluster     struct {
			ValidatorCount  uint32
			NetworkFeeIndex uint64
			Index           uint64
			Active          bool
			Balance         *big.Int
		}
	}

	err := p.abi.UnpackIntoInterface(&result, "ClusterWithdrawn", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterWithdrawn: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Value = result.Value
	event.Cluster = Cluster{
		ValidatorCount:  result.Cluster.ValidatorCount,
		NetworkFeeIndex: result.Cluster.NetworkFeeIndex,
		Index:           result.Cluster.Index,
		Active:          result.Cluster.Active,
		Balance:         result.Cluster.Balance,
	}

	return event, nil
}

func (p *EventParser) parseClusterDeposited(log *types.Log) (*ClusterDepositedEvent, error) {
	event := &ClusterDepositedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Value       *big.Int
		Cluster     struct {
			ValidatorCount  uint32
			NetworkFeeIndex uint64
			Index           uint64
			Active          bool
			Balance         *big.Int
		}
	}

	err := p.abi.UnpackIntoInterface(&result, "ClusterDeposited", log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterDeposited: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Value = result.Value
	event.Cluster = Cluster{
		ValidatorCount:  result.Cluster.ValidatorCount,
		NetworkFeeIndex: result.Cluster.NetworkFeeIndex,
		Index:           result.Cluster.Index,
		Active:          result.Cluster.Active,
		Balance:         result.Cluster.Balance,
	}

	return event, nil
}

// EncodeEventToJSON encodes a parsed event to JSON for storage.
func EncodeEventToJSON(event interface{}) (json.RawMessage, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}
	return json.RawMessage(data), nil
}

// EncodeLogToJSON encodes an Ethereum log to JSON for storage.
func EncodeLogToJSON(log *types.Log) (json.RawMessage, error) {
	// Convert to simpler structure for JSON storage
	logData := map[string]interface{}{
		"address": log.Address.Hex(),
		"topics":  make([]string, len(log.Topics)),
		"data":    common.Bytes2Hex(log.Data),
	}

	for i, topic := range log.Topics {
		logData["topics"].([]string)[i] = topic.Hex()
	}

	data, err := json.Marshal(logData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal log: %w", err)
	}

	return json.RawMessage(data), nil
}
