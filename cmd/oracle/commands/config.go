package commands

import (
	"errors"
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"gopkg.in/yaml.v3"

	"ssv-oracle/oracle"
	"ssv-oracle/txmanager"
	"ssv-oracle/wallet"
)

const defaultDBPath = "./data/oracle.db"

// Config represents the oracle configuration file.
type Config struct {
	EthRPC    string `yaml:"eth_rpc"`
	EthWSRPC  string `yaml:"eth_ws_rpc"` // Required only with --updater
	BeaconRPC string `yaml:"beacon_rpc"`

	SSVContract      string `yaml:"ssv_contract"`
	SSVViewsContract string `yaml:"ssv_views_contract"` // Required only with --updater

	SSVContractDeployBlock uint64 `yaml:"ssv_contract_deploy_block"`
	SyncBatchSize          uint64 `yaml:"sync_batch_size"`

	DBPath string `yaml:"db_path"`

	Wallet   wallet.Config         `yaml:"wallet"`
	TxPolicy txmanager.TxPolicy    `yaml:"tx_policy"`
	Schedule oracle.CommitSchedule `yaml:"commit_phases"`
}

// Validate checks config format and syntax.
func (c *Config) Validate(withUpdater bool) error {
	if c.EthRPC == "" {
		return errors.New("eth_rpc is required")
	}
	if c.BeaconRPC == "" {
		return errors.New("beacon_rpc is required")
	}
	if c.SSVContract == "" {
		return errors.New("ssv_contract is required")
	}
	if !common.IsHexAddress(c.SSVContract) {
		return fmt.Errorf("invalid ssv_contract address: %s", c.SSVContract)
	}
	if c.SSVContractDeployBlock == 0 {
		return errors.New("ssv_contract_deploy_block is required")
	}
	if err := c.Schedule.Validate(); err != nil {
		return fmt.Errorf("invalid commit_phases: %w", err)
	}
	if withUpdater {
		if c.EthWSRPC == "" {
			return errors.New("eth_ws_rpc is required when running with --updater")
		}
		if c.SSVViewsContract == "" {
			return errors.New("ssv_views_contract is required when running with --updater")
		}
		if !common.IsHexAddress(c.SSVViewsContract) {
			return fmt.Errorf("invalid ssv_views_contract address: %s", c.SSVViewsContract)
		}
	}
	return nil
}

// loadConfig reads and validates the configuration file.
func loadConfig(path string, withUpdater bool) (*Config, error) {
	cfg := &Config{
		DBPath: defaultDBPath,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config %s: %w", path, err)
	}

	if err := cfg.Validate(withUpdater); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}
