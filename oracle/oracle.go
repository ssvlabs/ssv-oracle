package oracle

import (
	"context"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/core/types"

	"ssv-oracle/contract"
	"ssv-oracle/eth/beacon"
	"ssv-oracle/eth/syncer"
	"ssv-oracle/logger"
	"ssv-oracle/merkle"
	"ssv-oracle/storage"
	"ssv-oracle/txmanager"
)

// balanceFloorGwei is 32 ETH in Gwei. Per spec, if a validator's effective balance
// is below 32 ETH, it is rounded up to 32 ETH for cluster sum calculations.
const balanceFloorGwei = 32_000_000_000

// Config holds Oracle configuration.
type Config struct {
	Storage        *storage.Storage
	ContractClient *contract.Client
	Syncer         *syncer.EventSyncer
	BeaconClient   *beacon.Client
	Schedule       CommitSchedule
}

// Oracle commits merkle roots of cluster effective balances to the SSV contract.
type Oracle struct {
	storage        oracleStorage
	contractClient *contract.Client
	syncer         *syncer.EventSyncer
	beaconClient   *beacon.Client
	schedule       CommitSchedule

	nextTarget uint64
}

type oracleStorage interface {
	GetActiveValidators(ctx context.Context) ([]storage.ActiveValidator, error)
	InsertPendingCommit(ctx context.Context, roundID, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []storage.ClusterBalance) error
	UpdateCommitStatus(ctx context.Context, roundID uint64, status storage.CommitStatus, txHash []byte) error
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

	// The finalized checkpoint epoch is the epoch boundary (slot = epoch × SLOTS_PER_EPOCH),
	// so the previous epoch (Epoch - 1) is fully finalized.
	checkpoint, err := o.beaconClient.GetFinalizedCheckpoint(ctx)
	if err != nil {
		return fmt.Errorf("failed to get initial checkpoint: %w", err)
	}
	fullyFinalized := checkpoint.Epoch - 1
	o.nextTarget = o.schedule.NextTarget(fullyFinalized)

	phase := o.schedule.PhaseAt(fullyFinalized)
	logger.Infow("Oracle started",
		"fullyFinalized", fullyFinalized,
		"waitingFor", o.nextTarget,
		"phaseStart", phase.StartEpoch,
		"interval", phase.Interval)

	// Main loop: react to finalized checkpoint events
	for {
		select {
		case <-ctx.Done():
			logger.Info("Oracle stopping")
			return ctx.Err()

		case checkpoint, ok := <-finalizedCh:
			if !ok {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("finalized checkpoint subscription closed")
			}

			// checkpoint.Epoch - 1 is the fully finalized epoch
			fullyFinalized := checkpoint.Epoch - 1

			// Not yet at target
			if fullyFinalized < o.nextTarget {
				logger.Debugw("Waiting for target",
					"fullyFinalized", fullyFinalized,
					"nextTarget", o.nextTarget)
				continue
			}

			// Target already passed - can happen if finalization was delayed
			if fullyFinalized > o.nextTarget {
				missed := o.nextTarget
				o.nextTarget = o.schedule.NextTarget(fullyFinalized)
				logger.Warnw("Missed commit target",
					"missed", missed,
					"fullyFinalized", fullyFinalized,
					"nextTarget", o.nextTarget)
				continue
			}

			// Exactly on target - commit
			if err := o.commit(ctx, checkpoint, o.nextTarget); err != nil {
				logger.Errorw("Commit failed", "target", o.nextTarget, "error", err)
				continue
			}

			o.nextTarget = o.schedule.NextTarget(fullyFinalized)
			logger.Infow("Waiting for finalization", "nextTarget", o.nextTarget)
		}
	}
}

// commit performs a single commit for the given target epoch.
func (o *Oracle) commit(ctx context.Context, checkpoint *beacon.FinalizedCheckpoint, target uint64) error {
	round := o.schedule.RoundAt(target)
	log := logger.With("target", target, "round", round)

	log.Infow("Committing",
		"fullyFinalized", checkpoint.Epoch-1,
		"referenceBlock", checkpoint.BlockNum)

	if err := o.syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return fmt.Errorf("sync to block %d: %w", checkpoint.BlockNum, err)
	}

	clusterBalances, err := o.fetchClusterBalances(ctx, checkpoint.StateRoot.String())
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

	if err := o.storage.UpdateCommitStatus(ctx, round, storage.CommitStatusConfirmed, receipt.TxHash.Bytes()); err != nil {
		log.Warnw("Failed to update commit status", "error", err)
	}

	log.Infow("Committed", "txHash", receipt.TxHash.Hex())
	return nil
}

func (o *Oracle) buildMerkleRoot(balances []storage.ClusterBalance) [32]byte {
	clusterMap := make(map[[32]byte]uint64)
	for _, bal := range balances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}
	return merkle.BuildMerkleTree(clusterMap)
}

func (o *Oracle) fetchClusterBalances(ctx context.Context, stateRoot string) ([]storage.ClusterBalance, error) {
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
	balanceMap, err := o.beaconClient.GetValidatorBalances(ctx, stateRoot, pubkeys)
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

func (o *Oracle) deduplicatePubkeys(validators []storage.ActiveValidator) [][]byte {
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

func (o *Oracle) aggregateByCluster(validators []storage.ActiveValidator, balanceMap map[phase0.BLSPubKey]uint64) ([]storage.ClusterBalance, int) {
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

	var result []storage.ClusterBalance
	for clusterID, total := range clusterTotals {
		result = append(result, storage.ClusterBalance{
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
	if statusErr := o.storage.UpdateCommitStatus(ctx, round, storage.CommitStatusFailed, txHash); statusErr != nil {
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
