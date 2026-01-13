package commands

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"

	"github.com/ssvlabs/ssv-oracle/oracle"
	"github.com/ssvlabs/ssv-oracle/txmanager"
	"github.com/ssvlabs/ssv-oracle/wallet"
)

const (
	defaultDBPath     = "./data/oracle.db"
	defaultAPIAddress = "127.0.0.1:8080"
)

type config struct {
	LogLevel string `yaml:"log_level"` // debug, info, warn, error (default: info)

	EthRPC    string `yaml:"eth_rpc"`
	EthWSRPC  string `yaml:"eth_ws_rpc"` // Required only with --updater
	BeaconRPC string `yaml:"beacon_rpc"`
	MEVRPC    string `yaml:"mev_rpc"` // Comma-separated MEV RPC URLs (optional, for --updater)

	SSVContract      string `yaml:"ssv_contract"`
	SSVViewsContract string `yaml:"ssv_views_contract"`

	SSVContractDeployBlock uint64 `yaml:"ssv_contract_deploy_block"`
	MaxSyncBatchSize       uint64 `yaml:"max_sync_batch_size"`

	DBPath     string `yaml:"db_path"`
	APIAddress string `yaml:"api_address"`

	Wallet   wallet.Config         `yaml:"wallet"`
	TxPolicy txmanager.TxPolicy    `yaml:"tx_policy"`
	Schedule oracle.CommitSchedule `yaml:"commit_phases"`
}

// getMEVRPCs parses comma-separated MEV RPC URLs, deduplicates, and returns unique URLs.
func (c *config) getMEVRPCs() []string {
	if c.MEVRPC == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var rpcs []string
	for _, s := range strings.Split(c.MEVRPC, ",") {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			if _, exists := seen[trimmed]; !exists {
				seen[trimmed] = struct{}{}
				rpcs = append(rpcs, trimmed)
			}
		}
	}
	return rpcs
}

// validate checks all config values and returns all errors joined together, or nil if valid.
// String fields are trimmed before validation.
func (c *config) validate(withUpdater bool) error {
	c.DBPath = strings.TrimSpace(c.DBPath)
	c.LogLevel = strings.TrimSpace(c.LogLevel)

	var errs []error
	if c.LogLevel != "" {
		var lvl zapcore.Level
		if err := lvl.UnmarshalText([]byte(strings.ToLower(c.LogLevel))); err != nil {
			errs = append(errs, fmt.Errorf("invalid log_level %q: must be debug, info, warn, or error", c.LogLevel))
		}
	}
	if err := validateURL(c.EthRPC, "eth_rpc", "http", "https"); err != nil {
		errs = append(errs, err)
	}
	if err := validateURL(c.BeaconRPC, "beacon_rpc", "http", "https"); err != nil {
		errs = append(errs, err)
	}
	if err := validateAddress(c.SSVContract, "ssv_contract"); err != nil {
		errs = append(errs, err)
	}
	if err := validateAddress(c.SSVViewsContract, "ssv_views_contract"); err != nil {
		errs = append(errs, err)
	}
	if c.SSVContractDeployBlock == 0 {
		errs = append(errs, errors.New("ssv_contract_deploy_block is required"))
	}
	if err := c.Schedule.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("invalid commit_phases: %w", err))
	}
	if withUpdater {
		if err := validateURL(c.EthWSRPC, "eth_ws_rpc", "ws", "wss"); err != nil {
			errs = append(errs, err)
		}
	}

	// Validate MEV RPC URLs if configured
	for i, rpc := range c.getMEVRPCs() {
		if err := validateURL(rpc, fmt.Sprintf("mev_rpc[%d]", i), "http", "https"); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func validateAddress(addr, name string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !common.IsHexAddress(addr) {
		return fmt.Errorf("invalid %s address: %s", name, addr)
	}
	if common.HexToAddress(addr) == (common.Address{}) {
		return fmt.Errorf("%s cannot be zero address", name)
	}
	return nil
}

func validateURL(value, name string, schemes ...string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid %s url: %s", name, value)
	}

	if len(schemes) == 0 {
		return nil
	}

	scheme := strings.ToLower(parsed.Scheme)
	for _, allowed := range schemes {
		if scheme == strings.ToLower(allowed) {
			return nil
		}
	}

	return fmt.Errorf("invalid %s scheme: %s (allowed: %s)", name, parsed.Scheme, strings.Join(schemes, ", "))
}

func loadConfig(path string, withUpdater bool) (*config, error) {
	cfg := &config{
		DBPath:     defaultDBPath,
		APIAddress: defaultAPIAddress,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.validate(withUpdater); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}
