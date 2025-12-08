package wallet

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Signer signs Ethereum transactions.
type Signer interface {
	Sign(tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)
	Address() common.Address
	Close() error
}

// Config holds wallet configuration.
type Config struct {
	Type          string `yaml:"type"`            // "env" or "keystore"
	PrivateKeyEnv string `yaml:"private_key_env"` // env var with private key
	KeystorePath  string `yaml:"keystore_path"`   // path to keystore file
	PasswordEnv   string `yaml:"password_env"`    // env var with password
	PasswordFile  string `yaml:"password_file"`   // file with password
}
