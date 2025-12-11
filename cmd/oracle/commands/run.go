package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	"ssv-oracle/contract"
	"ssv-oracle/oracle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/pkg/logger"
	"ssv-oracle/txmanager"
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
	RunE: runOracle,
}

func init() {
	runCmd.Flags().StringVarP(&configPath, "config", "c", "config.yaml", "Path to configuration file")
	runCmd.Flags().BoolVar(&freshStart, "fresh", false, "Start fresh: clear all database state")
	runCmd.Flags().BoolVar(&withUpdater, "updater", false, "Also run the cluster updater")
}

// Config represents the oracle configuration file.
type Config struct {
	EthRPC    string `yaml:"eth_rpc"`
	EthWSRPC  string `yaml:"eth_ws_rpc"`
	BeaconRPC string `yaml:"beacon_rpc"`

	SSVContract string `yaml:"ssv_contract"`

	SyncFromBlock  uint64 `yaml:"sync_from_block"`
	SyncBatchSize  uint64 `yaml:"sync_batch_size"`
	SyncMaxRetries int    `yaml:"sync_max_retries"`

	DBPath string `yaml:"db_path"`

	Wallet       wallet.Config        `yaml:"wallet"`
	TxPolicy     txmanager.TxPolicy   `yaml:"tx_policy"`
	CommitPhases []oracle.CommitPhase `yaml:"commit_phases"`
}

func runOracle(_ *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	if err := oracle.ValidatePhases(cfg.CommitPhases); err != nil {
		return fmt.Errorf("invalid commit phases: %w", err)
	}

	if cfg.DBPath == "" {
		cfg.DBPath = "./data/oracle.db"
	}

	signer, err := wallet.NewSigner(&cfg.Wallet)
	if err != nil {
		return fmt.Errorf("failed to create signer: %w", err)
	}
	defer func() { _ = signer.Close() }()

	startupFields := []any{
		"version", Version,
		"contract", cfg.SSVContract,
		"ethRPC", cfg.EthRPC,
		"beaconRPC", cfg.BeaconRPC,
	}
	if cfg.EthWSRPC != "" {
		startupFields = append(startupFields, "ethWSRPC", cfg.EthWSRPC)
	}
	startupFields = append(startupFields,
		"dbPath", cfg.DBPath,
		"signerAddress", signer.Address().Hex(),
		"updater", withUpdater,
	)
	logger.Infow("SSV Oracle starting", startupFields...)

	storage, execClient, beaconClient, err := initClients(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = storage.Close() }()
	defer execClient.Close()

	if freshStart {
		logger.Info("Fresh start: clearing database")
		if err := storage.ClearAllState(ctx); err != nil {
			return fmt.Errorf("failed to clear database: %w", err)
		}
	}

	if err := validateChainID(ctx, storage, execClient); err != nil {
		return err
	}

	syncer := ethsync.NewEventSyncer(ethsync.EventSyncerConfig{
		ExecutionClient: execClient,
		Storage:         storage,
		SSVContract:     common.HexToAddress(cfg.SSVContract),
	})

	currentPhase := oracle.GetPhaseForEpoch(cfg.CommitPhases, 0)
	logger.Infow("Commit phases configured",
		"phases", len(cfg.CommitPhases),
		"startEpoch", currentPhase.StartEpoch,
		"interval", currentPhase.Interval)

	ethClient, err := contract.NewClient(ctx, cfg.EthRPC, cfg.EthWSRPC, cfg.SSVContract, signer, &cfg.TxPolicy)
	if err != nil {
		return fmt.Errorf("failed to create contract client: %w", err)
	}
	defer ethClient.Close()

	return runServices(ctx, cfg, storage, ethClient, syncer, beaconClient)
}

func initClients(ctx context.Context, cfg *Config) (*ethsync.Storage, *ethsync.ExecutionClient, *ethsync.BeaconClient, error) {
	storage, err := ethsync.NewStorage(cfg.DBPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create storage: %w", err)
	}

	execClient, err := ethsync.NewExecutionClient(ethsync.ExecutionClientConfig{
		URL:        cfg.EthRPC,
		BatchSize:  cfg.SyncBatchSize,
		MaxRetries: cfg.SyncMaxRetries,
	})
	if err != nil {
		_ = storage.Close()
		return nil, nil, nil, fmt.Errorf("failed to create execution client: %w", err)
	}

	beaconClient, err := ethsync.NewBeaconClient(ctx, ethsync.BeaconClientConfig{
		URL: cfg.BeaconRPC,
	})
	if err != nil {
		execClient.Close()
		_ = storage.Close()
		return nil, nil, nil, fmt.Errorf("failed to create beacon client: %w", err)
	}

	return storage, execClient, beaconClient, nil
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
		Phases:         cfg.CommitPhases,
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

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config %s: %w", path, err)
	}

	return &cfg, nil
}
