package commands

import (
	"strings"
	"testing"

	"ssv-oracle/oracle"
)

func validConfig() *Config {
	return &Config{
		EthRPC:                 "http://localhost:8545",
		BeaconRPC:              "http://localhost:5052",
		SSVContract:            "0x1234567890123456789012345678901234567890",
		SSVContractDeployBlock: 17507487,
		Schedule:               oracle.CommitSchedule{{StartEpoch: 0, Interval: 225}},
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		modify      func(*Config)
		withUpdater bool
		wantErr     string
	}{
		{
			name:    "valid config",
			modify:  func(c *Config) {},
			wantErr: "",
		},
		{
			name:    "missing eth_rpc",
			modify:  func(c *Config) { c.EthRPC = "" },
			wantErr: "eth_rpc is required",
		},
		{
			name:    "missing beacon_rpc",
			modify:  func(c *Config) { c.BeaconRPC = "" },
			wantErr: "beacon_rpc is required",
		},
		{
			name:    "missing ssv_contract",
			modify:  func(c *Config) { c.SSVContract = "" },
			wantErr: "ssv_contract is required",
		},
		{
			name:    "invalid ssv_contract address",
			modify:  func(c *Config) { c.SSVContract = "not-an-address" },
			wantErr: "invalid ssv_contract address",
		},
		{
			name:    "missing ssv_contract_deploy_block",
			modify:  func(c *Config) { c.SSVContractDeployBlock = 0 },
			wantErr: "ssv_contract_deploy_block is required",
		},
		{
			name:    "empty schedule",
			modify:  func(c *Config) { c.Schedule = nil },
			wantErr: "invalid commit_phases",
		},
		{
			name:        "updater: missing eth_ws_rpc",
			modify:      func(c *Config) {},
			withUpdater: true,
			wantErr:     "eth_ws_rpc is required when running with --updater",
		},
		{
			name: "updater: missing ssv_views_contract",
			modify: func(c *Config) {
				c.EthWSRPC = "ws://localhost:8546"
			},
			withUpdater: true,
			wantErr:     "ssv_views_contract is required when running with --updater",
		},
		{
			name: "updater: invalid ssv_views_contract address",
			modify: func(c *Config) {
				c.EthWSRPC = "ws://localhost:8546"
				c.SSVViewsContract = "not-an-address"
			},
			withUpdater: true,
			wantErr:     "invalid ssv_views_contract address",
		},
		{
			name: "updater: valid config",
			modify: func(c *Config) {
				c.EthWSRPC = "ws://localhost:8546"
				c.SSVViewsContract = "0x1234567890123456789012345678901234567890"
			},
			withUpdater: true,
			wantErr:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(cfg)

			err := cfg.Validate(tt.withUpdater)

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Errorf("Validate() expected error containing %q, got nil", tt.wantErr)
				return
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConfig_Defaults(t *testing.T) {
	cfg := &Config{}

	if cfg.DBPath != "" {
		t.Errorf("DBPath should be empty before loadConfig, got %q", cfg.DBPath)
	}

	// loadConfig sets defaults, but we can't easily test that without a file
	// Just verify the constant is defined correctly
	if defaultDBPath != "./data/oracle.db" {
		t.Errorf("defaultDBPath = %q, want %q", defaultDBPath, "./data/oracle.db")
	}
}
