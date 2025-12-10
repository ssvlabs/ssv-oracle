package commands

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	DBHost        string `yaml:"db_host"`
	DBPort        int    `yaml:"db_port"`
	DBName        string `yaml:"db_name"`
	DBUser        string `yaml:"db_user"`
	DBPasswordEnv string `yaml:"db_password_env"`

	Wallet       wallet.Config        `yaml:"wallet"`
	TxPolicy     txmanager.TxPolicy   `yaml:"tx_policy"`
	CommitPhases []oracle.CommitPhase `yaml:"commit_phases"`
}

func runOracle(_ *cobra.Command, _ []string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	if err := oracle.ValidatePhases(cfg.CommitPhases); err != nil {
		return fmt.Errorf("invalid commit phases: %w", err)
	}

	dbPassword := os.Getenv(cfg.DBPasswordEnv)
	if dbPassword == "" {
		return fmt.Errorf("database password not found in %s", cfg.DBPasswordEnv)
	}

	signer, err := wallet.NewSigner(&cfg.Wallet)
	if err != nil {
		return fmt.Errorf("failed to create signer: %w", err)
	}
	defer func() { _ = signer.Close() }()

	logger.Infow("SSV Oracle starting",
		"version", Version,
		"contract", cfg.SSVContract,
		"signerAddress", signer.Address().Hex(),
		"updater", withUpdater)

	storage, execClient, beaconClient, err := initClients(cfg, dbPassword)
	if err != nil {
		return err
	}
	defer func() { _ = storage.Close() }()
	defer execClient.Close()

	if freshStart {
		logger.Info("Fresh start: clearing database")
		if err := storage.ClearAllState(context.Background()); err != nil {
			return fmt.Errorf("failed to clear database: %w", err)
		}
	}

	if err := validateChainID(storage, execClient); err != nil {
		return err
	}

	spec, err := beaconClient.GetSpec(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get beacon spec: %w", err)
	}
	logger.Infow("Beacon spec loaded",
		"genesis", spec.GenesisTime.Format(time.RFC3339),
		"slotsPerEpoch", spec.SlotsPerEpoch,
		"slotDuration", spec.SlotDuration)

	syncer, err := ethsync.NewEventSyncer(ethsync.EventSyncerConfig{
		ExecutionClient: execClient,
		Storage:         storage,
		SSVContract:     common.HexToAddress(cfg.SSVContract),
		Spec:            spec,
	})
	if err != nil {
		return fmt.Errorf("failed to create event syncer: %w", err)
	}

	currentPhase := oracle.GetPhaseForEpoch(cfg.CommitPhases, 0)
	logger.Infow("Commit phases configured",
		"phases", len(cfg.CommitPhases),
		"startEpoch", currentPhase.StartEpoch,
		"interval", currentPhase.Interval)

	ethClient, err := contract.NewClient(cfg.EthRPC, cfg.EthWSRPC, cfg.SSVContract, signer, &cfg.TxPolicy)
	if err != nil {
		return fmt.Errorf("failed to create contract client: %w", err)
	}
	defer ethClient.Close()

	return runServices(cfg, storage, ethClient, syncer, beaconClient)
}

func initClients(cfg *Config, dbPassword string) (*ethsync.PostgresStorage, *ethsync.ExecutionClient, *ethsync.BeaconClient, error) {
	connString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, url.QueryEscape(dbPassword), cfg.DBName)

	storage, err := ethsync.NewPostgresStorage(connString)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create storage: %w", err)
	}

	execClient, err := ethsync.NewExecutionClient(ethsync.ExecutionClientConfig{
		URL:        cfg.EthRPC,
		BatchSize:  cfg.SyncBatchSize,
		MaxRetries: cfg.SyncMaxRetries,
		RetryDelay: 5 * time.Second,
	})
	if err != nil {
		_ = storage.Close()
		return nil, nil, nil, fmt.Errorf("failed to create execution client: %w", err)
	}

	beaconClient, err := ethsync.NewBeaconClient(context.Background(), ethsync.BeaconClientConfig{
		URL: cfg.BeaconRPC,
	})
	if err != nil {
		_ = storage.Close()
		execClient.Close()
		return nil, nil, nil, fmt.Errorf("failed to create beacon client: %w", err)
	}

	return storage, execClient, beaconClient, nil
}

func validateChainID(storage *ethsync.PostgresStorage, execClient *ethsync.ExecutionClient) error {
	ctx := context.Background()

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
	cfg *Config,
	storage *ethsync.PostgresStorage,
	ethClient *contract.Client,
	syncer *ethsync.EventSyncer,
	beaconClient *ethsync.BeaconClient,
) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Infow("Received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("Syncing SSV contract events")
	if err := syncer.SyncToFinalized(ctx, cfg.SyncFromBlock); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	oracleInstance := oracle.New(&oracle.Config{
		Storage:        storage,
		ContractClient: ethClient,
		Phases:         cfg.CommitPhases,
	})

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return oracleInstance.Run(gCtx, syncer, beaconClient)
	})

	if withUpdater {
		updaterInstance := updater.New(&updater.Config{
			Storage:        storage,
			ContractClient: ethClient,
		})
		g.Go(func() error {
			return updaterInstance.Run(gCtx)
		})
	}

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("error: %w", err)
	}

	logger.Info("Shutdown complete")
	return nil
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
