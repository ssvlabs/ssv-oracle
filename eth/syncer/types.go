package syncer

import (
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/sha3"
)

const (
	eventValidatorAdded        = "ValidatorAdded"
	eventValidatorRemoved      = "ValidatorRemoved"
	eventClusterLiquidated     = "ClusterLiquidated"
	eventClusterReactivated    = "ClusterReactivated"
	eventClusterWithdrawn      = "ClusterWithdrawn"
	eventClusterDeposited      = "ClusterDeposited"
	eventClusterMigratedToETH  = "ClusterMigratedToETH"
	eventClusterBalanceUpdated = "ClusterBalanceUpdated"
)

var (
	eventSigValidatorAdded        = crypto.Keccak256Hash([]byte("ValidatorAdded(address,uint64[],bytes,bytes,(uint32,uint64,uint64,bool,uint256))"))
	eventSigValidatorRemoved      = crypto.Keccak256Hash([]byte("ValidatorRemoved(address,uint64[],bytes,(uint32,uint64,uint64,bool,uint256))"))
	eventSigClusterLiquidated     = crypto.Keccak256Hash([]byte("ClusterLiquidated(address,uint64[],(uint32,uint64,uint64,bool,uint256))"))
	eventSigClusterReactivated    = crypto.Keccak256Hash([]byte("ClusterReactivated(address,uint64[],(uint32,uint64,uint64,bool,uint256))"))
	eventSigClusterWithdrawn      = crypto.Keccak256Hash([]byte("ClusterWithdrawn(address,uint64[],uint256,(uint32,uint64,uint64,bool,uint256))"))
	eventSigClusterDeposited      = crypto.Keccak256Hash([]byte("ClusterDeposited(address,uint64[],uint256,(uint32,uint64,uint64,bool,uint256))"))
	eventSigClusterMigratedToETH  = crypto.Keccak256Hash([]byte("ClusterMigratedToETH(address,uint64[],uint256,uint256,uint32,(uint32,uint64,uint64,bool,uint256))"))
	eventSigClusterBalanceUpdated = crypto.Keccak256Hash([]byte("ClusterBalanceUpdated(address,uint64[],uint64,uint32,(uint32,uint64,uint64,bool,uint256))"))
)

type cluster struct {
	ValidatorCount  uint32
	NetworkFeeIndex uint64
	Index           uint64
	Active          bool
	Balance         *big.Int
}

type validatorAddedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	PublicKey   []byte
	Shares      []byte
	Cluster     cluster
}

type validatorRemovedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	PublicKey   []byte
	Cluster     cluster
}

type clusterLiquidatedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Cluster     cluster
}

type clusterReactivatedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Cluster     cluster
}

type clusterWithdrawnEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Value       *big.Int
	Cluster     cluster
}

type clusterDepositedEvent struct {
	Owner       common.Address
	OperatorIDs []uint64
	Value       *big.Int
	Cluster     cluster
}

type clusterMigratedToETHEvent struct {
	Owner            common.Address
	OperatorIDs      []uint64
	ETHDeposited     *big.Int
	SSVRefunded      *big.Int
	EffectiveBalance uint32
	Cluster          cluster
}

type clusterBalanceUpdatedEvent struct {
	Owner            common.Address // indexed
	OperatorIDs      []uint64
	BlockNum         uint64 // indexed
	EffectiveBalance uint32
	Cluster          cluster
}

// computeClusterID matches the contract's cluster ID computation.
func computeClusterID(owner common.Address, operatorIDs []uint64) [32]byte {
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

// clusterEvent is implemented by all events that affect cluster state.
type clusterEvent interface {
	clusterKey() (common.Address, []uint64)
	cluster() *cluster
}

func (e *validatorAddedEvent) clusterKey() (common.Address, []uint64) { return e.Owner, e.OperatorIDs }
func (e *validatorAddedEvent) cluster() *cluster                      { return &e.Cluster }

func (e *validatorRemovedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *validatorRemovedEvent) cluster() *cluster { return &e.Cluster }

func (e *clusterLiquidatedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *clusterLiquidatedEvent) cluster() *cluster { return &e.Cluster }

func (e *clusterReactivatedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *clusterReactivatedEvent) cluster() *cluster { return &e.Cluster }

func (e *clusterWithdrawnEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *clusterWithdrawnEvent) cluster() *cluster { return &e.Cluster }

func (e *clusterDepositedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *clusterDepositedEvent) cluster() *cluster { return &e.Cluster }

func (e *clusterMigratedToETHEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *clusterMigratedToETHEvent) cluster() *cluster { return &e.Cluster }

func (e *clusterBalanceUpdatedEvent) clusterKey() (common.Address, []uint64) {
	return e.Owner, e.OperatorIDs
}
func (e *clusterBalanceUpdatedEvent) cluster() *cluster { return &e.Cluster }
