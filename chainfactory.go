package interchaintest

import (
	"fmt"
	"github.com/chatton/interchaintest/chain/types"
	"go.uber.org/zap"

	_ "embed"

	"github.com/chatton/interchaintest/chain/cosmos"
)

const (
	defaultNumValidators = 2
	defaultNumFullNodes  = 1
)

func buildChain(log *zap.Logger, testName string, cfg types.Config, numValidators, numFullNodes *int) (types.Chain, error) {
	nv := defaultNumValidators
	if numValidators != nil {
		nv = *numValidators
	}
	nf := defaultNumFullNodes
	if numFullNodes != nil {
		nf = *numFullNodes
	}

	switch cfg.Type {
	case types.Cosmos:
		return cosmos.NewCosmosChain(testName, cfg, nv, nf, log), nil
	default:
		return nil, fmt.Errorf("unexpected error, unknown chain type: %s for chain: %s", cfg.Type, cfg.Name)
	}
}
