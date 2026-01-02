package commands

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"ssv-oracle/api"
	"ssv-oracle/contract"
	"ssv-oracle/eth/beacon"
	"ssv-oracle/eth/execution"
	"ssv-oracle/eth/syncer"
	"ssv-oracle/logger"
	"ssv-oracle/oracle"
	"ssv-oracle/storage"
	"ssv-oracle/updater"
	"ssv-oracle/wallet"
)

var (
	configPath  string
	freshStart  bool
	withUpdater bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the oracle service",
	Long: `Start the SSV oracle service and begin monitoring cluster effective balances.

Use --updater flag to also run the cluster updater, which listens for
committed roots and updates cluster balances on-chain using merkle proofs.`,
	RunE: run,
}

func init() {
	runCmd.Flags().StringVarP(&configPath, "config", "c", "config.yaml", "Path to configuration file")
	runCmd.Flags().BoolVar(&freshStart, "fresh", false, "Start fresh: clear all database state")
	runCmd.Flags().BoolVar(&withUpdater, "updater", false, "Enable the cluster updater")
}

func run(_ *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(configPath, withUpdater)
	if err != nil {
		return err
	}

	logger.Init(cfg.LogLevel)

	signer, err := wallet.NewSigner(&cfg.Wallet)
	if err != nil {
		return fmt.Errorf("create signer: %w", err)
	}
	defer func() { _ = signer.Close() }()

	startupFields := []any{
		"version", Version,
		"updater", withUpdater,
		"DBPath", cfg.DBPath,
		"apiAddress", cfg.APIAddress,
		"ethRPC", cfg.EthRPC,
		"beaconRPC", cfg.BeaconRPC,
		"contract", cfg.SSVContract,
		"signerAddress", signer.Address().Hex(),
	}
	if withUpdater {
		startupFields = append(startupFields,
			"ethWSRPC", cfg.EthWSRPC,
			"viewsContract", cfg.SSVViewsContract,
		)
	}
	logger.Infow("SSV Oracle starting", startupFields...)

	store, err := storage.New(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("create storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	if freshStart {
		logger.Info("Fresh start: clearing database")
		if err := store.ClearAllState(ctx); err != nil {
			return fmt.Errorf("clear database: %w", err)
		}
	}

	execClient, err := execution.New(ctx, execution.ClientConfig{
		URL:          cfg.EthRPC,
		MaxBatchSize: cfg.MaxSyncBatchSize,
	})
	if err != nil {
		return fmt.Errorf("create execution client: %w", err)
	}
	defer execClient.Close()

	chainID, err := validateChainID(ctx, store, execClient)
	if err != nil {
		return err
	}

	beaconClient, err := beacon.New(ctx, beacon.ClientConfig{
		URL: cfg.BeaconRPC,
	})
	if err != nil {
		return fmt.Errorf("create beacon client: %w", err)
	}

	eventSyncer := syncer.New(syncer.Config{
		ExecutionClient: execClient,
		Storage:         store,
		SSVContract:     common.HexToAddress(cfg.SSVContract),
	})

	contractCfg := &contract.Config{
		RPCURL:          cfg.EthRPC,
		ContractAddress: cfg.SSVContract,
		ChainID:         chainID,
		Signer:          signer,
		TxPolicy:        &cfg.TxPolicy,
	}

	if withUpdater {
		contractCfg.WSRPCURL = cfg.EthWSRPC
		contractCfg.ViewsContractAddress = cfg.SSVViewsContract
	}

	ethClient, err := contract.NewClient(contractCfg)
	if err != nil {
		return fmt.Errorf("create contract client: %w", err)
	}
	defer ethClient.Close()

	err = runServices(ctx, cfg, store, ethClient, eventSyncer, beaconClient)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Errorw("Shutdown complete", "error", err)
		return err
	}

	logger.Info("Shutdown complete")
	return nil
}

func validateChainID(ctx context.Context, store *storage.Storage, execClient *execution.Client) (*big.Int, error) {
	chainID, err := execClient.GetChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("get chain ID: %w", err)
	}
	logger.Infow("Connected to chain", "chainID", chainID)

	dbChainID, err := store.GetChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("get chain ID from database: %w", err)
	}

	if dbChainID == nil {
		if err := store.SetChainID(ctx, chainID.Uint64()); err != nil {
			return nil, fmt.Errorf("store chain ID: %w", err)
		}
		logger.Infow("Stored chain ID", "chainID", chainID)
		return chainID, nil
	}

	if *dbChainID != chainID.Uint64() {
		return nil, fmt.Errorf("chain ID mismatch: database=%d, RPC=%d (use --fresh)", *dbChainID, chainID)
	}

	return chainID, nil
}

func runServices(
	ctx context.Context,
	cfg *config,
	store *storage.Storage,
	ethClient *contract.Client,
	eventSyncer *syncer.EventSyncer,
	beaconClient *beacon.Client,
) error {
	logger.Info("Syncing SSV contract events")
	if err := eventSyncer.SyncToFinalized(ctx, cfg.SSVContractDeployBlock); err != nil {
		return fmt.Errorf("initial sync: %w", err)
	}

	oracleInstance := oracle.New(&oracle.Config{
		Storage:        store,
		ContractClient: ethClient,
		Syncer:         eventSyncer,
		BeaconClient:   beaconClient,
		Schedule:       cfg.Schedule,
	})

	g, gCtx := errgroup.WithContext(ctx)

	// Start API server
	apiServer := api.New(store, cfg.APIAddress)
	g.Go(func() error {
		return apiServer.Run(gCtx)
	})

	g.Go(func() error {
		return oracleInstance.Run(gCtx)
	})

	if withUpdater {
		updaterInstance := updater.New(&updater.Config{
			Storage:        store,
			ContractClient: ethClient,
			Syncer:         eventSyncer,
		})
		g.Go(func() error {
			return updaterInstance.Run(gCtx)
		})
	}

	return g.Wait()
}
