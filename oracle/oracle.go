package oracle

import (
	"context"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/ssvlabs/ssv-oracle/contract"
	"github.com/ssvlabs/ssv-oracle/eth/beacon"
	"github.com/ssvlabs/ssv-oracle/eth/syncer"
	"github.com/ssvlabs/ssv-oracle/logger"
	"github.com/ssvlabs/ssv-oracle/merkle"
	"github.com/ssvlabs/ssv-oracle/storage"
	"github.com/ssvlabs/ssv-oracle/txmanager"
)

const (
	// gweiPerETH is the number of gwei in one ETH.
	gweiPerETH = 1_000_000_000

	// balanceFloorGwei is 32 ETH in Gwei. Per spec, if a validator's effective balance
	// is below 32 ETH, it is rounded up to 32 ETH for cluster sum calculations.
	balanceFloorGwei = 32 * gweiPerETH
)

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
	InsertPendingCommit(ctx context.Context, targetEpoch uint64, merkleRoot []byte, referenceBlock uint64, clusterBalances []storage.ClusterBalance) error
	UpdateCommitStatus(ctx context.Context, targetEpoch uint64, status storage.CommitStatus, txHash []byte) error
}

// New creates a new Oracle instance.
// Schedule must be validated before calling (via CommitSchedule.Validate).
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
		return fmt.Errorf("subscribe to finalized checkpoints: %w", err)
	}
	logger.Info("Subscribed to finalized checkpoint events")

	// The finalized checkpoint epoch is the epoch boundary (slot = epoch × SLOTS_PER_EPOCH),
	// so the previous epoch (Epoch - 1) is fully finalized.
	finalizedEpoch, err := o.beaconClient.GetFinalizedEpoch(ctx)
	if err != nil {
		return fmt.Errorf("get finalized epoch: %w", err)
	}
	if finalizedEpoch == 0 {
		return fmt.Errorf("cannot start at genesis (epoch 0): no fully finalized epoch exists")
	}
	fullyFinalized := finalizedEpoch - 1
	o.nextTarget = o.schedule.NextTarget(fullyFinalized)

	phase := o.schedule.PhaseAt(fullyFinalized)
	logger.Infow("Oracle started",
		"fullyFinalized", fullyFinalized,
		"waitingFor", o.nextTarget,
		"phaseStart", phase.StartEpoch,
		"interval", phase.Interval)

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

			if checkpoint.Epoch == 0 {
				continue
			}

			fullyFinalized = checkpoint.Epoch - 1

			if fullyFinalized < o.nextTarget {
				logger.Debugw("Waiting for target",
					"fullyFinalized", fullyFinalized,
					"nextTarget", o.nextTarget)
				continue
			}

			if fullyFinalized > o.nextTarget {
				missed := o.nextTarget
				o.nextTarget = o.schedule.NextTarget(fullyFinalized)
				logger.Warnw("Missed commit target",
					"missed", missed,
					"fullyFinalized", fullyFinalized,
					"nextTarget", o.nextTarget)
				continue
			}

			if err := o.commit(ctx, checkpoint, o.nextTarget); err != nil {
				failed := o.nextTarget
				o.nextTarget = o.schedule.NextTarget(fullyFinalized)
				logger.Errorw("Commit failed",
					"target", failed,
					"nextTarget", o.nextTarget,
					"error", err)
				continue
			}

			o.nextTarget = o.schedule.NextTarget(fullyFinalized)
			logger.Debugw("Next target", "epoch", o.nextTarget)
		}
	}
}

