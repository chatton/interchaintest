package examples

import (
	"context"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	celestiaapp "github.com/celestiaorg/celestia-app/v4/app"
	"github.com/chatton/interchaintest/v1"
	"github.com/chatton/interchaintest/v1/chain/cosmos"
	"github.com/chatton/interchaintest/v1/ibc"
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

	enc := testutil.MakeTestEncodingConfig(celestiaapp.ModuleEncodingRegisters...)
	// Create a single Celestia chain directly
	celestia, err := interchaintest.NewChain(logger, t.Name(), client, network, &interchaintest.ChainSpec{
		Name:          "celestia",
		ChainName:     "celestia",
		Version:       "v4.0.0-rc1",
		NumValidators: &numValidators,
		NumFullNodes:  &numFullNodes,
		Config: ibc.Config{
			EncodingConfig:      &enc,
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
	})
	require.NoError(t, err)

	// Start the chain
	ctx := context.Background()
	additionalGenesisWallets := []ibc.WalletAmount{}
	require.NoError(t, celestia.Start(t.Name(), ctx, additionalGenesisWallets...))

	// Cleanup resources when the test is done
	t.Cleanup(func() {
		if err := celestia.(*cosmos.Chain).StopAllNodes(ctx); err != nil {
			t.Logf("Error stopping chain nodes: %v", err)
		}
	})

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
