package interchaintest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chatton/interchaintest/v1/ibc"
	"go.uber.org/zap"
)

// CreateLogFile creates a file with name in dir $HOME/.interchaintest/logs/.
func CreateLogFile(name string) (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home dir: %w", err)
	}
	fpath := filepath.Join(home, ".interchaintest", "logs")
	err = os.MkdirAll(fpath, 0o755)
	if err != nil {
		return nil, fmt.Errorf("mkdirall: %w", err)
	}
	return os.Create(filepath.Join(fpath, name))
}

// DefaultBlockDatabaseFilepath is the default filepath to the sqlite database for tracking blocks and transactions.
func DefaultBlockDatabaseFilepath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Join(home, ".interchaintest", "databases", "block.db")
}

// NewChain creates a single chain based on the provided ChainSpec.
// This is a simpler alternative to using NewBuiltinChainFactory when only a single chain is needed.
//
// Example:
//
//	chain := interchaintest.NewChain(logger, t.Name(), &interchaintest.ChainSpec{
//		Name:          "celestia",
//		ChainName:     "celestia",
//		Version:       "v4.0.0-rc1",
//		NumValidators: &numValidators,
//		NumFullNodes:  &numFullNodes,
//		Config: ibc.Config{
//			Type:                "cosmos",
//			ChainID:             "celestia",
//			// ... other config options
//		},
//	})
func NewChain(log *zap.Logger, testName string, spec *ChainSpec) (ibc.Chain, error) {
	cfg, err := spec.GetConfig(log)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain config: %w", err)
	}

	return buildChain(log, testName, *cfg, spec.NumValidators, spec.NumFullNodes)
}
