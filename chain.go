package interchaintest

import (
	"context"
	"fmt"
	"github.com/chatton/interchaintest/ibc"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
)

// NewChain creates a single chain based on the provided ChainSpec.
// This is a simpler alternative to using NewBuiltinChainFactory when only a single chain is needed.
//
// Example:
//
//	chain := interchaintest.NewChain(logger, t.Name(), client, networkID, &interchaintest.ChainSpec{
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
func NewChain(log *zap.Logger, testName string, client *client.Client, networkID string, spec *ChainSpec) (ibc.Chain, error) {
	cfg, err := spec.GetConfig(log)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain config: %w", err)
	}

	chain, err := buildChain(log, testName, *cfg, spec.NumValidators, spec.NumFullNodes)
	if err != nil {
		return nil, err
	}

	// Initialize the chain with docker resources
	if err := chain.Initialize(context.Background(), testName, client, networkID); err != nil {
		return nil, fmt.Errorf("failed to initialize chain: %w", err)
	}

	return chain, nil
}
