package ethsync

import (
	"math/big"
	"sort"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/sha3"
)

// Event type constants for SSV contract events.

const (
	EventValidatorAdded        = "ValidatorAdded"
	EventValidatorRemoved      = "ValidatorRemoved"
	EventClusterLiquidated     = "ClusterLiquidated"
	EventClusterReactivated    = "ClusterReactivated"
	EventClusterWithdrawn      = "ClusterWithdrawn"
	EventClusterDeposited      = "ClusterDeposited"
	EventClusterMigratedToETH  = "ClusterMigratedToETH"
	EventClusterBalanceUpdated = "ClusterBalanceUpdated"
)

// Event signatures use Solidity tuple format: (type1,type2,...) NOT tuple(type1,type2,...)
var (
	EventSigValidatorAdded        = crypto.Keccak256Hash([]byte("ValidatorAdded(address,uint64[],bytes,bytes,(uint32,uint64,uint64,bool,uint256))"))
	EventSigValidatorRemoved      = crypto.Keccak256Hash([]byte("ValidatorRemoved(address,uint64[],bytes,(uint32,uint64,uint64,bool,uint256))"))
	EventSigClusterLiquidated     = crypto.Keccak256Hash([]byte("ClusterLiquidated(address,uint64[],(uint32,uint64,uint64,bool,uint256))"))
	EventSigClusterReactivated    = crypto.Keccak256Hash([]byte("ClusterReactivated(address,uint64[],(uint32,uint64,uint64,bool,uint256))"))
	EventSigClusterWithdrawn      = crypto.Keccak256Hash([]byte("ClusterWithdrawn(address,uint64[],uint256,(uint32,uint64,uint64,bool,uint256))"))
	EventSigClusterDeposited      = crypto.Keccak256Hash([]byte("ClusterDeposited(address,uint64[],uint256,(uint32,uint64,uint64,bool,uint256))"))
	EventSigClusterMigratedToETH  = crypto.Keccak256Hash([]byte("ClusterMigratedToETH(address,uint64[],uint256,uint256,(uint32,uint64,uint64,bool,uint256))"))
	EventSigClusterBalanceUpdated = crypto.Keccak256Hash([]byte("ClusterBalanceUpdated(address,uint64[],uint64,uint256,uint64,(uint32,uint64,uint64,bool,uint256))"))
)

// Cluster represents an SSV cluster state (matches ISSVNetworkCore.sol).
type Cluster struct {
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	Active          bool
	Balance         *big.Int
}

// ValidatorAddedEvent is emitted when a validator is registered.
type ValidatorAddedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	PublicKey   []byte
	Shares      []byte
	Cluster     Cluster
}

// ValidatorRemovedEvent is emitted when a validator is removed.
type ValidatorRemovedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	PublicKey   []byte
	Cluster     Cluster
}

// ClusterLiquidatedEvent is emitted when a cluster is liquidated.
type ClusterLiquidatedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Cluster     Cluster
}

// ClusterReactivatedEvent is emitted when a liquidated cluster is reactivated.
type ClusterReactivatedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Cluster     Cluster
}

// ClusterWithdrawnEvent is emitted when SSV tokens are withdrawn from a cluster.
type ClusterWithdrawnEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Value       *big.Int
	Cluster     Cluster
}

// ClusterDepositedEvent is emitted when SSV tokens are deposited to a cluster.
type ClusterDepositedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Value       *big.Int
	Cluster     Cluster
}

// ClusterMigratedToETHEvent is emitted when a cluster migrates to ETH payments.
type ClusterMigratedToETHEvent struct {
	Owner        common.Address
	OperatorIDs  []uint64
	ETHDeposited *big.Int
	SSVRefunded  *big.Int
	Cluster      Cluster
}

// ClusterBalanceUpdatedEvent is emitted when a cluster's effective balance is updated.
type ClusterBalanceUpdatedEvent struct {
	Owner            common.Address // indexed
	OperatorIDs      []uint64
	BlockNum         uint64 // indexed
	EffectiveBalance *big.Int
	VUnits           uint64
	Cluster          Cluster
}

// ComputeClusterID computes keccak256(abi.encodePacked(owner, uint256(op1), uint256(op2), ...))
// with operator IDs sorted ascending. Matches SSV contract's cluster ID computation.
func ComputeClusterID(owner common.Address, operatorIDs []uint64) [32]byte {
	sortedIDs := make([]uint64, len(operatorIDs))
	copy(sortedIDs, operatorIDs)
	sort.Slice(sortedIDs, func(i, j int) bool {
		return sortedIDs[i] < sortedIDs[j]
	})

	data := make([]byte, 0, common.AddressLength+(common.HashLength*len(sortedIDs)))
	data = append(data, owner.Bytes()...)

	for _, id := range sortedIDs {
		idBytes := make([]byte, common.HashLength)
		new(big.Int).SetUint64(id).FillBytes(idBytes)
		data = append(data, idBytes...)
	}

	hash := sha3.NewLegacyKeccak256()
	hash.Write(data)
	var result [32]byte
	hash.Sum(result[:0])

	return result
}

// Spec holds beacon chain specification parameters.
type Spec struct {
	GenesisTime   time.Time
	SlotsPerEpoch uint64
	SlotDuration  time.Duration
}

// SlotAt returns the slot number at the given time.
func (s *Spec) SlotAt(t time.Time) uint64 {
	if t.Before(s.GenesisTime) {
		return 0
	}
	return uint64(t.Sub(s.GenesisTime) / s.SlotDuration)
}

// EpochAtTimestamp returns the epoch number for a given Unix timestamp.
func (s *Spec) EpochAtTimestamp(timestamp uint64) uint64 {
	t := time.Unix(int64(timestamp), 0)
	if t.Before(s.GenesisTime) {
		return 0
	}
	elapsed := t.Sub(s.GenesisTime)
	epochDuration := s.SlotDuration * time.Duration(s.SlotsPerEpoch)
	return uint64(elapsed / epochDuration)
}
