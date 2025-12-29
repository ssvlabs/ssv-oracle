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

// envSigner signs transactions using a private key from an environment variable.
//
// WARNING: For development/testing only. Not recommended for production.
// Environment variables may be logged, visible in process listings, or persisted
// in shell history. Private keys are not securely erased by Go's GC.
// For production, use an encrypted keystore signer.
type envSigner struct {
	privateKey *ecdsa.PrivateKey
	address    common.Address
}

func newEnvSigner(envVarName string) (*envSigner, error) {
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

	return &envSigner{
		privateKey: privateKey,
		address:    crypto.PubkeyToAddress(privateKey.PublicKey),
	}, nil
}

// Address returns the signer's Ethereum address.
func (s *envSigner) Address() common.Address {
	return s.address
}

// Close zeros out the private key (best-effort).
func (s *envSigner) Close() error {
	if s.privateKey != nil && s.privateKey.D != nil {
		s.privateKey.D.SetInt64(0)
	}
	s.privateKey = nil
	return nil
}

// Sign signs a transaction with the private key.
func (s *envSigner) Sign(tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	if s.privateKey == nil {
		return nil, fmt.Errorf("signer is closed")
	}
	if chainID == nil || chainID.Sign() == 0 {
		return nil, fmt.Errorf("chainID is required (EIP-155 replay protection)")
	}
	return types.SignTx(tx, types.LatestSignerForChainID(chainID), s.privateKey)
}
