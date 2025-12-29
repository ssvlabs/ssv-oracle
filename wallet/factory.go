package wallet

import "fmt"

// NewSigner creates a Signer based on the configuration.
// Type must be explicitly set to "env" or "keystore".
func NewSigner(cfg *Config) (Signer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wallet config is nil")
	}

	switch cfg.Type {
	case typeEnv:
		if cfg.PrivateKeyEnv == "" {
			return nil, fmt.Errorf("private_key_env is required for env signer")
		}
		return newEnvSigner(cfg.PrivateKeyEnv)

	case typeKeystore:
		if cfg.KeystorePath == "" {
			return nil, fmt.Errorf("keystore_path is required for keystore signer")
		}
		if cfg.PasswordEnv == "" && cfg.PasswordFile == "" {
			return nil, fmt.Errorf("password_env or password_file is required for keystore signer")
		}
		return newKeystoreSigner(cfg.KeystorePath, cfg.PasswordEnv, cfg.PasswordFile)

	case "":
		return nil, fmt.Errorf("wallet type is required: set to %q or %q", typeEnv, typeKeystore)

	default:
		return nil, fmt.Errorf("unsupported wallet type: %q (use %q or %q)", cfg.Type, typeEnv, typeKeystore)
	}
}
