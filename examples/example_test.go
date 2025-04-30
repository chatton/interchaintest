package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/celestiaorg/go-square/v2/share"
	"github.com/chatton/interchaintest/v1/dockerutil"
	testutil2 "github.com/chatton/interchaintest/v1/testutil"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/docker/docker/api/types"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
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
			ModifyGenesis: func(config ibc.Config, bytes []byte) ([]byte, error) {
				var genesis map[string]interface{}
				if err := json.Unmarshal(bytes, &genesis); err != nil {
					return nil, err
				}

				consensus, ok := genesis["consensus"].(map[string]interface{})
				if !ok {
					consensus = make(map[string]interface{})
					genesis["consensus"] = consensus
				}

				params, ok := consensus["params"].(map[string]interface{})
				if !ok {
					params = make(map[string]interface{})
					consensus["params"] = params
				}

				version, ok := params["version"].(map[string]interface{})
				if !ok {
					version = make(map[string]interface{})
					params["version"] = version
				}

				version["app"] = "4" // TODO: hack to set this to 4 for the multiplexer, figure out how to set this a better way.

				patched, err := json.MarshalIndent(genesis, "", "  ")
				if err != nil {
					return nil, err
				}
				return patched, nil
			},
			EncodingConfig:      &enc,
			AdditionalStartArgs: []string{"--force-no-bbr", "--grpc.enable", "--grpc.address", "0.0.0.0:9090", "--rpc.grpc_laddr=tcp://0.0.0.0:9099"},
			Type:                "cosmos",
			ChainID:             "celestia",
			Images: []ibc.DockerImage{
				{
					Repository: "ghcr.io/celestiaorg/celestia-app-multiplexer",
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
	err = celestia.Start(ctx)
	require.NoError(t, err)

	// Cleanup resources when the test is done
	t.Cleanup(func() {
		if err := celestia.Stop(ctx); err != nil {
			t.Logf("Error stopping chain: %v", err)
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

	createTxSim(t, err, ctx, client, network, logger, celestia, cosmosChain)

	// Wait for a short time to allow txsim to start
	time.Sleep(10 * time.Second)

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

func TestCelestiaChainStateSync(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	const (
		blocksToProduce            = 30
		stateSyncTrustHeightOffset = 5
		stateSyncTimeout           = 5 * time.Minute
	)

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
			ModifyGenesis: func(config ibc.Config, bytes []byte) ([]byte, error) {
				var genesis map[string]interface{}
				if err := json.Unmarshal(bytes, &genesis); err != nil {
					return nil, err
				}

				consensus, ok := genesis["consensus"].(map[string]interface{})
				if !ok {
					consensus = make(map[string]interface{})
					genesis["consensus"] = consensus
				}

				params, ok := consensus["params"].(map[string]interface{})
				if !ok {
					params = make(map[string]interface{})
					consensus["params"] = params
				}

				version, ok := params["version"].(map[string]interface{})
				if !ok {
					version = make(map[string]interface{})
					params["version"] = version
				}

				version["app"] = "4" // TODO: hack to set this to 4 for the multiplexer, figure out how to set this a better way.

				patched, err := json.MarshalIndent(genesis, "", "  ")
				if err != nil {
					return nil, err
				}
				return patched, nil
			},
			EncodingConfig:      &enc,
			AdditionalStartArgs: []string{"--force-no-bbr", "--grpc.enable", "--grpc.address", "0.0.0.0:9090", "--rpc.grpc_laddr=tcp://0.0.0.0:9099"},
			Type:                "cosmos",
			ChainID:             "celestia",
			Images: []ibc.DockerImage{
				{
					Repository: "ghcr.io/celestiaorg/celestia-app-multiplexer",
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
	err = celestia.Start(ctx)
	require.NoError(t, err)

	// Cleanup resources when the test is done
	t.Cleanup(func() {
		if err := celestia.Stop(ctx); err != nil {
			t.Logf("Error stopping chain: %v", err)
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

	createTxSim(t, err, ctx, client, network, logger, celestia, cosmosChain)

	nodeClient := cosmosChain.Nodes()[0].Client

	initialHeight := int64(0)

	// Use a ticker to periodically check for the initial height
	heightTicker := time.NewTicker(1 * time.Second)
	defer heightTicker.Stop()

	heightTimeoutCtx, heightCancel := context.WithTimeout(ctx, 30*time.Second) // Wait up to 30 seconds for the first block
	defer heightCancel()

	// Check immediately first, then on ticker intervals
	for {
		status, err := nodeClient.Status(heightTimeoutCtx)
		if err == nil && status.SyncInfo.LatestBlockHeight > 0 {
			initialHeight = status.SyncInfo.LatestBlockHeight
			break
		}

		select {
		case <-heightTicker.C:
			// Continue the loop
		case <-heightTimeoutCtx.Done():
			t.Logf("Timed out waiting for initial height")
			break
		}
	}

	require.Greater(t, initialHeight, int64(0), "failed to get initial height")
	targetHeight := initialHeight + blocksToProduce
	t.Logf("Successfully reached initial height %d", initialHeight)

	require.NoError(t, testutil2.WaitForBlocks(ctx, int(targetHeight), celestia), "failed to wait for target height")

	t.Logf("Successfully reached target height %d", targetHeight)

	t.Logf("Gathering state sync parameters")
	status, err := nodeClient.Status(ctx)
	require.NoError(t, err, "failed to get node zero client")

	latestHeight := status.SyncInfo.LatestBlockHeight
	trustHeight := latestHeight - stateSyncTrustHeightOffset
	require.Greaterf(t, trustHeight, int64(0), "calculated trust height %d is too low (latest height: %d)", trustHeight, latestHeight)

	trustBlock, err := nodeClient.Block(ctx, &trustHeight)
	require.NoError(t, err, "failed to get block at trust height %d", trustHeight)

	trustHash := trustBlock.BlockID.Hash.String()
	rpcServers := cosmosChain.Nodes().RPCString(ctx)

	t.Logf("Trust height: %d", trustHeight)
	t.Logf("Trust hash: %s", trustHash)
	t.Logf("RPC servers: %s", rpcServers)

	overrides := map[string]any{}
	configOverrides := make(testutil2.Toml)
	configOverrides["state_sync.enable"] = true
	configOverrides["state_sync.trust_height"] = trustHeight
	configOverrides["state_sync.trust_hash"] = trustHash
	configOverrides["state_sync.rpc_servers"] = rpcServers

	overrides["config/config.toml"] = configOverrides
	err = cosmosChain.AddFullNodes(ctx, overrides, 1)
	require.NoError(t, err, "failed to add node")

	stateSyncClient := cosmosChain.FullNodes[0].Client

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	timeoutCtx, cancel := context.WithTimeout(ctx, stateSyncTimeout)
	defer cancel()

	// Check immediately first, then on ticker intervals
	for {
		status, err := stateSyncClient.Status(timeoutCtx)
		if err != nil {
			t.Logf("Failed to get status from state sync node, retrying...: %v", err)
			select {
			case <-ticker.C:
				continue
			case <-timeoutCtx.Done():
				t.Fatalf("timed out waiting for state sync node to catch up after %v", stateSyncTimeout)
			}
		}

		t.Logf("State sync node status: Height=%d, CatchingUp=%t", status.SyncInfo.LatestBlockHeight, status.SyncInfo.CatchingUp)

		if !status.SyncInfo.CatchingUp && status.SyncInfo.LatestBlockHeight >= latestHeight {
			t.Logf("State sync successful! Node caught up to height %d", status.SyncInfo.LatestBlockHeight)
			break
		}

		select {
		case <-ticker.C:
			// Continue the loop
		case <-timeoutCtx.Done():
			t.Fatalf("timed out waiting for state sync node to catch up after %v", stateSyncTimeout)
		}
	}
}

func createTxSim(t *testing.T, err error, ctx context.Context, client *client.Client, network string, logger *zap.Logger, celestia ibc.Chain, cosmosChain *cosmos.Chain) {
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
	t.Log("TxSim container started successfully")
	t.Logf("TxSim container ID: %s", container.Name)

	// Wait for a short time to allow txsim to start
	time.Sleep(10 * time.Second)

	// Cleanup the container when the test is done
	t.Cleanup(func() {
		if err := container.Stop(10 * time.Second); err != nil {
			t.Logf("Error stopping txsim container: %v", err)
		}
	})
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
