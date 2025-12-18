package beacon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	eth2client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"ssv-oracle/eth"
	"ssv-oracle/logger"
)

const (
	defaultBeaconTimeout = 30 * time.Second
	validatorBatchSize   = 1000 // Max validators per beacon API request
	maxParallelRequests  = 5
)

// ErrBeaconSyncing indicates the beacon node is still syncing.
var ErrBeaconSyncing = errors.New("beacon node is syncing")

// wrapBeaconError provides context-specific error messages for beacon API failures.
// Returns permanent errors for 404 (not found) to prevent retrying.
func wrapBeaconError(err error, operation string) error {
	if err == nil {
		return nil
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 404:
			return eth.Permanent(fmt.Errorf("%s: not found", operation))
		case 503:
			return fmt.Errorf("%s: %w", operation, ErrBeaconSyncing)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// API defines the beacon node capabilities required by the oracle.
type API interface {
	eth2client.GenesisProvider
	eth2client.SpecProvider
	eth2client.FinalityProvider
	eth2client.SignedBeaconBlockProvider
	eth2client.ValidatorsProvider
	eth2client.EventsProvider
}

// Client wraps a beacon node client for fetching chain data.
type Client struct {
	client      API
	Spec        *Spec
	retryConfig eth.RetryConfig
}

// ClientConfig holds configuration for the beacon client.
type ClientConfig struct {
	URL         string
	Timeout     time.Duration
	RetryConfig *eth.RetryConfig // nil uses DefaultRetryConfig()
}

// New creates a new beacon client and fetches the chain spec.
func New(ctx context.Context, cfg ClientConfig) (*Client, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultBeaconTimeout
	}

	client, err := http.New(ctx,
		http.WithAddress(cfg.URL),
		http.WithTimeout(cfg.Timeout),
		http.WithLogLevel(zerolog.Disabled),
	)
	if err != nil {
		return nil, fmt.Errorf("beacon node %s: %w", cfg.URL, err)
	}

	retryConfig := eth.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		retryConfig = *cfg.RetryConfig
	}

	bc := &Client{
		client:      client.(API),
		retryConfig: retryConfig,
	}

	if err := bc.fetchSpec(ctx); err != nil {
		return nil, err
	}

	return bc, nil
}

func (c *Client) fetchSpec(ctx context.Context) error {
	return eth.WithRetry(ctx, c.retryConfig, func() error {
		genesisResp, err := c.client.Genesis(ctx, &api.GenesisOpts{})
		if err != nil {
			return wrapBeaconError(err, "get genesis")
		}

		specResp, err := c.client.Spec(ctx, &api.SpecOpts{})
		if err != nil {
			return wrapBeaconError(err, "get spec")
		}

		slotsPerEpoch, ok := specResp.Data["SLOTS_PER_EPOCH"].(uint64)
		if !ok {
			return eth.Permanent(fmt.Errorf("SLOTS_PER_EPOCH not found or invalid type in spec"))
		}

		secondsPerSlot, ok := specResp.Data["SECONDS_PER_SLOT"].(time.Duration)
		if !ok {
			return eth.Permanent(fmt.Errorf("SECONDS_PER_SLOT not found or invalid type in spec"))
		}

		c.Spec = &Spec{
			GenesisTime:   genesisResp.Data.GenesisTime,
			SlotsPerEpoch: slotsPerEpoch,
			SlotDuration:  secondsPerSlot,
		}

		logger.Infow("Beacon spec loaded",
			"genesis", c.Spec.GenesisTime.Format(time.RFC3339),
			"slotsPerEpoch", c.Spec.SlotsPerEpoch,
			"slotDuration", c.Spec.SlotDuration)

		return nil
	})
}

// CurrentEpoch returns the current epoch based on wall clock time.
func (c *Client) CurrentEpoch() uint64 {
	return c.Spec.CurrentEpoch()
}

// FinalizedCheckpoint contains the finalized epoch and corresponding execution block.
type FinalizedCheckpoint struct {
	Epoch     uint64
	BlockNum  uint64
	StateRoot phase0.Root
}

// GetFinalizedCheckpoint returns the latest finalized checkpoint.
func (c *Client) GetFinalizedCheckpoint(ctx context.Context) (*FinalizedCheckpoint, error) {
	var checkpoint *FinalizedCheckpoint
	err := eth.WithRetry(ctx, c.retryConfig, func() error {
		finalityResp, err := c.client.Finality(ctx, &api.FinalityOpts{
			State: "head",
		})
		if err != nil {
			return wrapBeaconError(err, "get finality")
		}

		epoch := uint64(finalityResp.Data.Finalized.Epoch)
		root := finalityResp.Data.Finalized.Root

		blockResp, err := c.client.SignedBeaconBlock(ctx, &api.SignedBeaconBlockOpts{
			Block: root.String(),
		})
		if err != nil {
			return wrapBeaconError(err, "get beacon block")
		}

		blockNum, err := blockResp.Data.ExecutionBlockNumber()
		if err != nil {
			return fmt.Errorf("get execution block number: %w", err)
		}

		stateRoot, err := blockResp.Data.StateRoot()
		if err != nil {
			return fmt.Errorf("get state root: %w", err)
		}

		checkpoint = &FinalizedCheckpoint{
			Epoch:     epoch,
			BlockNum:  blockNum,
			StateRoot: stateRoot,
		}
		return nil
	})
	return checkpoint, err
}

