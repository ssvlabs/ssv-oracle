package wallet

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// Test private key (DO NOT use in production)
const testPrivateKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
const testAddress = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

func TestEnvSigner(t *testing.T) {
	// Set test private key in environment
	os.Setenv("TEST_PRIVATE_KEY", testPrivateKeyHex)
	defer os.Unsetenv("TEST_PRIVATE_KEY")

	signer, err := NewEnvSigner("TEST_PRIVATE_KEY")
	if err != nil {
		t.Fatalf("NewEnvSigner failed: %v", err)
	}
	defer signer.Close()

	// Verify address
	expectedAddr := common.HexToAddress(testAddress)
	if signer.Address() != expectedAddr {
		t.Errorf("Address mismatch: got %s, want %s", signer.Address().Hex(), expectedAddr.Hex())
	}

	// Test signing a transaction
	tx := createTestTransaction()
	chainID := big.NewInt(1)

	signedTx, err := signer.Sign(tx, chainID)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Verify signature
	sender, err := types.LatestSignerForChainID(chainID).Sender(signedTx)
	if err != nil {
		t.Fatalf("Failed to get sender: %v", err)
	}

	if sender != expectedAddr {
		t.Errorf("Sender mismatch: got %s, want %s", sender.Hex(), expectedAddr.Hex())
	}
}

func TestEnvSigner_MissingEnvVar(t *testing.T) {
	os.Unsetenv("NONEXISTENT_KEY")

	_, err := NewEnvSigner("NONEXISTENT_KEY")
	if err == nil {
		t.Error("Expected error for missing env var, got nil")
	}
}

func TestEnvSigner_EmptyEnvVarName(t *testing.T) {
	_, err := NewEnvSigner("")
	if err == nil {
		t.Error("Expected error for empty env var name, got nil")
	}
}

func TestEnvSigner_InvalidPrivateKey(t *testing.T) {
	os.Setenv("TEST_INVALID_KEY", "not-a-valid-key")
	defer os.Unsetenv("TEST_INVALID_KEY")

	_, err := NewEnvSigner("TEST_INVALID_KEY")
	if err == nil {
		t.Error("Expected error for invalid private key, got nil")
	}
}

