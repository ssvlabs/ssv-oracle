package wallet

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// KeystoreSigner signs transactions using an encrypted keystore file.
type KeystoreSigner struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
}

// NewKeystoreSigner creates a signer from an encrypted keystore file.
// Password is read from passwordEnv (env var) or passwordFile.
func NewKeystoreSigner(keystorePath, passwordEnv, passwordFile string) (*KeystoreSigner, error) {
	if keystorePath == "" {
		return nil, fmt.Errorf("keystore path cannot be empty")
	}

	password, err := readPassword(passwordEnv, passwordFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read password: %w", err)
	}

	keyjson, err := os.ReadFile(keystorePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read keystore file: %w", err)
	}

	key, err := keystore.DecryptKey(keyjson, password)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt keystore: %w", err)
	}

	return &KeystoreSigner{
		privateKey: key.PrivateKey,
		address:    key.Address,
	}, nil
}

func readPassword(passwordEnv, passwordFile string) (string, error) {
	if passwordEnv != "" {
		if password := os.Getenv(passwordEnv); password != "" {
			return password, nil
		}
	}

	if passwordFile != "" {
		data, err := os.ReadFile(passwordFile)
		if err != nil {
			return "", fmt.Errorf("failed to read password file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	return "", fmt.Errorf("no password source provided: set password_env or password_file")
}

// Sign signs a transaction with the private key.
func (s *KeystoreSigner) Sign(tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	return types.SignTx(tx, types.LatestSignerForChainID(chainID), s.privateKey)
}

// Address returns the signer's Ethereum address.
func (s *KeystoreSigner) Address() common.Address {
	return s.address
}

// Close zeros out the private key (best-effort; Go's GC doesn't guarantee secure erasure).
func (s *KeystoreSigner) Close() error {
	if s.privateKey != nil && s.privateKey.D != nil {
		s.privateKey.D.SetInt64(0)
	}
	s.privateKey = nil
	return nil
}
