package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"ssv-oracle/contract"
	"ssv-oracle/oracle"
	"ssv-oracle/pkg/ethsync"
)

var (
	configPath string
	freshStart bool
)

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the oracle service",
	Long:  `Start the SSV oracle service and begin monitoring cluster effective balances.`,
	RunE:  runOracle,
}

func init() {
	runCmd.Flags().StringVarP(&configPath, "config", "c", "config.yaml", "Path to configuration file")
	runCmd.Flags().BoolVar(&freshStart, "fresh", false, "Start fresh: clear all database state and sync from SSV contract genesis")
}

// Config represents the oracle configuration file
type Config struct {
	// Network
	EthRPC    string `yaml:"eth_rpc"`
	BeaconRPC string `yaml:"beacon_rpc"`

	// Contracts
	SSVContract    string `yaml:"ssv_contract"`
	OracleContract string `yaml:"oracle_contract"`

	// Event Syncing
	SyncFromBlock  uint64 `yaml:"sync_from_block"`
	SyncBatchSize  uint64 `yaml:"sync_batch_size"`
	SyncMaxRetries int    `yaml:"sync_max_retries"`

	// Database
	DBHost        string `yaml:"db_host"`
	DBPort        int    `yaml:"db_port"`
	DBName        string `yaml:"db_name"`
	DBUser        string `yaml:"db_user"`
	DBPasswordEnv string `yaml:"db_password_env"`

	// Oracle
	PrivateKeyEnv string `yaml:"private_key_env"`

	// Oracle Timing Configuration
	OracleTiming []oracle.TimingPhase `yaml:"oracle_timing"`
}

func runOracle(_ *cobra.Command, _ []string) error {
	// Load configuration
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	// Validate timing configuration
	if err := oracle.ValidateTimingPhases(cfg.OracleTiming); err != nil {
		return fmt.Errorf("invalid timing configuration: %w", err)
	}

	// Get private key from environment
	privateKey := os.Getenv(cfg.PrivateKeyEnv)
	if privateKey == "" {
		return fmt.Errorf("private key not found in environment variable %s", cfg.PrivateKeyEnv)
	}

	// Get database password from environment
	dbPassword := os.Getenv(cfg.DBPasswordEnv)
	if dbPassword == "" {
		return fmt.Errorf("database password not found in environment variable %s", cfg.DBPasswordEnv)
	}

	log.Printf("SSV Oracle %s", Version)
	log.Printf("SSV Contract: %s", cfg.SSVContract)
	log.Printf("Oracle Contract: %s", cfg.OracleContract)

	// Enable mock mode if oracle contract is zero address
	mockMode := cfg.OracleContract == "0x0000000000000000000000000000000000000000"
	if mockMode {
		log.Println("Oracle contract is zero address, running in mock mode")
	}

	// 1. Create PostgreSQL storage
	connString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, dbPassword, cfg.DBName)

	storage, err := ethsync.NewPostgresStorage(connString)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}
	defer func() {
		if err := storage.Close(); err != nil {
			log.Printf("Warning: failed to close storage: %v", err)
		}
	}()

	// Handle fresh start flag
	if freshStart {
		log.Println("Fresh start: clearing database...")
		ctx := context.Background()
		if err := storage.ClearAllState(ctx); err != nil {
			return fmt.Errorf("failed to clear database state: %w", err)
		}
	}

	// Create execution client
	execClient, err := ethsync.NewExecutionClient(ethsync.ExecutionClientConfig{
		URL:        cfg.EthRPC,
		BatchSize:  cfg.SyncBatchSize,
		MaxRetries: cfg.SyncMaxRetries,
		RetryDelay: 5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("failed to create execution client: %w", err)
	}
	defer execClient.Close()

	// Get and log chain ID
	chainID, err := execClient.GetChainID(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get chain ID: %w", err)
	}
	log.Printf("Chain ID: %d", chainID)

	// Validate chain ID matches database (prevents accidental network changes)
	ctx := context.Background()
	dbChainID, err := storage.GetChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get chain ID from database: %w", err)
	}

	if dbChainID == nil {
		// First run: store chain ID
		if err := storage.SetChainID(ctx, chainID.Uint64()); err != nil {
			return fmt.Errorf("failed to store chain ID: %w", err)
		}
		log.Printf("Stored chain ID: %d", chainID)
	} else if *dbChainID != chainID.Uint64() {
		return fmt.Errorf("chain ID mismatch: database has %d, RPC has %d. Use --fresh to start with new chain", *dbChainID, chainID)
	}

	// Create beacon client
	beaconClient := ethsync.NewBeaconClient(ethsync.BeaconClientConfig{
		URL:        cfg.BeaconRPC,
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		RetryDelay: 5 * time.Second,
	})

	// Fetch genesis time to create beacon spec for slot/epoch calculations
	spec, err := beaconClient.GetSpec(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get beacon spec: %w", err)
	}
	log.Printf("Beacon spec: genesis=%s, slotsPerEpoch=%d, slotDuration=%v",
		spec.GenesisTime.Format(time.RFC3339), spec.SlotsPerEpoch, spec.SlotDuration)

	// Create event syncer
	ssvContract := common.HexToAddress(cfg.SSVContract)
	syncer, err := ethsync.NewEventSyncer(ethsync.EventSyncerConfig{
		ExecutionClient: execClient,
		Storage:         storage,
		SSVContract:     ssvContract,
		Spec:            spec,
	})
	if err != nil {
		return fmt.Errorf("failed to create event syncer: %w", err)
	}

	// Log timing configuration
	currentPhase := oracle.GetTimingForEpoch(cfg.OracleTiming, 0)
	log.Printf("Oracle timing: %d phases configured, first phase: startEpoch=%d, interval=%d",
		len(cfg.OracleTiming), currentPhase.StartEpoch, currentPhase.Interval)

	// Create Ethereum client for oracle commits
	var ethClient *contract.Client
	if mockMode {
		ethClient = contract.NewMockClient(storage)
	} else {
		var err error
		ethClient, err = contract.NewClient(cfg.EthRPC, cfg.OracleContract, privateKey)
		if err != nil {
			return fmt.Errorf("failed to create Ethereum client: %w", err)
		}
		defer ethClient.Close()
	}

	// Create oracle
	oracleCfg := &oracle.Config{
		Storage:        storage,
		ContractClient: ethClient,
		TimingPhases:   cfg.OracleTiming,
	}

	oracleInstance := oracle.New(oracleCfg)

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down gracefully...", sig)
		cancel()
	}()

	// Perform initial sync (blocking, sequential)
	log.Println("Syncing SSV contract events...")
	if err := syncer.SyncToFinalized(ctx, cfg.SyncFromBlock); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// Run oracle loop (which will do incremental syncs and balance fetching)
	log.Println("Starting oracle commit loop...")

	if err := oracleInstance.Run(ctx, syncer, beaconClient); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("oracle error: %w", err)
	}

	log.Println("Oracle shutdown complete")
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
