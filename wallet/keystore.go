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

// keystoreSigner signs transactions using an encrypted keystore file.
type keystoreSigner struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
}

func newKeystoreSigner(keystorePath, passwordEnv, passwordFile string) (*keystoreSigner, error) {
	if keystorePath == "" {
		return nil, fmt.Errorf("keystore path cannot be empty")
	}

	password, err := readPassword(passwordEnv, passwordFile)
	if err != nil {
		return nil, fmt.Errorf("read password: %w", err)
	}

	keyjson, err := os.ReadFile(keystorePath)
	if err != nil {
		return nil, fmt.Errorf("read keystore file: %w", err)
	}

	key, err := keystore.DecryptKey(keyjson, password)
	if err != nil {
		return nil, fmt.Errorf("decrypt keystore: %w", err)
	}

	return &keystoreSigner{
		privateKey: key.PrivateKey,
		address:    key.Address,
	}, nil
}

// Address returns the signer's Ethereum address.
func (s *keystoreSigner) Address() common.Address {
	return s.address
}

// Close zeros out the private key (best-effort).
func (s *keystoreSigner) Close() error {
	if s.privateKey != nil && s.privateKey.D != nil {
		s.privateKey.D.SetInt64(0)
	}
	s.privateKey = nil
	return nil
}

// Sign signs a transaction with the private key.
func (s *keystoreSigner) Sign(tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	if s.privateKey == nil {
		return nil, fmt.Errorf("signer is closed")
	}
	if chainID == nil || chainID.Sign() == 0 {
		return nil, fmt.Errorf("chainID is required (EIP-155 replay protection)")
	}
	return types.SignTx(tx, types.LatestSignerForChainID(chainID), s.privateKey)
}

func readPassword(passwordEnv, passwordFile string) (string, error) {
	// Prefer file over env var (files can have restricted permissions, env vars visible in /proc)
	if passwordFile != "" {
		data, err := os.ReadFile(passwordFile)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		// Only trim trailing newlines (not spaces/tabs which may be part of password)
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if passwordEnv != "" {
		password := os.Getenv(passwordEnv)
		if password == "" {
			return "", fmt.Errorf("environment variable %s is not set or empty", passwordEnv)
		}
		return password, nil
	}
	return "", fmt.Errorf("no password source provided: set password_file or password_env")
}
