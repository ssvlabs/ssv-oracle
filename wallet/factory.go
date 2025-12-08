package wallet

import "fmt"

// Signer type constants.
const (
	TypeEnv      = "env"
	TypeKeystore = "keystore"
)

// NewSigner creates a Signer based on the provided configuration.
func NewSigner(cfg *Config) (Signer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wallet config is nil")
	}

	switch cfg.Type {
	case TypeEnv, "":
		if cfg.PrivateKeyEnv == "" {
			return nil, fmt.Errorf("private_key_env is required for env signer")
		}
		return NewEnvSigner(cfg.PrivateKeyEnv)

	case TypeKeystore:
		if cfg.KeystorePath == "" {
			return nil, fmt.Errorf("keystore_path is required for keystore signer")
		}
		if cfg.PasswordEnv == "" && cfg.PasswordFile == "" {
			return nil, fmt.Errorf("password_env or password_file is required for keystore signer")
		}
		return NewKeystoreSigner(cfg.KeystorePath, cfg.PasswordEnv, cfg.PasswordFile)

	default:
		return nil, fmt.Errorf("unsupported wallet type: %s", cfg.Type)
	}
}
