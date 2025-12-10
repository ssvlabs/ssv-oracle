package wallet

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Signer type constants.
const (
	TypeEnv      = "env"
	TypeKeystore = "keystore"
)

// Config holds wallet configuration.
type Config struct {
	Type          string `yaml:"type"`
	PrivateKeyEnv string `yaml:"private_key_env"`
	KeystorePath  string `yaml:"keystore_path"`
	PasswordEnv   string `yaml:"password_env"`
	PasswordFile  string `yaml:"password_file"`
}

// Signer signs Ethereum transactions.
type Signer interface {
	Sign(tx *types.Transaction, chainID *big.Int) (*types.Transaction, error)
	Address() common.Address
	Close() error
}
