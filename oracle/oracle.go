package oracle

import (
	"context"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/contract"
	"ssv-oracle/merkle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
	"ssv-oracle/txmanager"
)

const balanceFloorGwei = 32_000_000_000

// Config holds Oracle configuration.
type Config struct {
	Storage        *ethsync.Storage
	ContractClient *contract.Client
	Syncer         *ethsync.EventSyncer
	BeaconClient   *ethsync.BeaconClient
	Schedule       CommitSchedule
}

// Oracle commits merkle roots of cluster effective balances to the SSV contract.
type Oracle struct {
	storage        storage
	contractClient *contract.Client
	syncer         *ethsync.EventSyncer
	beaconClient   *ethsync.BeaconClient
	schedule       CommitSchedule

	lastCommitted uint64
}

type storage interface {
	GetActiveValidators(ctx context.Context) ([]ethsync.ActiveValidator, error)
	InsertPendingCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []ethsync.ClusterBalance) error
	UpdateCommitStatus(ctx context.Context, roundID uint64, status ethsync.CommitStatus, txHash []byte) error
}

// New creates a new Oracle instance.
func New(cfg *Config) *Oracle {
	return &Oracle{
		storage:        cfg.Storage,
		contractClient: cfg.ContractClient,
		syncer:         cfg.Syncer,
		beaconClient:   cfg.BeaconClient,
		schedule:       cfg.Schedule,
	}
}

// Run starts the oracle main loop, committing roots at each target epoch.
func (o *Oracle) Run(ctx context.Context) error {
	finalizedCh, err := o.beaconClient.SubscribeFinalizedCheckpoints(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to finalized checkpoints: %w", err)
	}
	logger.Info("Subscribed to finalized checkpoint events")

	// The finalized checkpoint epoch means the first block of that epoch was proposed,
	// so the previous epoch (Epoch - 1) is fully finalized.
	checkpoint, err := o.beaconClient.GetFinalizedCheckpoint(ctx)
	if err != nil {
		return fmt.Errorf("failed to get initial checkpoint: %w", err)
	}
	fullyFinalized := checkpoint.Epoch - 1
	o.lastCommitted = o.schedule.LatestTarget(fullyFinalized)

	phase := o.schedule.PhaseAt(o.lastCommitted)
	nextTarget := o.lastCommitted + phase.Interval
	logger.Infow("Oracle started",
		"skipping", o.lastCommitted,
		"waitingFor", nextTarget,
		"fullyFinalized", fullyFinalized)

	// Main loop: react to finalized checkpoint events
	for {
		select {
		case <-ctx.Done():
			logger.Info("Oracle stopping")
			return ctx.Err()

		case checkpoint, ok := <-finalizedCh:
			if !ok {
				return fmt.Errorf("finalized checkpoint subscription closed")
			}

			// checkpoint.Epoch - 1 is the fully finalized epoch
			fullyFinalized := checkpoint.Epoch - 1
			target := o.schedule.LatestTarget(fullyFinalized)
			if target == 0 || target <= o.lastCommitted {
				logger.Debugw("Skipping checkpoint",
					"fullyFinalized", fullyFinalized,
					"target", target,
					"lastCommitted", o.lastCommitted)
				continue
			}

			if err := o.commit(ctx, checkpoint, target); err != nil {
				logger.Errorw("Commit failed", "target", target, "error", err)
				continue
			}

			o.lastCommitted = target

			phase := o.schedule.PhaseAt(target)
			logger.Infow("Waiting for finalization", "nextTarget", target+phase.Interval)
		}
	}
}

