package ethsync

import (
	"context"
	"fmt"
	"sync"
	"time"

	eth2client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

const (
	defaultBeaconTimeout = 30 * time.Second
	validatorBatchSize   = 1000 // Max validators per beacon API request
	maxParallelRequests  = 5
)

// BeaconAPI defines the beacon node capabilities required by the oracle.
type BeaconAPI interface {
	eth2client.GenesisProvider
	eth2client.SpecProvider
	eth2client.FinalityProvider
	eth2client.SignedBeaconBlockProvider
	eth2client.ValidatorsProvider
}

// BeaconClient wraps a beacon node client for fetching chain data.
type BeaconClient struct {
	client BeaconAPI
	spec   *Spec
}

// BeaconClientConfig holds configuration for the beacon client.
type BeaconClientConfig struct {
	URL     string
	Timeout time.Duration
}

// NewBeaconClient creates a new beacon client.
func NewBeaconClient(ctx context.Context, cfg BeaconClientConfig) (*BeaconClient, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultBeaconTimeout
	}

	client, err := http.New(ctx,
		http.WithAddress(cfg.URL),
		http.WithTimeout(cfg.Timeout),
		http.WithLogLevel(zerolog.Disabled),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create beacon client: %w", err)
	}

	return &BeaconClient{
		client: client.(BeaconAPI),
	}, nil
}

// GetSpec returns the beacon chain spec (cached).
func (c *BeaconClient) GetSpec(ctx context.Context) (*Spec, error) {
	if c.spec != nil {
		return c.spec, nil
	}

	genesisResp, err := c.client.Genesis(ctx, &api.GenesisOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get genesis: %w", err)
	}

	specResp, err := c.client.Spec(ctx, &api.SpecOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get spec: %w", err)
	}

	slotsPerEpoch, ok := specResp.Data["SLOTS_PER_EPOCH"].(uint64)
	if !ok {
		return nil, fmt.Errorf("SLOTS_PER_EPOCH not found or invalid type in spec")
	}

	secondsPerSlot, ok := specResp.Data["SECONDS_PER_SLOT"].(time.Duration)
	if !ok {
		return nil, fmt.Errorf("SECONDS_PER_SLOT not found or invalid type in spec")
	}

	c.spec = &Spec{
		GenesisTime:   genesisResp.Data.GenesisTime,
		SlotsPerEpoch: slotsPerEpoch,
		SlotDuration:  secondsPerSlot,
	}

	return c.spec, nil
}

// FinalizedCheckpoint contains the finalized epoch and corresponding execution block.
type FinalizedCheckpoint struct {
	Epoch    uint64
	BlockNum uint64 // execution layer
}

// GetFinalizedCheckpoint returns the latest finalized checkpoint.
func (c *BeaconClient) GetFinalizedCheckpoint(ctx context.Context) (*FinalizedCheckpoint, error) {
	finalityResp, err := c.client.Finality(ctx, &api.FinalityOpts{
		State: "head",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get finality: %w", err)
	}

	epoch := uint64(finalityResp.Data.Finalized.Epoch)
	root := finalityResp.Data.Finalized.Root

	blockResp, err := c.client.SignedBeaconBlock(ctx, &api.SignedBeaconBlockOpts{
		Block: root.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get beacon block: %w", err)
	}

	blockNum, err := blockResp.Data.ExecutionBlockNumber()
	if err != nil {
		return nil, fmt.Errorf("failed to get execution block number: %w", err)
	}

	return &FinalizedCheckpoint{
		Epoch:    epoch,
		BlockNum: blockNum,
	}, nil
}

// GetFinalizedValidatorBalances returns effective balances in Gwei for the given validators.
func (c *BeaconClient) GetFinalizedValidatorBalances(ctx context.Context, pubkeys [][]byte) (map[phase0.BLSPubKey]uint64, error) {
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
			resp, err := c.client.Validators(ctx, &api.ValidatorsOpts{
				State:   "finalized",
				PubKeys: batch,
			})
			if err != nil {
				return fmt.Errorf("failed to get validators: %w", err)
			}

			mu.Lock()
			for _, v := range resp.Data {
				merged[v.Validator.PublicKey] = uint64(v.Validator.EffectiveBalance)
			}
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return merged, nil
}