// GetValidatorBalances returns effective balances in Gwei for the given validators.
func (c *Client) GetValidatorBalances(ctx context.Context, stateRoot string, pubkeys [][]byte) (map[phase0.BLSPubKey]uint64, error) {
	if len(pubkeys) == 0 {
		return make(map[phase0.BLSPubKey]uint64), nil
	}

	blsPubkeys := make([]phase0.BLSPubKey, len(pubkeys))
	for i, pk := range pubkeys {
		copy(blsPubkeys[i][:], pk)
	}

	var batches [][]phase0.BLSPubKey
	for i := 0; i < len(blsPubkeys); i += validatorBatchSize {
		end := i + validatorBatchSize
		if end > len(blsPubkeys) {
			end = len(blsPubkeys)
		}
		batches = append(batches, blsPubkeys[i:end])
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxParallelRequests)

	var mu sync.Mutex
	merged := make(map[phase0.BLSPubKey]uint64, len(pubkeys))

	for _, batch := range batches {
		g.Go(func() error {
			return eth.WithRetry(ctx, c.retryConfig, func() error {
				resp, err := c.client.Validators(ctx, &api.ValidatorsOpts{
					State:   stateRoot,
					PubKeys: batch,
				})
				if err != nil {
					return wrapBeaconError(err, "get validators")
				}

				mu.Lock()
				for _, v := range resp.Data {
					merged[v.Validator.PublicKey] = uint64(v.Validator.EffectiveBalance)
				}
				mu.Unlock()
				return nil
			})
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return merged, nil
}

// SubscribeFinalizedCheckpoints starts an SSE subscription for finalized checkpoints.
// Returns a channel that receives checkpoints. Close the context to stop the subscription.
// Note: go-eth2-client handles SSE reconnection internally with exponential backoff.
func (c *Client) SubscribeFinalizedCheckpoints(ctx context.Context) (<-chan *FinalizedCheckpoint, error) {
	ch := make(chan *FinalizedCheckpoint, 1)

	err := c.client.Events(ctx, &api.EventsOpts{
		Topics: []string{"finalized_checkpoint"},
		FinalizedCheckpointHandler: func(_ context.Context, event *apiv1.FinalizedCheckpointEvent) {
			checkpoint, err := c.handleFinalizedEvent(ctx, event)
			if err != nil {
				logger.Errorw("Failed to process finalized checkpoint event",
					"epoch", event.Epoch,
					"error", err)
				return
			}

			select {
			case ch <- checkpoint:
				logger.Debugw("Finalized checkpoint event",
					"checkpointEpoch", checkpoint.Epoch,
					"fullyFinalized", checkpoint.Epoch-1,
					"referenceBlock", checkpoint.BlockNum)
			default:
				// Channel full, skip (consumer will get next one)
			}
		},
	})
	if err != nil {
		close(ch)
		return nil, fmt.Errorf("subscribe to finalized checkpoints: %w", err)
	}

	// Close channel when context is done
	go func() {
		<-ctx.Done()
		close(ch)
	}()

	return ch, nil
}

func (c *Client) handleFinalizedEvent(ctx context.Context, event *apiv1.FinalizedCheckpointEvent) (*FinalizedCheckpoint, error) {
	var blockResp *api.Response[*spec.VersionedSignedBeaconBlock]
	err := eth.WithRetry(ctx, c.retryConfig, func() error {
		var err error
		blockResp, err = c.client.SignedBeaconBlock(ctx, &api.SignedBeaconBlockOpts{
			Block: event.Block.String(),
		})
		return wrapBeaconError(err, "get finalized block")
	})
	if err != nil {
		return nil, err
	}

	blockNum, err := blockResp.Data.ExecutionBlockNumber()
	if err != nil {
		return nil, fmt.Errorf("get execution block number: %w", err)
	}

	stateRoot, err := blockResp.Data.StateRoot()
	if err != nil {
		return nil, fmt.Errorf("get state root: %w", err)
	}

	return &FinalizedCheckpoint{
		Epoch:     uint64(event.Epoch),
		BlockNum:  blockNum,
		StateRoot: stateRoot,
	}, nil
}