// commit performs a single commit for the given target epoch.
func (o *Oracle) commit(ctx context.Context, checkpoint *ethsync.FinalizedCheckpoint, target uint64) error {
	round := o.schedule.RoundAt(target)
	log := logger.With("target", target, "round", round)

	log.Infow("Committing",
		"fullyFinalized", checkpoint.Epoch-1,
		"referenceBlock", checkpoint.BlockNum)

	if err := o.syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return fmt.Errorf("sync to block %d: %w", checkpoint.BlockNum, err)
	}

	clusterBalances, err := o.fetchClusterBalances(ctx)
	if err != nil {
		return fmt.Errorf("fetch balances: %w", err)
	}

	merkleRoot := o.buildMerkleRoot(clusterBalances)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", merkleRoot),
		"clusters", len(clusterBalances))

	if err := o.storage.InsertPendingCommit(ctx, round, target, merkleRoot[:], checkpoint.BlockNum, clusterBalances); err != nil {
		return fmt.Errorf("store pending commit: %w", err)
	}

	receipt, err := o.contractClient.CommitRoot(ctx, merkleRoot, checkpoint.BlockNum, round, target)
	if err != nil {
		return o.handleCommitError(ctx, log, round, target, receipt, err)
	}

	if err := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusConfirmed, receipt.TxHash.Bytes()); err != nil {
		log.Warnw("Failed to update commit status", "error", err)
	}

	log.Infow("Committed", "txHash", receipt.TxHash.Hex())
	return nil
}

func (o *Oracle) buildMerkleRoot(balances []ethsync.ClusterBalance) [32]byte {
	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range balances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}
	return merkle.BuildMerkleTree(clusterMap)
}

func (o *Oracle) fetchClusterBalances(ctx context.Context) ([]ethsync.ClusterBalance, error) {
	validators, err := o.storage.GetActiveValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("get active validators: %w", err)
	}

	if len(validators) == 0 {
		logger.Info("No active validators")
		return nil, nil
	}

	pubkeys := o.deduplicatePubkeys(validators)

	start := time.Now()
	balanceMap, err := o.beaconClient.GetFinalizedValidatorBalances(ctx, pubkeys)
	if err != nil {
		return nil, fmt.Errorf("fetch validator balances: %w", err)
	}

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	logger.Infow("Balances fetched",
		"validators", len(validators),
		"fromBeacon", len(balanceMap),
		"notOnBeacon", notOnBeacon,
		"clusters", len(result),
		"took", time.Since(start).Round(time.Millisecond))

	return result, nil
}

func (o *Oracle) deduplicatePubkeys(validators []ethsync.ActiveValidator) [][]byte {
	seen := make(map[phase0.BLSPubKey]struct{})
	var pubkeys [][]byte
	for _, v := range validators {
		var pk phase0.BLSPubKey
		copy(pk[:], v.ValidatorPubkey)
		if _, exists := seen[pk]; !exists {
			seen[pk] = struct{}{}
			pubkeys = append(pubkeys, v.ValidatorPubkey)
		}
	}
	return pubkeys
}

func (o *Oracle) aggregateByCluster(validators []ethsync.ActiveValidator, balanceMap map[phase0.BLSPubKey]uint64) ([]ethsync.ClusterBalance, int) {
	clusterTotals := make(map[[32]byte]uint64)
	var notOnBeacon int

	for _, v := range validators {
		var pk phase0.BLSPubKey
		copy(pk[:], v.ValidatorPubkey)

		balance, onBeacon := balanceMap[pk]
		if !onBeacon {
			notOnBeacon++
		}
		if balance < balanceFloorGwei {
			balance = balanceFloorGwei
		}

		var clusterID [32]byte
		copy(clusterID[:], v.ClusterID)
		clusterTotals[clusterID] += balance
	}

	var result []ethsync.ClusterBalance
	for clusterID, total := range clusterTotals {
		result = append(result, ethsync.ClusterBalance{
			ClusterID:        clusterID[:],
			EffectiveBalance: total,
		})
	}

	return result, notOnBeacon
}

func (o *Oracle) handleCommitError(
	ctx context.Context,
	log logger.Logger,
	round, target uint64,
	receipt *types.Receipt,
	err error,
) error {
	var txHash []byte
	if receipt != nil {
		txHash = receipt.TxHash.Bytes()
	}
	if statusErr := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusFailed, txHash); statusErr != nil {
		log.Warnw("Failed to update commit status", "error", statusErr)
	}

	if revertErr, ok := txmanager.IsRevertError(err); ok {
		log.Errorw("Commit reverted, skipping",
			"reason", revertErr.Reason,
			"simulated", revertErr.Simulated)
		return nil // Skip to next target
	}

	return fmt.Errorf("commit failed: %w", err)
}
