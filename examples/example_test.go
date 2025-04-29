package examples

import (
	"context"
	"fmt"
	"github.com/celestiaorg/go-square/v2/share"
	"github.com/chatton/interchaintest/v1/dockerutil"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/docker/docker/api/types"
	"github.com/moby/moby/client"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	celestiaapp "github.com/celestiaorg/celestia-app/v4/app"
	"github.com/celestiaorg/celestia-app/v4/test/util/testnode"
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
	numValidators := 1
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
			AdditionalStartArgs: []string{"--force-no-bbr", "--grpc.enable", "--grpc.address", "0.0.0.0:9090"},
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

	networkName, err := GetNetworkNameFromID(ctx, client, network)
	require.NoError(t, err)

	// Deploy txsim image
	t.Log("Deploying txsim image")
	txsimImage := dockerutil.NewImage(logger, client, networkName, t.Name(), "ghcr.io/celestiaorg/txsim", "v4.0.0-rc1")

	// Get the RPC address to connect to the Celestia node
	rpcAddress := celestia.GetHostRPCAddress()
	t.Logf("Connecting to Celestia node at %s", rpcAddress)

	// Run the txsim container
	opts := dockerutil.ContainerOptions{
		User: dockerutil.GetRootUserString(),
		// Mount the Celestia home directory into the txsim container
		Binds: []string{cosmosChain.Validators[0].VolumeName + ":/celestia-home"},
	}

	t.Logf("waiting for grpc service to be online")
	time.Sleep(10 * time.Second)

	args := []string{
		"/bin/txsim",
		"--key-path", "/celestia-home",
		"--grpc-endpoint", celestia.GetGRPCAddress(),
		"--poll-time", "1s",
		"--seed", "42",
		"--blob", "10",
		"--blob-amounts", "100",
		"--blob-sizes", "100-2000",
		"--gas-price", "0.025",
		"--blob-share-version", fmt.Sprintf("%d", share.ShareVersionZero),
	}

	// Start the txsim container
	container, err := txsimImage.Start(ctx, args, opts)
	require.NoError(t, err, "Failed to start txsim container")

	// Cleanup the container when the test is done
	t.Cleanup(func() {
		if err := container.Stop(10 * time.Second); err != nil {
			t.Logf("Error stopping txsim container: %v", err)
		}
	})

	// Wait for a short time to allow txsim to start
	time.Sleep(10 * time.Second)

	// Check if the container is running
	t.Log("TxSim container started successfully")
	t.Logf("TxSim container ID: %s", container.Name)

	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	const requiredTxs = 10
	const pollInterval = 5 * time.Second

	// periodically check for transactions until timeout or required transactions are found
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check for transactions
			blockchain, err := testnode.ReadBlockchainHeaders(ctx, celestia.GetHostRPCAddress())
			if err != nil {
				t.Logf("Error reading blockchain headers: %v", err)
				continue
			}

			totalTxs := 0
			for _, blockMeta := range blockchain {
				totalTxs += blockMeta.NumTxs
			}

			t.Logf("Current transaction count: %d", totalTxs)

			if totalTxs >= requiredTxs {
				t.Logf("Found %d transactions, continuing with test", totalTxs)
				return
			}
		case <-pollCtx.Done():
			t.Logf("Timed out waiting for %d transactions", requiredTxs)
			t.Failed()
		}
	}
}

// GetNetworkNameFromID resolves the network name given its ID.
func GetNetworkNameFromID(ctx context.Context, cli *client.Client, networkID string) (string, error) {
	network, err := cli.NetworkInspect(ctx, networkID, types.NetworkInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to inspect network %s: %w", networkID, err)
	}
	if network.Name == "" {
		return "", fmt.Errorf("network %s has no name", networkID)
	}
	return network.Name, nil
}
