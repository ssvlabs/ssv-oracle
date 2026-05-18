package syncer

import (
	"context"
	"errors"
	"fmt"

	"github.com/ssvlabs/ssv-oracle/eth/execution"
	"github.com/ssvlabs/ssv-oracle/storage"
)

// BuildHeadStateSnapshot returns an in-memory cluster-state overlay
// at the chain head for clusterIDs.
//
// The overlay seeds from finalized state in storage and replays head
// events from (lastSynced, headNum] on top. Events for out-of-set
// cluster IDs are parsed and discarded — cluster_id requires parsing
// OperatorIds from event data, so earlier filtering isn't possible.
//
// Returned ClusterRow values must not be mutated: the *big.Int
// Balance and []uint64 OperatorIDs are shared with the overlay.
//
// Parse failures on handled events are fatal. Unknown signatures
// are skipped.
func (s *EventSyncer) BuildHeadStateSnapshot(ctx context.Context, clusterIDs [][]byte) (map[[32]byte]storage.ClusterRow, error) {
	finalized, lastSynced, err := s.storage.GetFinalizedClusters(ctx, clusterIDs)
	if err != nil {
		return nil, fmt.Errorf("get finalized clusters: %w", err)
	}

	overlay := make(map[[32]byte]storage.ClusterRow, len(clusterIDs))
	for _, row := range finalized {
		if row == nil {
			continue
		}
		var key [32]byte
		copy(key[:], row.ClusterID)
		overlay[key] = *row
	}

	requested := make(map[[32]byte]struct{}, len(clusterIDs))
	for _, id := range clusterIDs {
		var key [32]byte
		copy(key[:], id)
		requested[key] = struct{}{}
	}

	headNum, err := s.client.GetHeadBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("get head block: %w", err)
	}
	if lastSynced >= headNum {
		return overlay, nil
	}

	// FetchLogs packs block timestamps in BlockLogs that go unused
	// here — cheaper than maintaining a parallel fetch path.
	err = s.client.FetchLogs(ctx, s.ssvContract, lastSynced+1, headNum, EventTopics(),
		func(_ uint64, batches []execution.BlockLogs) error {
			for _, bl := range batches {
				for i := range bl.Logs {
					log := &bl.Logs[i]
					_, eventData, perr := s.parser.parseLog(log)
					if perr != nil {
						if errors.Is(perr, errUnknownEvent) {
							continue
						}
						return fmt.Errorf("parse event at block %d log %d: %w",
							log.BlockNumber, log.Index, perr)
					}
					e, ok := eventData.(clusterEvent)
					if !ok {
						continue
					}
					applyClusterEventToOverlay(overlay, requested, e)
				}
			}
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("fetch overlay logs: %w", err)
	}

	return overlay, nil
}

// applyClusterEventToOverlay writes the post-event cluster state
// from e into overlay, only for cluster IDs in the requested set.
// Multiple events for the same cluster, applied in chain order,
// leave the overlay reflecting the last event.
func applyClusterEventToOverlay(
	overlay map[[32]byte]storage.ClusterRow,
	requested map[[32]byte]struct{},
	e clusterEvent,
) {
	owner, operatorIDs := e.clusterKey()
	idArr := computeClusterID(owner, operatorIDs)
	if _, inSet := requested[idArr]; !inSet {
		return
	}
	cluster := e.cluster()
	overlay[idArr] = storage.ClusterRow{
		ClusterID:       idArr[:],
		OwnerAddress:    owner.Bytes(),
		OperatorIDs:     operatorIDs,
		ValidatorCount:  cluster.ValidatorCount,
		NetworkFeeIndex: cluster.NetworkFeeIndex,
		Index:           cluster.Index,
		IsActive:        cluster.Active,
		Balance:         cluster.Balance,
	}
}
