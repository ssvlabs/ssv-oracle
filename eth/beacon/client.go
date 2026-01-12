package beacon

import (
	"context"
	"fmt"
	"sync"
	"time"

	eth2client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/ssvlabs/ssv-oracle/eth"
	"github.com/ssvlabs/ssv-oracle/logger"
)

const (
	defaultBeaconTimeout    = 30 * time.Second
	balanceFetchBatchSize   = 2000
	balanceFetchConcurrency = 10
)

type beaconAPI interface {
	eth2client.FinalityProvider
	eth2client.SignedBeaconBlockProvider
	eth2client.ValidatorsProvider
	eth2client.EventsProvider
	eth2client.BeaconBlockHeadersProvider
}

// Client wraps a beacon node client for fetching chain data.
type Client struct {
	beacon      beaconAPI
	retryConfig eth.RetryConfig
}

// ClientConfig holds configuration for the beacon client.
type ClientConfig struct {
	URL         string
	Timeout     time.Duration
	RetryConfig *eth.RetryConfig // nil uses DefaultRetryConfig()
}

// New creates a new beacon client.
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

	version := "unknown"
	if vp, ok := client.(eth2client.NodeVersionProvider); ok {
		if resp, err := vp.NodeVersion(ctx, &api.NodeVersionOpts{}); err == nil {
			version = resp.Data
		}
	}
	logger.Infow("Beacon client connected", "version", version, "url", cfg.URL)

	retryConfig := eth.DefaultRetryConfig()
	if cfg.RetryConfig != nil {
		retryConfig = *cfg.RetryConfig
	}

	return &Client{
		beacon:      client.(beaconAPI),
		retryConfig: retryConfig,
	}, nil
}

// FinalizedCheckpoint represents a finalized beacon chain checkpoint.
type FinalizedCheckpoint struct {
	Epoch     uint64
	BlockNum  uint64
	BlockRoot phase0.Root
}

// GetFinalizedEpoch returns the latest finalized checkpoint epoch.
func (c *Client) GetFinalizedEpoch(ctx context.Context) (uint64, error) {
	var epoch uint64
	err := eth.WithRetry(ctx, c.retryConfig, func() error {
		resp, err := c.beacon.Finality(ctx, &api.FinalityOpts{
			State: "head",
		})
		if err != nil {
			return fmt.Errorf("get finality: %w", err)
		}
		epoch = uint64(resp.Data.Finalized.Epoch)
		return nil
	})
	return epoch, err
}

func (c *Client) fetchCheckpoint(ctx context.Context, event *apiv1.FinalizedCheckpointEvent) (*FinalizedCheckpoint, error) {
	blockResp, err := c.beacon.SignedBeaconBlock(ctx, &api.SignedBeaconBlockOpts{
		Block: event.Block.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("get beacon block: %w", err)
	}

	blockNum, err := blockResp.Data.ExecutionBlockNumber()
	if err != nil {
		return nil, fmt.Errorf("get execution block number: %w", err)
	}

	return &FinalizedCheckpoint{
		Epoch:     uint64(event.Epoch),
		BlockNum:  blockNum,
		BlockRoot: event.Block,
	}, nil
}

// VerifyFinalizedBlockRoot checks that the beacon node's current finalized block
// matches the expected block root.
func (c *Client) VerifyFinalizedBlockRoot(ctx context.Context, expectedBlockRoot phase0.Root) error {
	return eth.WithRetry(ctx, c.retryConfig, func() error {
		headerResp, err := c.beacon.BeaconBlockHeader(ctx, &api.BeaconBlockHeaderOpts{
			Block: "finalized",
		})
		if err != nil {
			return fmt.Errorf("get finalized block header: %w", err)
		}

		if headerResp.Data.Root != expectedBlockRoot {
			return fmt.Errorf("finalized block root mismatch: expected %#x, got %#x",
				expectedBlockRoot, headerResp.Data.Root)
		}
		return nil
	})
}

// GetValidatorBalances returns effective balances in Gwei for the given validators
// at the finalized state.
func (c *Client) GetValidatorBalances(ctx context.Context, pubkeys [][]byte) (map[phase0.BLSPubKey]uint64, error) {
	if len(pubkeys) == 0 {
		return make(map[phase0.BLSPubKey]uint64), nil
	}

	blsPubkeys := make([]phase0.BLSPubKey, len(pubkeys))
	for i, pk := range pubkeys {
		copy(blsPubkeys[i][:], pk)
	}

	var batches [][]phase0.BLSPubKey
	for i := 0; i < len(blsPubkeys); i += balanceFetchBatchSize {
		end := i + balanceFetchBatchSize
		if end > len(blsPubkeys) {
			end = len(blsPubkeys)
		}
		batches = append(batches, blsPubkeys[i:end])
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(balanceFetchConcurrency)

	var mu sync.Mutex
	merged := make(map[phase0.BLSPubKey]uint64, len(pubkeys))

	for _, batch := range batches {
		g.Go(func() error {
			return eth.WithRetry(ctx, c.retryConfig, func() error {
				resp, err := c.beacon.Validators(ctx, &api.ValidatorsOpts{
					State:   "finalized",
					PubKeys: batch,
				})
				if err != nil {
					return fmt.Errorf("get validators: %w", err)
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

	err := c.beacon.Events(ctx, &api.EventsOpts{
		Topics: []string{"finalized_checkpoint"},
		FinalizedCheckpointHandler: func(_ context.Context, event *apiv1.FinalizedCheckpointEvent) {
			var checkpoint *FinalizedCheckpoint
			err := eth.WithRetry(ctx, c.retryConfig, func() error {
				var fetchErr error
				checkpoint, fetchErr = c.fetchCheckpoint(ctx, event)
				return fetchErr
			})
			if err != nil {
				logger.Errorw("Failed to fetch checkpoint",
					"epoch", event.Epoch,
					"block", event.Block.String(),
					"error", err)
				return
			}

			select {
			case ch <- checkpoint:
				logger.Debugw("Finalized checkpoint event",
					"epoch", checkpoint.Epoch,
					"fullyFinalized", checkpoint.Epoch-1,
					"blockNum", checkpoint.BlockNum)
			default:
				logger.Warnw("Dropped finalized checkpoint (consumer slow)",
					"epoch", checkpoint.Epoch)
			}
		},
	})
	if err != nil {
		close(ch)
		return nil, fmt.Errorf("subscribe to finalized checkpoints: %w", err)
	}

	go func() {
		<-ctx.Done()
		close(ch)
	}()

	return ch, nil
}
