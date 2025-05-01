package factory

import (
	"context"
	"fmt"
	"github.com/chatton/interchaintest/framework/cosmos"
	"github.com/chatton/interchaintest/framework/types"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
)

const (
	defaultNumValidators = 2
	defaultNumFullNodes  = 1
)

// NewChain creates a single chain based on the provided ChainSpec.
func NewChain(log *zap.Logger, testName string, client *client.Client, networkID string, spec *types.ChainSpec) (types.Chain, error) {
	cfg := spec.Config
	chain, err := buildChain(log, testName, cfg, spec.NumValidators, spec.NumFullNodes)
	if err != nil {
		return nil, err
	}

	// Initialize the chain with docker resources
	if err := chain.Initialize(context.Background(), testName, client, networkID); err != nil {
		return nil, fmt.Errorf("failed to initialize chain: %w", err)
	}

	return chain, nil
}

// buildChain creates and returns a new blockchain instance based on the given configuration and parameters.
func buildChain(log *zap.Logger, testName string, cfg types.Config, numValidators, numFullNodes *int) (types.Chain, error) {
	nv := defaultNumValidators
	if numValidators != nil {
		nv = *numValidators
	}
	nf := defaultNumFullNodes
	if numFullNodes != nil {
		nf = *numFullNodes
	}

	if cfg.Type != cosmos.Type {
		return nil, fmt.Errorf("unknown chain type: %s", cfg.Type)
	}

	return cosmos.NewCosmosChain(testName, cfg, nv, nf, log), nil
}
