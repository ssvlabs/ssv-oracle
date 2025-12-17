package syncer

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

// NewParser creates a new event parser.
func NewParser() *EventParser {
	return &EventParser{abi: contract.SSVNetworkABI}
}

// ParseLog parses an Ethereum log into a structured event.
func (p *EventParser) ParseLog(log *types.Log) (string, any, error) {
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
	case EventSigClusterMigratedToETH:
		event, err := p.parseClusterMigratedToETH(log)
		return EventClusterMigratedToETH, event, err
	case EventSigClusterBalanceUpdated:
		event, err := p.parseClusterBalanceUpdated(log)
		return EventClusterBalanceUpdated, event, err
	default:
		return "", nil, fmt.Errorf("unknown event signature: %s", eventSig.Hex())
	}
}

func (p *EventParser) parseValidatorAdded(log *types.Log) (*ValidatorAddedEvent, error) {
	event := &ValidatorAddedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		PublicKey   []byte
		Shares      []byte
		Cluster     Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventValidatorAdded, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ValidatorAdded: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.PublicKey = result.PublicKey
	event.Shares = result.Shares
	event.Cluster = result.Cluster

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
		Cluster     Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventValidatorRemoved, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ValidatorRemoved: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.PublicKey = result.PublicKey
	event.Cluster = result.Cluster

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
		Cluster     Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventClusterLiquidated, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterLiquidated: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Cluster = result.Cluster

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
		Cluster     Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventClusterReactivated, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterReactivated: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Cluster = result.Cluster

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
		Cluster     Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventClusterWithdrawn, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterWithdrawn: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Value = result.Value
	event.Cluster = result.Cluster

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
		Cluster     Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventClusterDeposited, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterDeposited: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Value = result.Value
	event.Cluster = result.Cluster

	return event, nil
}

func (p *EventParser) parseClusterMigratedToETH(log *types.Log) (*ClusterMigratedToETHEvent, error) {
	event := &ClusterMigratedToETHEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds  []uint64
		ETHDeposited *big.Int
		SSVRefunded  *big.Int
		Cluster      Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventClusterMigratedToETH, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterMigratedToETH: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.ETHDeposited = result.ETHDeposited
	event.SSVRefunded = result.SSVRefunded
	event.Cluster = result.Cluster

	return event, nil
}

func (p *EventParser) parseClusterBalanceUpdated(log *types.Log) (*ClusterBalanceUpdatedEvent, error) {
	event := &ClusterBalanceUpdatedEvent{}

	if len(log.Topics) < 3 {
		return nil, fmt.Errorf("missing indexed topics (owner, blockNum)")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())
	event.BlockNum = log.Topics[2].Big().Uint64()

	var result struct {
		OperatorIDs      []uint64
		EffectiveBalance *big.Int
		VUnits           uint64
		Cluster          Cluster
	}

	err := p.abi.UnpackIntoInterface(&result, EventClusterBalanceUpdated, log.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack ClusterBalanceUpdated: %w", err)
	}

	event.OperatorIDs = result.OperatorIDs
	event.EffectiveBalance = result.EffectiveBalance
	event.VUnits = result.VUnits
	event.Cluster = result.Cluster

	return event, nil
}

// EncodeEventToJSON encodes a parsed event to JSON for storage.
func EncodeEventToJSON(event any) (json.RawMessage, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}
	return data, nil
}

// EncodeLogToJSON encodes an Ethereum log to JSON for storage.
func EncodeLogToJSON(log *types.Log) (json.RawMessage, error) {
	logData := map[string]any{
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

	return data, nil
}
