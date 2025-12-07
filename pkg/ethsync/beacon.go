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

type BeaconClient struct {
	client eth2client.Service
	spec   *Spec
}

type BeaconClientConfig struct {
	URL     string
	Timeout time.Duration
}

func NewBeaconClient(ctx context.Context, cfg BeaconClientConfig) (*BeaconClient, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	client, err := http.New(ctx,
		http.WithAddress(cfg.URL),
		http.WithTimeout(cfg.Timeout),
		http.WithLogLevel(zerolog.Disabled),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create beacon client: %w", err)
	}

	return &BeaconClient{client: client}, nil
}

// GetSpec returns beacon chain spec (cached after first call).
func (c *BeaconClient) GetSpec(ctx context.Context) (*Spec, error) {
	if c.spec != nil {
		return c.spec, nil
	}

	genesisProvider, ok := c.client.(eth2client.GenesisProvider)
	if !ok {
		return nil, fmt.Errorf("client does not support GenesisProvider")
	}
	genesisResp, err := genesisProvider.Genesis(ctx, &api.GenesisOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get genesis: %w", err)
	}

	specProvider, ok := c.client.(eth2client.SpecProvider)
	if !ok {
		return nil, fmt.Errorf("client does not support SpecProvider")
	}
	specResp, err := specProvider.Spec(ctx, &api.SpecOpts{})
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

type FinalizedCheckpoint struct {
	Epoch    uint64
	BlockNum uint64 // Execution layer block number
}

func (c *BeaconClient) GetFinalizedCheckpoint(ctx context.Context) (*FinalizedCheckpoint, error) {
	finalityProvider, ok := c.client.(eth2client.FinalityProvider)
	if !ok {
		return nil, fmt.Errorf("client does not support FinalityProvider")
	}

	finalityResp, err := finalityProvider.Finality(ctx, &api.FinalityOpts{
		State: "head",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get finality: %w", err)
	}

	epoch := uint64(finalityResp.Data.Finalized.Epoch)
	root := finalityResp.Data.Finalized.Root

	blockProvider, ok := c.client.(eth2client.SignedBeaconBlockProvider)
	if !ok {
		return nil, fmt.Errorf("client does not support SignedBeaconBlockProvider")
	}

	blockResp, err := blockProvider.SignedBeaconBlock(ctx, &api.SignedBeaconBlockOpts{
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

const (
	validatorBatchSize  = 1000 // Max validators per beacon API request
	maxParallelRequests = 5
)

// GetFinalizedValidatorBalances returns effective balances (in Gwei) for validators.
// Returns map of BLS pubkey -> effective balance.
func (c *BeaconClient) GetFinalizedValidatorBalances(ctx context.Context, pubkeys [][]byte) (map[phase0.BLSPubKey]uint64, error) {
	if len(pubkeys) == 0 {
		return make(map[phase0.BLSPubKey]uint64), nil
	}

	validatorsProvider, ok := c.client.(eth2client.ValidatorsProvider)
	if !ok {
		return nil, fmt.Errorf("client does not support ValidatorsProvider")
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
			resp, err := validatorsProvider.Validators(ctx, &api.ValidatorsOpts{
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