func TestKeystoreSigner(t *testing.T) {
	// Create a temporary directory for the keystore
	tmpDir := t.TempDir()

	// Generate a key and store it
	privateKey, err := crypto.HexToECDSA(testPrivateKeyHex)
	if err != nil {
		t.Fatalf("Failed to parse test private key: %v", err)
	}

	// Create keystore and store the key
	ks := keystore.NewKeyStore(tmpDir, keystore.StandardScryptN, keystore.StandardScryptP)
	password := "testpassword123"

	account, err := ks.ImportECDSA(privateKey, password)
	if err != nil {
		t.Fatalf("Failed to import key: %v", err)
	}

	// Find the keystore file
	files, err := filepath.Glob(filepath.Join(tmpDir, "UTC--*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("Failed to find keystore file")
	}
	keystorePath := files[0]

	// Test with password from env var
	os.Setenv("TEST_KEYSTORE_PASSWORD", password)
	defer os.Unsetenv("TEST_KEYSTORE_PASSWORD")

	signer, err := NewKeystoreSigner(keystorePath, "TEST_KEYSTORE_PASSWORD", "")
	if err != nil {
		t.Fatalf("NewKeystoreSigner failed: %v", err)
	}
	defer signer.Close()

	// Verify address matches
	if signer.Address() != account.Address {
		t.Errorf("Address mismatch: got %s, want %s", signer.Address().Hex(), account.Address.Hex())
	}

	// Test signing
	tx := createTestTransaction()
	chainID := big.NewInt(1)

	signedTx, err := signer.Sign(tx, chainID)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	sender, err := types.LatestSignerForChainID(chainID).Sender(signedTx)
	if err != nil {
		t.Fatalf("Failed to get sender: %v", err)
	}

	if sender != account.Address {
		t.Errorf("Sender mismatch: got %s, want %s", sender.Hex(), account.Address.Hex())
	}
}

func TestKeystoreSigner_PasswordFromFile(t *testing.T) {
	// Create a temporary directory for the keystore
	tmpDir := t.TempDir()

	// Generate a key and store it
	privateKey, err := crypto.HexToECDSA(testPrivateKeyHex)
	if err != nil {
		t.Fatalf("Failed to parse test private key: %v", err)
	}

	// Create keystore and store the key
	ks := keystore.NewKeyStore(tmpDir, keystore.StandardScryptN, keystore.StandardScryptP)
	password := "testpassword123"

	_, err = ks.ImportECDSA(privateKey, password)
	if err != nil {
		t.Fatalf("Failed to import key: %v", err)
	}

	// Find the keystore file
	files, err := filepath.Glob(filepath.Join(tmpDir, "UTC--*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("Failed to find keystore file")
	}
	keystorePath := files[0]

	// Create password file
	passwordFile := filepath.Join(tmpDir, "password.txt")
	if err := os.WriteFile(passwordFile, []byte(password+"\n"), 0600); err != nil {
		t.Fatalf("Failed to write password file: %v", err)
	}

	// Test with password from file
	signer, err := NewKeystoreSigner(keystorePath, "", passwordFile)
	if err != nil {
		t.Fatalf("NewKeystoreSigner with password file failed: %v", err)
	}
	defer signer.Close()

	// Verify it works
	tx := createTestTransaction()
	_, err = signer.Sign(tx, big.NewInt(1))
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
}

func TestKeystoreSigner_WrongPassword(t *testing.T) {
	// Create a temporary directory for the keystore
	tmpDir := t.TempDir()

	privateKey, err := crypto.HexToECDSA(testPrivateKeyHex)
	if err != nil {
		t.Fatalf("Failed to parse test private key: %v", err)
	}

	ks := keystore.NewKeyStore(tmpDir, keystore.StandardScryptN, keystore.StandardScryptP)
	_, err = ks.ImportECDSA(privateKey, "correctpassword")
	if err != nil {
		t.Fatalf("Failed to import key: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(tmpDir, "UTC--*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("Failed to find keystore file")
	}

	os.Setenv("TEST_WRONG_PASSWORD", "wrongpassword")
	defer os.Unsetenv("TEST_WRONG_PASSWORD")

	_, err = NewKeystoreSigner(files[0], "TEST_WRONG_PASSWORD", "")
	if err == nil {
		t.Error("Expected error for wrong password, got nil")
	}
}

func TestFactory(t *testing.T) {
	os.Setenv("TEST_FACTORY_KEY", testPrivateKeyHex)
	defer os.Unsetenv("TEST_FACTORY_KEY")

	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "env signer",
			cfg: &Config{
				Type:          TypeEnv,
				PrivateKeyEnv: "TEST_FACTORY_KEY",
			},
			wantErr: false,
		},
		{
			name: "env signer (default type)",
			cfg: &Config{
				Type:          "",
				PrivateKeyEnv: "TEST_FACTORY_KEY",
			},
			wantErr: false,
		},
		{
			name: "env signer missing key",
			cfg: &Config{
				Type:          TypeEnv,
				PrivateKeyEnv: "",
			},
			wantErr: true,
		},
		{
			name: "keystore missing path",
			cfg: &Config{
				Type: TypeKeystore,
			},
			wantErr: true,
		},
		{
			name: "keystore missing password",
			cfg: &Config{
				Type:         TypeKeystore,
				KeystorePath: "/path/to/keystore.json",
			},
			wantErr: true,
		},
		{
			name: "unsupported type",
			cfg: &Config{
				Type: "unknown",
			},
			wantErr: true,
		},
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signer, err := NewSigner(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSigner() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if signer != nil {
				signer.Close()
			}
		})
	}
}

func TestSignerClose(t *testing.T) {
	os.Setenv("TEST_CLOSE_KEY", testPrivateKeyHex)
	defer os.Unsetenv("TEST_CLOSE_KEY")

	signer, err := NewEnvSigner("TEST_CLOSE_KEY")
	if err != nil {
		t.Fatalf("NewEnvSigner failed: %v", err)
	}

	// Close should not error
	if err := signer.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Double close should also not error
	if err := signer.Close(); err != nil {
		t.Errorf("Second Close() error = %v", err)
	}
}

// createTestTransaction creates a simple test transaction
func createTestTransaction() *types.Transaction {
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	return types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     0,
		GasTipCap: big.NewInt(1000000000),
		GasFeeCap: big.NewInt(2000000000),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      nil,
	})
}
