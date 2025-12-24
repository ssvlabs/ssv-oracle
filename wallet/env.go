package wallet

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// EnvSigner signs transactions using a private key from an environment variable.
//
// WARNING: For development/testing only. Not recommended for production.
// Environment variables may be logged, visible in process listings, or persisted
// in shell history. Private keys are not securely erased by Go's GC.
// For production, use KeystoreSigner with encrypted keystore files.
type EnvSigner struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
}

// NewEnvSigner creates a signer from an environment variable containing a hex private key.
func NewEnvSigner(envVarName string) (*EnvSigner, error) {
	if envVarName == "" {
		return nil, fmt.Errorf("environment variable name cannot be empty")
	}

	keyHex := os.Getenv(envVarName)
	if keyHex == "" {
		return nil, fmt.Errorf("environment variable %s is not set or empty", envVarName)
	}

	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("parse private key from %s: %w", envVarName, err)
	}

	return &EnvSigner{
		privateKey: privateKey,
		address:    crypto.PubkeyToAddress(privateKey.PublicKey),
	}, nil
}

// Address returns the signer's Ethereum address.
func (s *EnvSigner) Address() common.Address {
	return s.address
}

// Close zeros out the private key (best-effort).
func (s *EnvSigner) Close() error {
	if s.privateKey != nil && s.privateKey.D != nil {
		s.privateKey.D.SetInt64(0)
	}
	s.privateKey = nil
	return nil
}

// Sign signs a transaction with the private key.
func (s *EnvSigner) Sign(tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	return types.SignTx(tx, types.LatestSignerForChainID(chainID), s.privateKey)
}