func (o *Oracle) commit(ctx context.Context, checkpoint *beacon.FinalizedCheckpoint, target uint64) error {
	log := logger.With("target", target)
	start := time.Now()

	log.Infow("Committing", "refBlock", checkpoint.BlockNum)

	if err := o.syncer.SyncToBlock(ctx, checkpoint.BlockNum); err != nil {
		return fmt.Errorf("sync to block %d: %w", checkpoint.BlockNum, err)
	}

	if err := o.beaconClient.VerifyFinalizedBlockRoot(ctx, checkpoint.BlockRoot); err != nil {
		return fmt.Errorf("verify finalized block: %w", err)
	}

	clusterBalances, validatorCount, err := o.fetchClusterBalances(ctx)
	if err != nil {
		return fmt.Errorf("fetch balances: %w", err)
	}

	merkleRoot := buildTree(clusterBalances).Root
	log.Debugw("Merkle tree built",
		"root", fmt.Sprintf("0x%x", merkleRoot),
		"clusters", len(clusterBalances))

	if err := o.storage.InsertPendingCommit(ctx, target, merkleRoot[:], checkpoint.BlockNum, clusterBalances); err != nil {
		return fmt.Errorf("store pending commit: %w", err)
	}

	// Check on-chain IMMEDIATELY before sending to minimize race window.
	// Race: Multiple oracles may compute the same root for this block and race to commit.
	// If another oracle commits first, our tx would revert with "root already committed".
	// By checking here, we avoid wasting gas on a tx that will fail.
	committedRoot, err := o.contractClient.GetCommittedRoot(ctx, checkpoint.BlockNum)
	if err != nil {
		log.Warnw("Failed to check committed root, proceeding with commit", "error", err)
	} else if committedRoot != [32]byte{} {
		if committedRoot == merkleRoot {
			log.Infow("Block already confirmed with matching root, skipping commit",
				"blockNum", checkpoint.BlockNum,
				"root", fmt.Sprintf("0x%x", merkleRoot))
			if err := o.storage.UpdateCommitStatus(ctx, target, storage.CommitStatusConfirmed, nil); err != nil {
				log.Warnw("Failed to update commit status", "error", err)
			}
		} else {
			log.Warnw("Block already confirmed with different root, skipping commit",
				"blockNum", checkpoint.BlockNum,
				"ourRoot", fmt.Sprintf("0x%x", merkleRoot),
				"committedRoot", fmt.Sprintf("0x%x", committedRoot))
			if err := o.storage.UpdateCommitStatus(ctx, target, storage.CommitStatusFailed, nil); err != nil {
				log.Warnw("Failed to update commit status", "error", err)
			}
		}
		return nil
	}

	receipt, err := o.contractClient.CommitRoot(ctx, merkleRoot, checkpoint.BlockNum)
	if err != nil {
		return o.handleCommitError(ctx, log, target, merkleRoot, receipt, err)
	}

	if err := o.storage.UpdateCommitStatus(ctx, target, storage.CommitStatusConfirmed, receipt.TxHash.Bytes()); err != nil {
		log.Warnw("Failed to update commit status", "error", err)
	}

	log.Infow("Committed",
		"txHash", receipt.TxHash.Hex(),
		"root", fmt.Sprintf("0x%x", merkleRoot),
		"refBlock", checkpoint.BlockNum,
		"validators", validatorCount,
		"clusters", len(clusterBalances),
		"took", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func buildTree(balances []storage.ClusterBalance) *merkle.Tree {
	clusterMap := make(map[[32]byte]uint32)
	for _, bal := range balances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}
	return merkle.NewTree(clusterMap)
}

func (o *Oracle) fetchClusterBalances(ctx context.Context) ([]storage.ClusterBalance, int, error) {
	validators, err := o.storage.GetActiveValidators(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get active validators: %w", err)
	}

	if len(validators) == 0 {
		logger.Debug("No active validators")
		return nil, 0, nil
	}

	pubkeys := o.deduplicatePubkeys(validators)

	start := time.Now()
	balanceMap, err := o.beaconClient.GetValidatorBalances(ctx, pubkeys)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch validator balances: %w", err)
	}

	result, notOnBeacon := o.aggregateByCluster(validators, balanceMap)

	logger.Debugw("Balances fetched",
		"validators", len(validators),
		"fromBeacon", len(balanceMap),
		"notOnBeacon", notOnBeacon,
		"clusters", len(result),
		"took", time.Since(start).Round(time.Millisecond).String())

	return result, len(validators), nil
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
			// Validator not in beacon state (pending activation, exited, or withdrawn).
			// Balance is 0, will be floored to 32 ETH below.
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
	for clusterID, totalGwei := range clusterTotals {
		result = append(result, storage.ClusterBalance{
			ClusterID:        clusterID[:],
			EffectiveBalance: uint32(totalGwei / gweiPerETH),
		})
	}

	return result, notOnBeacon
}

func (o *Oracle) handleCommitError(
	ctx context.Context,
	log logger.Logger,
	target uint64,
	merkleRoot [32]byte,
	receipt *types.Receipt,
	err error,
) error {
	var txHash []byte
	if receipt != nil {
		txHash = receipt.TxHash.Bytes()
	}
	if statusErr := o.storage.UpdateCommitStatus(ctx, target, storage.CommitStatusFailed, txHash); statusErr != nil {
		log.Warnw("Failed to update commit status", "error", statusErr)
	}

	if revertErr, ok := txmanager.IsRevertError(err); ok {
		log.Warnw("Commit reverted",
			"root", fmt.Sprintf("0x%x", merkleRoot),
			"simulated", revertErr.Simulated,
			"reason", revertErr.Reason)
		return nil
	}

	return fmt.Errorf("commit failed: %w", err)
}
