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

const (
	errorRetryDelay  = 10 * time.Second
	balanceFloorGwei = 32_000_000_000
)

// Config holds Oracle configuration.
type Config struct {
	Storage        *ethsync.Storage
	ContractClient *contract.Client
	Syncer         *ethsync.EventSyncer
	BeaconClient   *ethsync.BeaconClient
	Phases         []CommitPhase
}

// Oracle commits merkle roots of cluster effective balances to the SSV contract.
type Oracle struct {
	storage        storage
	contractClient *contract.Client
	syncer         *ethsync.EventSyncer
	beaconClient   *ethsync.BeaconClient
	phases         []CommitPhase

	finalizedCh <-chan *ethsync.FinalizedCheckpoint
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
		phases:         cfg.Phases,
	}
}

// Run starts the oracle main loop, committing roots at each target epoch.
func (o *Oracle) Run(ctx context.Context) error {
	var err error
	o.finalizedCh, err = o.beaconClient.SubscribeFinalizedCheckpoints(ctx)
	if err != nil {
		return fmt.Errorf("failed to subscribe to finalized checkpoints: %w", err)
	}
	logger.Info("Subscribed to finalized checkpoint events")

	// On startup, skip current target epoch and wait for the next one.
	// This avoids attempting to commit for epochs that may already be committed.
	checkpoint, err := o.beaconClient.GetFinalizedCheckpoint(ctx)
	if err != nil {
		return fmt.Errorf("failed to get initial checkpoint: %w", err)
	}
	lastTargetEpoch := NextTargetEpoch(o.phases, checkpoint.Epoch)
	logger.Infow("Oracle started, waiting for next target epoch",
		"currentTarget", lastTargetEpoch,
		"currentEpoch", o.beaconClient.CurrentEpoch())

	for {
		targetEpoch, err := o.processNextCommit(ctx, lastTargetEpoch)
		if err != nil {
			if ctx.Err() != nil {
				logger.Info("Oracle stopping")
				return ctx.Err()
			}
			logger.Errorw("Commit failed, retrying", "error", err, "retryDelay", errorRetryDelay)
			select {
			case <-ctx.Done():
				logger.Info("Oracle stopping")
				return ctx.Err()
			case <-time.After(errorRetryDelay):
			}
			continue
		}
		lastTargetEpoch = targetEpoch
	}
}

func (o *Oracle) processNextCommit(ctx context.Context, lastTargetEpoch uint64) (uint64, error) {
	// Wait for a finalized checkpoint that gives us a new target epoch
	checkpoint, targetEpoch, err := o.waitForNewTarget(ctx, lastTargetEpoch)
	if err != nil {
		return 0, err
	}

	phase := GetPhaseForEpoch(o.phases, targetEpoch)
	round := RoundInPhase(phase, targetEpoch)

	log := logger.With("targetEpoch", targetEpoch, "round", round)
	log.Infow("Epoch finalized",
		"currentEpoch", o.beaconClient.CurrentEpoch(),
		"checkpointEpoch", checkpoint.Epoch,
		"checkpointBlock", checkpoint.BlockNum)

	if err := o.syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return 0, fmt.Errorf("failed to sync to block %d: %w", checkpoint.BlockNum, err)
	}

	clusterBalances, err := o.fetchClusterBalances(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch balances: %w", err)
	}

	merkleRoot := o.buildMerkleRoot(clusterBalances)
	log.Infow("Merkle tree built",
		"root", fmt.Sprintf("0x%x", merkleRoot),
		"clusters", len(clusterBalances))

	if err := o.storage.InsertPendingCommit(ctx, round, targetEpoch, merkleRoot[:], checkpoint.BlockNum, clusterBalances); err != nil {
		return 0, fmt.Errorf("failed to store pending commit: %w", err)
	}

	receipt, err := o.contractClient.CommitRoot(ctx, merkleRoot, checkpoint.BlockNum, round, targetEpoch)
	if err != nil {
		return o.handleCommitError(ctx, log, round, targetEpoch, receipt, err)
	}

	if err := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusConfirmed, receipt.TxHash.Bytes()); err != nil {
		log.Warnw("Failed to update commit status", "error", err)
	}

	log.Infow("Committed", "txHash", receipt.TxHash.Hex())
	return targetEpoch, nil
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
		return nil, fmt.Errorf("failed to get active validators: %w", err)
	}

	if len(validators) == 0 {
		logger.Info("No active validators")
		return nil, nil
	}

	pubkeys := o.deduplicatePubkeys(validators)

	start := time.Now()
	balanceMap, err := o.beaconClient.GetFinalizedValidatorBalances(ctx, pubkeys)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch validator balances: %w", err)
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
	round, targetEpoch uint64,
	receipt *types.Receipt,
	err error,
) (uint64, error) {
	var txHash []byte
	if receipt != nil {
		txHash = receipt.TxHash.Bytes()
	}
	if statusErr := o.storage.UpdateCommitStatus(ctx, round, ethsync.CommitStatusFailed, txHash); statusErr != nil {
		log.Warnw("Failed to update commit status", "error", statusErr)
	}

	if revertErr, ok := txmanager.IsRevertError(err); ok {
		log.Errorw("Commit reverted, skipping to next epoch",
			"reason", revertErr.Reason,
			"simulated", revertErr.Simulated)
		return targetEpoch, nil
	}

	return 0, fmt.Errorf("failed to commit: %w", err)
}

// waitForNewTarget returns the next finalized checkpoint with a new target epoch to commit.
func (o *Oracle) waitForNewTarget(ctx context.Context, lastTargetEpoch uint64) (*ethsync.FinalizedCheckpoint, uint64, error) {
	// Check if we can commit immediately
	if checkpoint, target, err := o.checkpointForTarget(ctx, lastTargetEpoch); err != nil {
		return nil, 0, err
	} else if checkpoint != nil {
		return checkpoint, target, nil
	}

	phase := GetPhaseForEpoch(o.phases, lastTargetEpoch)
	logger.Infow("Waiting for next finalization",
		"nextTarget", lastTargetEpoch+phase.Interval,
		"currentEpoch", o.beaconClient.CurrentEpoch())

	for {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case checkpoint, ok := <-o.finalizedCh:
			if !ok {
				return nil, 0, fmt.Errorf("finalized checkpoint subscription closed")
			}
			if checkpoint, target, _ := o.validateCheckpoint(checkpoint, lastTargetEpoch); checkpoint != nil {
				return checkpoint, target, nil
			}
		}
	}
}

// checkpointForTarget gets current checkpoint and validates it for committing.
func (o *Oracle) checkpointForTarget(ctx context.Context, lastTargetEpoch uint64) (*ethsync.FinalizedCheckpoint, uint64, error) {
	checkpoint, err := o.beaconClient.GetFinalizedCheckpoint(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get checkpoint: %w", err)
	}
	return o.validateCheckpoint(checkpoint, lastTargetEpoch)
}

// validateCheckpoint checks if checkpoint can be used to commit a new target.
func (o *Oracle) validateCheckpoint(checkpoint *ethsync.FinalizedCheckpoint, lastTargetEpoch uint64) (*ethsync.FinalizedCheckpoint, uint64, error) {
	targetEpoch := NextTargetEpoch(o.phases, checkpoint.Epoch)

	if targetEpoch <= lastTargetEpoch && lastTargetEpoch > 0 {
		return nil, 0, nil
	}

	logger.Infow("Found target epoch",
		"targetEpoch", targetEpoch,
		"checkpointEpoch", checkpoint.Epoch)

	return checkpoint, targetEpoch, nil
}
