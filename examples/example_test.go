package examples

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/chatton/interchaintest/v1"
	"github.com/chatton/interchaintest/v1/chain/cosmos"
	"github.com/chatton/interchaintest/v1/ibc"
	"github.com/chatton/interchaintest/v1/testreporter"
)

func TestCelestiaChain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	t.Parallel()

	// Create a new logger for the test
	logger := zaptest.NewLogger(t)

	// Setup Docker client and network
	client, network := interchaintest.DockerSetup(t)

	// Define the number of validators
	numValidators := 4
	numFullNodes := 0

	// Create a chain factory with a single Celestia chain
	cf := interchaintest.NewBuiltinChainFactory(logger, []*interchaintest.ChainSpec{
		{
			Name:          "celestia",
			ChainName:     "celestia",
			Version:       "v4.0.0-rc1",
			NumValidators: &numValidators,
			NumFullNodes:  &numFullNodes,
			Config: ibc.Config{
				AdditionalStartArgs: []string{"--force-no-bbr"},
				Type:                "cosmos",
				ChainID:             "celestia",
				Images: []ibc.DockerImage{
					{
						Repository: "ghcr.io/celestiaorg/celestia-app",
						Version:    "v4.0.0-rc1",
						UIDGID:     "10001:10001",
					},
				},
				Bin:           "celestia-appd",
				Bech32Prefix:  "celestia",
				Denom:         "utia",
				GasPrices:     "0.025utia",
				GasAdjustment: 1.3,
			},
		},
	})

	// Get the chains from the factory
	chains, err := cf.Chains(t.Name())
	require.NoError(t, err)

	// We only have one chain
	celestia := chains[0]

	// Create an Interchain object
	ic := interchaintest.NewInterchain().
		AddChain(celestia)

	// Create a test reporter
	rep := testreporter.NewNopReporter()
	eRep := rep.RelayerExecReporter(t)

	// Build the interchain
	ctx := context.Background()
	require.NoError(t, ic.Build(ctx, eRep, interchaintest.InterchainBuildOptions{
		TestName:  t.Name(),
		Client:    client,
		NetworkID: network,
	}))
	defer ic.Close()

	// Verify the chain is running
	height, err := celestia.Height(ctx)
	require.NoError(t, err)
	require.Greater(t, height, int64(0))

	// Get the validators
	cosmosChain, ok := celestia.(*cosmos.Chain)
	require.True(t, ok, "expected celestia to be a cosmos.Chain")

	// Verify we have the expected number of validators
	require.Equal(t, numValidators, len(cosmosChain.Validators))

	t.Logf("Successfully started Celestia chain with %d validators", numValidators)
}
