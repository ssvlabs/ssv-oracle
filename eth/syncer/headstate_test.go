package syncer

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ssvlabs/ssv-oracle/storage"
)

func TestApplyClusterEventToOverlay_InSet_AppliesPostEventState(t *testing.T) {
	owner := common.HexToAddress("0xaabbcc")
	operatorIDs := []uint64{1, 2, 3, 4}
	idArr := computeClusterID(owner, operatorIDs)

	overlay := map[[32]byte]storage.ClusterRow{
		idArr: {IsActive: true, Balance: big.NewInt(100)},
	}
	requested := map[[32]byte]struct{}{idArr: {}}

	event := &clusterLiquidatedEvent{
		Owner:       owner,
		OperatorIDs: operatorIDs,
		Cluster:     cluster{Active: false, Balance: big.NewInt(0)},
	}

	applyClusterEventToOverlay(overlay, requested, event)

	got := overlay[idArr]
	if got.IsActive {
		t.Error("expected IsActive=false after Liquidated")
	}
	if got.Balance.Int64() != 0 {
		t.Errorf("expected Balance=0 after Liquidated, got %d", got.Balance.Int64())
	}
}

func TestApplyClusterEventToOverlay_OutOfSet_DoesNotPolluteOverlay(t *testing.T) {
	inSetOwner := common.HexToAddress("0x111")
	inSetOps := []uint64{1, 2, 3, 4}
	inSetID := computeClusterID(inSetOwner, inSetOps)

	outOfSetOwner := common.HexToAddress("0x222")
	outOfSetOps := []uint64{5, 6, 7, 8}
	outOfSetID := computeClusterID(outOfSetOwner, outOfSetOps)

	overlay := map[[32]byte]storage.ClusterRow{
		inSetID: {IsActive: true, Balance: big.NewInt(100)},
	}
	requested := map[[32]byte]struct{}{inSetID: {}}

	event := &clusterLiquidatedEvent{
		Owner:       outOfSetOwner,
		OperatorIDs: outOfSetOps,
		Cluster:     cluster{Active: false},
	}

	applyClusterEventToOverlay(overlay, requested, event)

	if !overlay[inSetID].IsActive {
		t.Error("in-set entry must not be touched by out-of-set event")
	}
	if _, exists := overlay[outOfSetID]; exists {
		t.Error("out-of-set entry must not be added to overlay")
	}
}

func TestApplyClusterEventToOverlay_MultipleEventsSameCluster_LastWins(t *testing.T) {
	owner := common.HexToAddress("0xaabbcc")
	operatorIDs := []uint64{1, 2, 3, 4}
	idArr := computeClusterID(owner, operatorIDs)

	overlay := map[[32]byte]storage.ClusterRow{}
	requested := map[[32]byte]struct{}{idArr: {}}

	applyClusterEventToOverlay(overlay, requested, &clusterLiquidatedEvent{
		Owner: owner, OperatorIDs: operatorIDs,
		Cluster: cluster{Active: false, Balance: big.NewInt(0)},
	})
	if overlay[idArr].IsActive {
		t.Fatal("expected inactive after Liquidated")
	}

	applyClusterEventToOverlay(overlay, requested, &clusterReactivatedEvent{
		Owner: owner, OperatorIDs: operatorIDs,
		Cluster: cluster{Active: true, Balance: big.NewInt(50)},
	})
	got := overlay[idArr]
	if !got.IsActive {
		t.Error("expected active after Reactivated")
	}
	if got.Balance.Int64() != 50 {
		t.Errorf("expected balance=50 after Reactivated, got %d", got.Balance.Int64())
	}
}
