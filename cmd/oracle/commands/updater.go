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

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"ssv-oracle/contract"
	"ssv-oracle/oracle"
	"ssv-oracle/pkg/ethsync"
	"ssv-oracle/updater"
)

var (
	updaterConfigPath string
)

// updaterCmd represents the updater command
var updaterCmd = &cobra.Command{
	Use:   "updater",
	Short: "Start the cluster updater service",
	Long: `Start the cluster updater service that listens for RootCommitted events
and updates cluster balances on-chain using merkle proofs.

In mock mode: listens for PostgreSQL NOTIFY when new commits are inserted.
In real mode: subscribes to RootCommitted events from the oracle contract.`,
	RunE: runUpdater,
}

func init() {
	updaterCmd.Flags().StringVarP(&updaterConfigPath, "config", "c", "config.yaml", "Path to configuration file")
}

// UpdaterConfig extends Config with updater-specific settings
type UpdaterConfig struct {
	// Network
	EthRPC    string `yaml:"eth_rpc"`
	BeaconRPC string `yaml:"beacon_rpc"`

	// Contracts
	OracleContract string `yaml:"oracle_contract"`

	// Database
	DBHost        string `yaml:"db_host"`
	DBPort        int    `yaml:"db_port"`
	DBName        string `yaml:"db_name"`
	DBUser        string `yaml:"db_user"`
	DBPasswordEnv string `yaml:"db_password_env"`

	// Oracle
	PrivateKeyEnv string `yaml:"private_key_env"`
	OracleTiming  []oracle.TimingPhase `yaml:"oracle_timing"`
}

func runUpdater(_ *cobra.Command, _ []string) error {
	// Load configuration
	cfg, err := loadUpdaterConfig(updaterConfigPath)
	if err != nil {
		return err
	}

	// Enable mock mode if oracle contract is zero address
	mockMode := cfg.OracleContract == "0x0000000000000000000000000000000000000000"

	// Validate timing config
	if err := oracle.ValidateTimingPhases(cfg.OracleTiming); err != nil {
		log.Fatalf("Invalid timing config: %v", err)
	}

	// Get private key from environment
	privateKey := os.Getenv(cfg.PrivateKeyEnv)
	if privateKey == "" && !mockMode {
		log.Fatalf("Private key not found in environment variable %s", cfg.PrivateKeyEnv)
	}

	// Get database password from environment
	dbPassword := os.Getenv(cfg.DBPasswordEnv)
	if dbPassword == "" {
		log.Fatalf("Database password not found in environment variable %s", cfg.DBPasswordEnv)
	}

	log.Printf("SSV Cluster Updater %s", Version)
	log.Printf("Oracle Contract: %s", cfg.OracleContract)
	if mockMode {
		log.Println("Running in mock mode (oracle contract is zero address)")
	}

	// Build connection string
	connString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, dbPassword, cfg.DBName)

	// Create PostgreSQL storage
	storage, err := ethsync.NewPostgresStorage(connString)
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}
	defer func() {
		if err := storage.Close(); err != nil {
			log.Printf("Warning: failed to close storage: %v", err)
		}
	}()

	// Create beacon client for spec
	beaconClient := ethsync.NewBeaconClient(ethsync.BeaconClientConfig{
		URL:        cfg.BeaconRPC,
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		RetryDelay: 5 * time.Second,
	})

	spec, err := beaconClient.GetSpec(context.Background())
	if err != nil {
		log.Fatalf("Failed to get beacon spec: %v", err)
	}
	log.Printf("Beacon spec: slotsPerEpoch=%d", spec.SlotsPerEpoch)

	// Create contract client
	var ethClient *contract.Client
	if mockMode {
		ethClient = contract.NewMockClient(storage)
	} else {
		var err error
		ethClient, err = contract.NewClient(cfg.EthRPC, cfg.OracleContract, privateKey)
		if err != nil {
			log.Fatalf("Failed to create Ethereum client: %v", err)
		}
		defer ethClient.Close()
	}

	// Create updater
	updaterInstance := updater.New(&updater.Config{
		Storage:        storage,
		ContractClient: ethClient,
		Spec:           spec,
		TimingPhases:   cfg.OracleTiming,
		MockMode:       mockMode,
		DBConnString:   connString,
	})

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

	// Run updater
	log.Println("Starting cluster updater...")

	if err := updaterInstance.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("Updater error: %v", err)
	}

	log.Println("Updater shutdown complete")
	return nil
}

func loadUpdaterConfig(path string) (*UpdaterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg UpdaterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
