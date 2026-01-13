package commands

import (
	"os"
	"strings"
	"testing"

	"github.com/ssvlabs/ssv-oracle/oracle"
)

func validConfig() *config {
	return &config{
		EthRPC:                 "http://localhost:8545",
		BeaconRPC:              "http://localhost:5052",
		SSVContract:            "0x1234567890123456789012345678901234567890",
		SSVViewsContract:       "0x1234567890123456789012345678901234567890",
		SSVContractDeployBlock: 17507487,
		Schedule:               oracle.CommitSchedule{{StartEpoch: 0, Interval: 225}},
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		modify      func(*config)
		withUpdater bool
		wantErr     string
	}{
		{
			name:    "valid config",
			modify:  func(c *config) {},
			wantErr: "",
		},
		{
			name:    "whitespace trimmed",
			modify:  func(c *config) { c.EthRPC = "  http://localhost:8545  " },
			wantErr: "",
		},
		{
			name:    "missing eth_rpc",
			modify:  func(c *config) { c.EthRPC = "" },
			wantErr: "eth_rpc is required",
		},
		{
			name:    "invalid eth_rpc url",
			modify:  func(c *config) { c.EthRPC = "not-a-url" },
			wantErr: "invalid eth_rpc url",
		},
		{
			name:    "wrong eth_rpc scheme",
			modify:  func(c *config) { c.EthRPC = "ws://localhost:8545" },
			wantErr: "invalid eth_rpc scheme",
		},
		{
			name:    "missing beacon_rpc",
			modify:  func(c *config) { c.BeaconRPC = "" },
			wantErr: "beacon_rpc is required",
		},
		{
			name:    "missing ssv_contract",
			modify:  func(c *config) { c.SSVContract = "" },
			wantErr: "ssv_contract is required",
		},
		{
			name:    "invalid ssv_contract address",
			modify:  func(c *config) { c.SSVContract = "not-an-address" },
			wantErr: "invalid ssv_contract address",
		},
		{
			name:    "zero ssv_contract address",
			modify:  func(c *config) { c.SSVContract = "0x0000000000000000000000000000000000000000" },
			wantErr: "ssv_contract cannot be zero address",
		},
		{
			name:    "missing ssv_contract_deploy_block",
			modify:  func(c *config) { c.SSVContractDeployBlock = 0 },
			wantErr: "ssv_contract_deploy_block is required",
		},
		{
			name:    "empty schedule",
			modify:  func(c *config) { c.Schedule = nil },
			wantErr: "invalid commit_phases",
		},
		{
			name:    "missing ssv_views_contract",
			modify:  func(c *config) { c.SSVViewsContract = "" },
			wantErr: "ssv_views_contract is required",
		},
		{
			name:    "invalid ssv_views_contract address",
			modify:  func(c *config) { c.SSVViewsContract = "not-an-address" },
			wantErr: "invalid ssv_views_contract address",
		},
		{
			name:        "updater: missing eth_ws_rpc",
			modify:      func(c *config) {},
			withUpdater: true,
			wantErr:     "eth_ws_rpc is required",
		},
		{
			name: "updater: valid config",
			modify: func(c *config) {
				c.EthWSRPC = "ws://localhost:8546"
			},
			withUpdater: true,
			wantErr:     "",
		},
		{
			name: "updater: wrong eth_ws_rpc scheme",
			modify: func(c *config) {
				c.EthWSRPC = "http://localhost:8546"
			},
			withUpdater: true,
			wantErr:     "invalid eth_ws_rpc scheme",
		},
		{
			name: "optional eth_ws_rpc ignored when not updater",
			modify: func(c *config) {
				c.EthWSRPC = "http://localhost:8546" // Wrong scheme, but ignored
			},
			withUpdater: false,
			wantErr:     "", // Not validated when updater disabled
		},
		{
			name: "multiple errors collected",
			modify: func(c *config) {
				c.EthRPC = ""
				c.BeaconRPC = ""
				c.SSVContract = ""
			},
			wantErr: "eth_rpc is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(cfg)

			err := cfg.validate(tt.withUpdater)

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
	cfg := &config{}

	if cfg.DBPath != "" {
		t.Errorf("DBPath should be empty before loadConfig, got %q", cfg.DBPath)
	}

	// loadConfig sets defaults, but we can't easily test that without a file
	// Just verify the constant is defined correctly
	if defaultDBPath != "./data/oracle.db" {
		t.Errorf("defaultDBPath = %q, want %q", defaultDBPath, "./data/oracle.db")
	}
}

func TestConfig_MultipleErrors(t *testing.T) {
	cfg := &config{
		EthRPC:                 "",
		BeaconRPC:              "",
		SSVContract:            "",
		SSVViewsContract:       "",
		SSVContractDeployBlock: 0,
		Schedule:               nil,
	}

	err := cfg.validate(false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errStr := err.Error()
	expected := []string{
		"eth_rpc is required",
		"beacon_rpc is required",
		"ssv_contract is required",
		"ssv_views_contract is required",
		"ssv_contract_deploy_block is required",
		"invalid commit_phases",
	}

	for _, want := range expected {
		if !strings.Contains(errStr, want) {
			t.Errorf("error should contain %q, got: %s", want, errStr)
		}
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.yaml", false)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Errorf("error should mention 'read config', got: %v", err)
	}
}

func TestLoadConfig_UnknownField(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	content := `
eth_rpc: "http://localhost:8545"
beacon_rpc: "http://localhost:5052"
ssv_contract: "0x1234567890123456789012345678901234567890"
ssv_views_contract: "0x1234567890123456789012345678901234567890"
ssv_contract_deploy_block: 17507487
unknown_field: "should cause error"
commit_phases:
  - start_epoch: 0
    interval: 225
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	_, err = loadConfig(tmpFile.Name(), false)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("error should mention 'unknown_field', got: %v", err)
	}
}

func TestLoadConfig_DefaultDBPath(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Config without db_path - should use default
	content := `
eth_rpc: "http://localhost:8545"
beacon_rpc: "http://localhost:5052"
ssv_contract: "0x1234567890123456789012345678901234567890"
ssv_views_contract: "0x1234567890123456789012345678901234567890"
ssv_contract_deploy_block: 17507487
commit_phases:
  - start_epoch: 0
    interval: 225
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	cfg, err := loadConfig(tmpFile.Name(), false)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	if cfg.DBPath != defaultDBPath {
		t.Errorf("DBPath = %q, want default %q", cfg.DBPath, defaultDBPath)
	}
}

func TestLoadConfig_ExplicitDBPath(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	content := `
eth_rpc: "http://localhost:8545"
beacon_rpc: "http://localhost:5052"
ssv_contract: "0x1234567890123456789012345678901234567890"
ssv_views_contract: "0x1234567890123456789012345678901234567890"
ssv_contract_deploy_block: 17507487
db_path: "/custom/path/oracle.db"
commit_phases:
  - start_epoch: 0
    interval: 225
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	cfg, err := loadConfig(tmpFile.Name(), false)
	if err != nil {
		t.Fatalf("loadConfig() error: %v", err)
	}

	if cfg.DBPath != "/custom/path/oracle.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/custom/path/oracle.db")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	content := `
eth_rpc: "http://localhost:8545"
  invalid_indent: true
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	_, err = loadConfig(tmpFile.Name(), false)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error should mention 'parse config', got: %v", err)
	}
}

func TestLoadConfig_ValidationError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Missing required fields
	content := `
eth_rpc: "http://localhost:8545"
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	_, err = loadConfig(tmpFile.Name(), false)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid config") {
		t.Errorf("error should mention 'invalid config', got: %v", err)
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		schemes []string
		wantErr string
	}{
		{
			name:    "valid http",
			value:   "http://localhost:8545",
			schemes: []string{"http", "https"},
			wantErr: "",
		},
		{
			name:    "valid https",
			value:   "https://mainnet.infura.io/v3/key",
			schemes: []string{"http", "https"},
			wantErr: "",
		},
		{
			name:    "valid ws",
			value:   "ws://localhost:8546",
			schemes: []string{"ws", "wss"},
			wantErr: "",
		},
		{
			name:    "valid wss",
			value:   "wss://mainnet.infura.io/ws/v3/key",
			schemes: []string{"ws", "wss"},
			wantErr: "",
		},
		{
			name:    "empty",
			value:   "",
			schemes: []string{"http"},
			wantErr: "is required",
		},
		{
			name:    "no scheme",
			value:   "localhost:8545",
			schemes: []string{"http"},
			wantErr: "invalid",
		},
		{
			name:    "wrong scheme",
			value:   "ftp://localhost:8545",
			schemes: []string{"http", "https"},
			wantErr: "invalid",
		},
		{
			name:    "no host",
			value:   "http://",
			schemes: []string{"http"},
			wantErr: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.value, "test_field", tt.schemes...)

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validateURL() unexpected error: %v", err)
				}
				return
			}

			if err == nil {
				t.Errorf("validateURL() expected error containing %q, got nil", tt.wantErr)
				return
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validateURL() error = %q, want containing %q", err.Error(), tt.wantErr)
			}
		})
	}
}
