package commands

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"ssv-oracle/contract"
	"ssv-oracle/oracle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
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

	signer, err := wallet.NewSigner(&cfg.Wallet)
	if err != nil {
		return fmt.Errorf("failed to create signer: %w", err)
	}
	defer func() { _ = signer.Close() }()

	startupFields := []any{
		"version", Version,
		"updater", withUpdater,
		"DBPath", cfg.DBPath,
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

	storage, err := ethsync.NewStorage(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}
	defer func() { _ = storage.Close() }()

	if freshStart {
		logger.Info("Fresh start: clearing database")
		if err := storage.ClearAllState(ctx); err != nil {
			return fmt.Errorf("failed to clear database: %w", err)
		}
	}

	execClient, err := ethsync.NewExecutionClient(ethsync.ExecutionClientConfig{
		URL:       cfg.EthRPC,
		BatchSize: cfg.SyncBatchSize,
	})
	if err != nil {
		return fmt.Errorf("failed to create execution client: %w", err)
	}
	defer execClient.Close()

	if err := validateChainID(ctx, storage, execClient); err != nil {
		return err
	}

	beaconClient, err := ethsync.NewBeaconClient(ctx, ethsync.BeaconClientConfig{
		URL: cfg.BeaconRPC,
	})
	if err != nil {
		return fmt.Errorf("failed to create beacon client: %w", err)
	}

	syncer := ethsync.NewEventSyncer(ethsync.EventSyncerConfig{
		ExecutionClient: execClient,
		Storage:         storage,
		SSVContract:     common.HexToAddress(cfg.SSVContract),
	})

	ethClient, err := contract.NewClient(ctx, &contract.Config{
		RPCURL:               cfg.EthRPC,
		WSRPCURL:             cfg.EthWSRPC,
		ContractAddress:      cfg.SSVContract,
		ViewsContractAddress: cfg.SSVViewsContract,
		Signer:               signer,
		TxPolicy:             &cfg.TxPolicy,
	})
	if err != nil {
		return fmt.Errorf("failed to create contract client: %w", err)
	}
	defer ethClient.Close()

	return runServices(ctx, cfg, storage, ethClient, syncer, beaconClient)
}

func validateChainID(ctx context.Context, storage *ethsync.Storage, execClient *ethsync.ExecutionClient) error {
	chainID, err := execClient.GetChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get chain ID: %w", err)
	}
	logger.Infow("Connected to chain", "chainID", chainID)

	dbChainID, err := storage.GetChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get chain ID from database: %w", err)
	}

	if dbChainID == nil {
		if err := storage.SetChainID(ctx, chainID.Uint64()); err != nil {
			return fmt.Errorf("failed to store chain ID: %w", err)
		}
		logger.Infow("Stored chain ID", "chainID", chainID)
		return nil
	}

	if *dbChainID != chainID.Uint64() {
		return fmt.Errorf("chain ID mismatch: database=%d, RPC=%d (use --fresh)", *dbChainID, chainID)
	}

	return nil
}

func runServices(
	ctx context.Context,
	cfg *Config,
	storage *ethsync.Storage,
	ethClient *contract.Client,
	syncer *ethsync.EventSyncer,
	beaconClient *ethsync.BeaconClient,
) error {
	logger.Info("Syncing SSV contract events")
	if err := syncer.SyncToFinalized(ctx, cfg.SyncFromBlock); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	oracleInstance := oracle.New(&oracle.Config{
		Storage:        storage,
		ContractClient: ethClient,
		Syncer:         syncer,
		BeaconClient:   beaconClient,
		Schedule:       cfg.Schedule,
	})

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return oracleInstance.Run(gCtx)
	})

	if withUpdater {
		updaterInstance := updater.New(&updater.Config{
			Storage:        storage,
			ContractClient: ethClient,
			Syncer:         syncer,
		})
		g.Go(func() error {
			return updaterInstance.Run(gCtx)
		})
	}

	err := g.Wait()
	if ctx.Err() != nil {
		logger.Info("Received shutdown signal")
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	logger.Info("Shutdown complete")
	return nil
}
