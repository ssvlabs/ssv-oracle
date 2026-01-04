package syncer

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/contract"
)

// errUnknownEvent is returned when the event signature is not recognized.
// This is expected for events we don't handle (e.g., RootCommitted, OperatorAdded).
var errUnknownEvent = errors.New("unknown event signature")

type eventParser struct {
	abi abi.ABI
}

func newParser() *eventParser {
	return &eventParser{abi: contract.SSVNetworkABI}
}

func (p *eventParser) parseLog(log *types.Log) (string, any, error) {
	if len(log.Topics) == 0 {
		return "", nil, fmt.Errorf("log has no topics")
	}

	eventSig := log.Topics[0]

	switch eventSig {
	case eventSigValidatorAdded:
		event, err := p.parseValidatorAdded(log)
		return eventValidatorAdded, event, err
	case eventSigValidatorRemoved:
		event, err := p.parseValidatorRemoved(log)
		return eventValidatorRemoved, event, err
	case eventSigClusterLiquidated:
		event, err := p.parseClusterLiquidated(log)
		return eventClusterLiquidated, event, err
	case eventSigClusterReactivated:
		event, err := p.parseClusterReactivated(log)
		return eventClusterReactivated, event, err
	case eventSigClusterWithdrawn:
		event, err := p.parseClusterWithdrawn(log)
		return eventClusterWithdrawn, event, err
	case eventSigClusterDeposited:
		event, err := p.parseClusterDeposited(log)
		return eventClusterDeposited, event, err
	case eventSigClusterMigratedToETH:
		event, err := p.parseClusterMigratedToETH(log)
		return eventClusterMigratedToETH, event, err
	case eventSigClusterBalanceUpdated:
		event, err := p.parseClusterBalanceUpdated(log)
		return eventClusterBalanceUpdated, event, err
	default:
		return "", nil, fmt.Errorf("%w: %s", errUnknownEvent, eventSig.Hex())
	}
}

func (p *eventParser) parseValidatorAdded(log *types.Log) (*validatorAddedEvent, error) {
	event := &validatorAddedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		PublicKey   []byte
		Shares      []byte
		Cluster     cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventValidatorAdded, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ValidatorAdded: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.PublicKey = result.PublicKey
	event.Shares = result.Shares
	event.Cluster = result.Cluster

	return event, nil
}

func (p *eventParser) parseValidatorRemoved(log *types.Log) (*validatorRemovedEvent, error) {
	event := &validatorRemovedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		PublicKey   []byte
		Cluster     cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventValidatorRemoved, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ValidatorRemoved: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.PublicKey = result.PublicKey
	event.Cluster = result.Cluster

	return event, nil
}

func (p *eventParser) parseClusterLiquidated(log *types.Log) (*clusterLiquidatedEvent, error) {
	event := &clusterLiquidatedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Cluster     cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventClusterLiquidated, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ClusterLiquidated: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Cluster = result.Cluster

	return event, nil
}

func (p *eventParser) parseClusterReactivated(log *types.Log) (*clusterReactivatedEvent, error) {
	event := &clusterReactivatedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Cluster     cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventClusterReactivated, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ClusterReactivated: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Cluster = result.Cluster

	return event, nil
}

func (p *eventParser) parseClusterWithdrawn(log *types.Log) (*clusterWithdrawnEvent, error) {
	event := &clusterWithdrawnEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Value       *big.Int
		Cluster     cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventClusterWithdrawn, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ClusterWithdrawn: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Value = result.Value
	event.Cluster = result.Cluster

	return event, nil
}

func (p *eventParser) parseClusterDeposited(log *types.Log) (*clusterDepositedEvent, error) {
	event := &clusterDepositedEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds []uint64
		Value       *big.Int
		Cluster     cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventClusterDeposited, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ClusterDeposited: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.Value = result.Value
	event.Cluster = result.Cluster

	return event, nil
}

func (p *eventParser) parseClusterMigratedToETH(log *types.Log) (*clusterMigratedToETHEvent, error) {
	event := &clusterMigratedToETHEvent{}

	if len(log.Topics) < 2 {
		return nil, fmt.Errorf("missing owner topic")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())

	var result struct {
		OperatorIds      []uint64
		EthDeposited     *big.Int
		SsvRefunded      *big.Int
		EffectiveBalance uint32
		Cluster          cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventClusterMigratedToETH, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ClusterMigratedToETH: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.ETHDeposited = result.EthDeposited
	event.SSVRefunded = result.SsvRefunded
	event.EffectiveBalance = result.EffectiveBalance
	event.Cluster = result.Cluster

	return event, nil
}

func (p *eventParser) parseClusterBalanceUpdated(log *types.Log) (*clusterBalanceUpdatedEvent, error) {
	event := &clusterBalanceUpdatedEvent{}

	if len(log.Topics) < 3 {
		return nil, fmt.Errorf("missing indexed topics (owner, blockNum)")
	}
	event.Owner = common.BytesToAddress(log.Topics[1].Bytes())
	event.BlockNum = log.Topics[2].Big().Uint64()

	var result struct {
		OperatorIds      []uint64
		EffectiveBalance uint32
		Cluster          cluster
	}

	err := p.abi.UnpackIntoInterface(&result, eventClusterBalanceUpdated, log.Data)
	if err != nil {
		return nil, fmt.Errorf("unpack ClusterBalanceUpdated: %w", err)
	}

	event.OperatorIDs = result.OperatorIds
	event.EffectiveBalance = result.EffectiveBalance
	event.Cluster = result.Cluster

	return event, nil
}
