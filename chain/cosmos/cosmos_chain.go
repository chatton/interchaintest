package cosmos

import (
	"bytes"
	"context"
	"fmt"
	"github.com/cometbft/cometbft/rpc/client/http"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	dockerimagetypes "github.com/docker/docker/api/types/image"
	volumetypes "github.com/docker/docker/api/types/volume"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	sdkmath "cosmossdk.io/math"

	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types" // nolint:staticcheck
	chanTypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/chatton/interchaintest/v1/dockerutil"
	"github.com/chatton/interchaintest/v1/ibc"
	"github.com/chatton/interchaintest/v1/testutil"
)

// Chain is a local docker testnet for a Cosmos SDK chain.
// Implements the ibc.Chain interface.
type Chain struct {
	testName      string
	cfg           ibc.Config
	NumValidators int
	numFullNodes  int
	Validators    ChainNodes
	FullNodes     ChainNodes
	Provider      *Chain
	Consumers     []*Chain

	cdc      *codec.ProtoCodec
	log      *zap.Logger
	keyring  keyring.Keyring
	findTxMu sync.Mutex
}

func NewCosmosChain(testName string, chainConfig ibc.Config, numValidators int, numFullNodes int, log *zap.Logger) *Chain {
	if chainConfig.EncodingConfig == nil {
		panic("chain config must have an encoding config set")
	}

	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	kr := keyring.NewInMemory(cdc)

	return &Chain{
		testName:      testName,
		cfg:           chainConfig,
		NumValidators: numValidators,
		numFullNodes:  numFullNodes,
		log:           log,
		cdc:           cdc,
		keyring:       kr,
	}
}

// GetCodec returns the codec for the chain.
func (c *Chain) GetCodec() *codec.ProtoCodec {
	return c.cdc
}

// Nodes returns all nodes, including validators and fullnodes.
func (c *Chain) Nodes() ChainNodes {
	return append(c.Validators, c.FullNodes...)
}

// AddFullNodes adds new fullnodes to the network, peering with the existing nodes.
func (c *Chain) AddFullNodes(ctx context.Context, configFileOverrides map[string]any, inc int) error {
	// Get peer string for existing nodes
	peers := c.Nodes().PeerString(ctx)

	// Get genesis.json
	genbz, err := c.Validators[0].GenesisFileContent(ctx)
	if err != nil {
		return err
	}

	prevCount := c.numFullNodes
	c.numFullNodes += inc
	if err := c.initializeChainNodes(ctx, c.testName, c.GetFullNode().DockerClient, c.GetFullNode().NetworkID); err != nil {
		return err
	}

	var eg errgroup.Group
	for i := prevCount; i < c.numFullNodes; i++ {
		eg.Go(func() error {
			fn := c.FullNodes[i]
			if err := fn.InitFullNodeFiles(ctx); err != nil {
				return err
			}
			if err := fn.SetPeers(ctx, peers); err != nil {
				return err
			}
			if err := fn.OverwriteGenesisFile(ctx, genbz); err != nil {
				return err
			}
			for configFile, modifiedConfig := range configFileOverrides {
				modifiedToml, ok := modifiedConfig.(testutil.Toml)
				if !ok {
					return fmt.Errorf("provided toml override for file %s is of type (%T). Expected (DecodedToml)", configFile, modifiedConfig)
				}
				if err := testutil.ModifyTomlConfigFile(
					ctx,
					fn.logger(),
					fn.DockerClient,
					fn.TestName,
					fn.VolumeName,
					configFile,
					modifiedToml,
				); err != nil {
					return err
				}
			}
			if err := fn.CreateNodeContainer(ctx); err != nil {
				return err
			}
			return fn.StartContainer(ctx)
		})
	}
	return eg.Wait()
}

// Implements Chain interface.
func (c *Chain) Config() ibc.Config {
	return c.cfg
}

// Implements Chain interface.
func (c *Chain) Initialize(ctx context.Context, testName string, cli *client.Client, networkID string) error {
	return c.initializeChainNodes(ctx, testName, cli, networkID)
}

func (c *Chain) GetFullNode() *ChainNode {
	return c.GetNode()
}

func (c *Chain) GetNode() *ChainNode {
	return c.Validators[0]
}

// Exec implements ibc.Chain.
func (c *Chain) Exec(ctx context.Context, cmd []string, env []string) (stdout, stderr []byte, err error) {
	return c.GetFullNode().Exec(ctx, cmd, env)
}

// Implements Chain interface.
func (c *Chain) GetRPCAddress() string {
	return fmt.Sprintf("http://%s:26657", c.GetFullNode().HostName())
}

// Implements Chain interface.
func (c *Chain) GetAPIAddress() string {
	return fmt.Sprintf("http://%s:1317", c.GetFullNode().HostName())
}

// Implements Chain interface.
func (c *Chain) GetGRPCAddress() string {
	return fmt.Sprintf("%s:9090", c.GetFullNode().HostName())
}

// GetHostRPCAddress returns the address of the RPC server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *Chain) GetHostRPCAddress() string {
	return "http://" + c.GetFullNode().hostRPCPort
}

func (c *Chain) Client() (*http.HTTP, error) {
	return http.New(c.GetHostRPCAddress(), "/websocket")
}

// GetHostAPIAddress returns the address of the REST API server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *Chain) GetHostAPIAddress() string {
	return "http://" + c.GetFullNode().hostAPIPort
}

// GetHostGRPCAddress returns the address of the gRPC server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *Chain) GetHostGRPCAddress() string {
	return c.GetFullNode().hostGRPCPort
}

// GetHostP2PAddress returns the address of the P2P server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *Chain) GetHostPeerAddress() string {
	return c.GetFullNode().hostP2PPort
}

// HomeDir implements ibc.Chain.
func (c *Chain) HomeDir() string {
	return c.GetFullNode().HomeDir()
}

// HostHomeDir returns the path to the home dir on the host.
func (c *Chain) HostHomeDir() string {
	return c.Nodes()[0].VolumeName
}

// Implements Chain interface.
func (c *Chain) CreateKey(ctx context.Context, keyName string) error {
	return c.GetFullNode().CreateKey(ctx, keyName)
}

// Implements Chain interface.
func (c *Chain) RecoverKey(ctx context.Context, keyName, mnemonic string) error {
	return c.GetFullNode().RecoverKey(ctx, keyName, mnemonic)
}

// Implements Chain interface.
func (c *Chain) GetAddress(ctx context.Context, keyName string) ([]byte, error) {
	b32Addr, err := c.GetFullNode().AccountKeyBech32(ctx, keyName)
	if err != nil {
		return nil, err
	}

	return sdk.GetFromBech32(b32Addr, c.Config().Bech32Prefix)
}

// BuildWallet will return a Cosmos wallet
// If mnemonic != "", it will restore using that mnemonic
// If mnemonic == "", it will create a new key.
func (c *Chain) BuildWallet(ctx context.Context, keyName string, mnemonic string) (ibc.Wallet, error) {
	if mnemonic != "" {
		if err := c.RecoverKey(ctx, keyName, mnemonic); err != nil {
			return nil, fmt.Errorf("failed to recover key with name %q on chain %s: %w", keyName, c.cfg.Name, err)
		}
	} else {
		if err := c.CreateKey(ctx, keyName); err != nil {
			return nil, fmt.Errorf("failed to create key with name %q on chain %s: %w", keyName, c.cfg.Name, err)
		}
	}

	addrBytes, err := c.GetAddress(ctx, keyName)
	if err != nil {
		return nil, fmt.Errorf("failed to get account address for key %q on chain %s: %w", keyName, c.cfg.Name, err)
	}

	return NewWallet(keyName, addrBytes, mnemonic, c.cfg), nil
}

// BuildRelayerWallet will return a Cosmos wallet populated with the mnemonic so that the wallet can
// be restored in the relayer node using the mnemonic. After it is built, that address is included in
// genesis with some funds.
func (c *Chain) BuildRelayerWallet(ctx context.Context, keyName string) (ibc.Wallet, error) {
	coinType, err := strconv.ParseUint(c.cfg.CoinType, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid coin type: %w", err)
	}

	info, mnemonic, err := c.keyring.NewMnemonic(
		keyName,
		keyring.English,
		hd.CreateHDPath(uint32(coinType), 0, 0).String(),
		"", // Empty passphrase.
		hd.Secp256k1,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create mnemonic: %w", err)
	}

	addrBytes, err := info.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address: %w", err)
	}

	return NewWallet(keyName, addrBytes, mnemonic, c.cfg), nil
}

// Implements Chain interface.
func (c *Chain) SendFunds(ctx context.Context, keyName string, amount ibc.WalletAmount) error {
	return c.GetFullNode().BankSend(ctx, keyName, amount)
}

// Implements Chain interface.
func (c *Chain) SendFundsWithNote(ctx context.Context, keyName string, amount ibc.WalletAmount, note string) (string, error) {
	return c.GetFullNode().BankSendWithNote(ctx, keyName, amount, note)
}

// ExportState exports the chain state at specific height.
// Implements Chain interface.
func (c *Chain) ExportState(ctx context.Context, height int64) (string, error) {
	return c.GetFullNode().ExportState(ctx, height)
}

func (c *Chain) GetTransaction(txhash string) (*sdk.TxResponse, error) {
	fn := c.GetFullNode()
	return fn.GetTransaction(fn.CliContext(), txhash)
}

func (c *Chain) GetGasFeesInNativeDenom(gasPaid int64) int64 {
	gasPrice, _ := strconv.ParseFloat(strings.Replace(c.cfg.GasPrices, c.cfg.Denom, "", 1), 64)
	fees := float64(gasPaid) * gasPrice
	return int64(math.Ceil(fees))
}

func (c *Chain) UpgradeVersion(ctx context.Context, cli *client.Client, containerRepo, version string) {
	c.cfg.Images[0].Version = version
	for _, n := range c.Validators {
		n.Image.Version = version
		n.Image.Repository = containerRepo
	}
	for _, n := range c.FullNodes {
		n.Image.Version = version
		n.Image.Repository = containerRepo
	}
	c.pullImages(ctx, cli)
}

func (c *Chain) pullImages(ctx context.Context, cli *client.Client) {
	for _, image := range c.Config().Images {
		if image.Version == "local" {
			continue
		}
		rc, err := cli.ImagePull(
			ctx,
			image.Repository+":"+image.Version,
			dockerimagetypes.PullOptions{},
		)
		if err != nil {
			c.log.Error("Failed to pull image",
				zap.Error(err),
				zap.String("repository", image.Repository),
				zap.String("tag", image.Version),
			)
		} else {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
	}
}

// NewChainNode constructs a new cosmos chain node with a docker volume.
func (c *Chain) NewChainNode(
	ctx context.Context,
	testName string,
	cli *client.Client,
	networkID string,
	image ibc.DockerImage,
	validator bool,
	index int,
) (*ChainNode, error) {
	// Construct the ChainNode first so we can access its name.
	// The ChainNode's VolumeName cannot be set until after we create the volume.
	tn := NewChainNode(c.log, validator, c, cli, networkID, testName, image, index)

	v, err := cli.VolumeCreate(ctx, volumetypes.CreateOptions{
		Labels: map[string]string{
			dockerutil.CleanupLabel:   testName,
			dockerutil.NodeOwnerLabel: tn.Name(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating volume for chain node: %w", err)
	}
	tn.VolumeName = v.Name

	if err := dockerutil.SetVolumeOwner(ctx, dockerutil.VolumeOwnerOptions{
		Log:        c.log,
		Client:     cli,
		VolumeName: v.Name,
		ImageRef:   image.Ref(),
		TestName:   testName,
		UidGid:     image.UIDGID,
	}); err != nil {
		return nil, fmt.Errorf("set volume owner: %w", err)
	}

	return tn, nil
}

// creates the test node objects required for bootstrapping tests.
func (c *Chain) initializeChainNodes(
	ctx context.Context,
	testName string,
	cli *client.Client,
	networkID string,
) error {
	chainCfg := c.Config()
	c.pullImages(ctx, cli)
	image := chainCfg.Images[0]

	newVals := make(ChainNodes, c.NumValidators)
	copy(newVals, c.Validators)
	newFullNodes := make(ChainNodes, c.numFullNodes)
	copy(newFullNodes, c.FullNodes)

	eg, egCtx := errgroup.WithContext(ctx)
	for i := len(c.Validators); i < c.NumValidators; i++ {
		eg.Go(func() error {
			val, err := c.NewChainNode(egCtx, testName, cli, networkID, image, true, i)
			if err != nil {
				return err
			}
			newVals[i] = val
			return nil
		})
	}
	for i := len(c.FullNodes); i < c.numFullNodes; i++ {
		eg.Go(func() error {
			fn, err := c.NewChainNode(egCtx, testName, cli, networkID, image, false, i)
			if err != nil {
				return err
			}
			newFullNodes[i] = fn
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	c.findTxMu.Lock()
	defer c.findTxMu.Unlock()
	c.Validators = newVals
	c.FullNodes = newFullNodes
	return nil
}

type GenesisValidatorPubKey struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type GenesisValidators struct {
	Address string                 `json:"address"`
	Name    string                 `json:"name"`
	Power   string                 `json:"power"`
	PubKey  GenesisValidatorPubKey `json:"pub_key"`
}
type GenesisFile struct {
	Validators []GenesisValidators `json:"validators"`
}

type ValidatorWithIntPower struct {
	Address      string
	Power        int64
	PubKeyBase64 string
}

// Bootstraps the chain and starts it from genesis.
func (c *Chain) Start(ctx context.Context, additionalGenesisWallets ...ibc.WalletAmount) error {
	chainCfg := c.Config()

	genesisAmounts := make([][]sdk.Coin, len(c.Validators))
	genesisSelfDelegation := make([]sdk.Coin, len(c.Validators))

	for i := range c.Validators {
		genesisAmounts[i] = []sdk.Coin{{Amount: sdkmath.NewInt(10_000_000_000_000), Denom: chainCfg.Denom}}
		genesisSelfDelegation[i] = sdk.Coin{Amount: sdkmath.NewInt(5_000_000), Denom: chainCfg.Denom}
	}

	configFileOverrides := chainCfg.ConfigFileOverrides

	eg := new(errgroup.Group)
	// Initialize config and sign gentx for each validator.
	for i, v := range c.Validators {
		v.Validator = true
		eg.Go(func() error {
			if err := v.InitFullNodeFiles(ctx); err != nil {
				return err
			}
			for configFile, modifiedConfig := range configFileOverrides {
				modifiedToml, ok := modifiedConfig.(testutil.Toml)
				if !ok {
					return fmt.Errorf("provided toml override for file %s is of type (%T). Expected (DecodedToml)", configFile, modifiedConfig)
				}
				if err := testutil.ModifyTomlConfigFile(
					ctx,
					v.logger(),
					v.DockerClient,
					v.TestName,
					v.VolumeName,
					configFile,
					modifiedToml,
				); err != nil {
					return fmt.Errorf("failed to modify toml config file: %w", err)
				}
			}
			if !c.cfg.SkipGenTx {
				return v.InitValidatorGenTx(ctx, &chainCfg, genesisAmounts[i], genesisSelfDelegation[i])
			}
			return nil
		})
	}

	// Initialize config for each full node.
	for _, n := range c.FullNodes {
		n.Validator = false
		eg.Go(func() error {
			if err := n.InitFullNodeFiles(ctx); err != nil {
				return err
			}
			for configFile, modifiedConfig := range configFileOverrides {
				modifiedToml, ok := modifiedConfig.(testutil.Toml)
				if !ok {
					return fmt.Errorf("provided toml override for file %s is of type (%T). Expected (DecodedToml)", configFile, modifiedConfig)
				}
				if err := testutil.ModifyTomlConfigFile(
					ctx,
					n.logger(),
					n.DockerClient,
					n.TestName,
					n.VolumeName,
					configFile,
					modifiedToml,
				); err != nil {
					return err
				}
			}
			return nil
		})
	}

	// wait for this to finish
	if err := eg.Wait(); err != nil {
		return err
	}

	// for the validators we need to collect the gentxs and the accounts
	// to the first node's genesis file
	validator0 := c.Validators[0]
	for i := 1; i < len(c.Validators); i++ {
		validatorN := c.Validators[i]

		bech32, err := validatorN.AccountKeyBech32(ctx, valKey)
		if err != nil {
			return err
		}

		if err := validator0.AddGenesisAccount(ctx, bech32, genesisAmounts[0]); err != nil {
			return err
		}

		if !c.cfg.SkipGenTx {
			if err := validatorN.copyGentx(ctx, validator0); err != nil {
				return err
			}
		}
	}

	for _, wallet := range additionalGenesisWallets {
		if err := validator0.AddGenesisAccount(ctx, wallet.Address, []sdk.Coin{{Denom: wallet.Denom, Amount: wallet.Amount}}); err != nil {
			return err
		}
	}

	if !c.cfg.SkipGenTx {
		if err := validator0.CollectGentxs(ctx); err != nil {
			return err
		}
	}

	genbz, err := validator0.GenesisFileContent(ctx)
	if err != nil {
		return err
	}

	genbz = bytes.ReplaceAll(genbz, []byte(`"stake"`), []byte(fmt.Sprintf(`"%s"`, chainCfg.Denom)))

	if c.cfg.ModifyGenesis != nil {
		genbz, err = c.cfg.ModifyGenesis(chainCfg, genbz)
		if err != nil {
			return err
		}
	}

	// Provide EXPORT_GENESIS_FILE_PATH and EXPORT_GENESIS_CHAIN to help debug genesis file
	exportGenesis := os.Getenv("EXPORT_GENESIS_FILE_PATH")
	exportGenesisChain := os.Getenv("EXPORT_GENESIS_CHAIN")
	if exportGenesis != "" && exportGenesisChain == c.cfg.Name {
		c.log.Debug("Exporting genesis file",
			zap.String("chain", exportGenesisChain),
			zap.String("path", exportGenesis),
		)
		_ = os.WriteFile(exportGenesis, genbz, 0o600)
	}

	chainNodes := c.Nodes()

	for _, cn := range chainNodes {
		if err := cn.OverwriteGenesisFile(ctx, genbz); err != nil {
			return err
		}
	}

	if err := chainNodes.LogGenesisHashes(ctx); err != nil {
		return err
	}

	eg, egCtx := errgroup.WithContext(ctx)
	for _, n := range chainNodes {
		eg.Go(func() error {
			return n.CreateNodeContainer(egCtx)
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	peers := chainNodes.PeerString(ctx)

	eg, egCtx = errgroup.WithContext(ctx)
	for _, n := range chainNodes {
		c.log.Info("Starting container", zap.String("container", n.Name()))
		eg.Go(func() error {
			if err := n.SetPeers(egCtx, peers); err != nil {
				return err
			}
			return n.StartContainer(egCtx)
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	// Wait for blocks before considering the chains "started"
	return testutil.WaitForBlocks(ctx, 2, c.GetFullNode())
}

// Height implements ibc.Chain.
func (c *Chain) Height(ctx context.Context) (int64, error) {
	return c.GetFullNode().Height(ctx)
}

// Acknowledgements implements ibc.Chain, returning all acknowledgments in block at height.
func (c *Chain) Acknowledgements(ctx context.Context, height int64) ([]ibc.PacketAcknowledgement, error) {
	var acks []*chanTypes.MsgAcknowledgement
	err := RangeBlockMessages(ctx, c.cfg.EncodingConfig.InterfaceRegistry, c.GetFullNode().Client, height, func(msg sdk.Msg) bool {
		found, ok := msg.(*chanTypes.MsgAcknowledgement)
		if ok {
			acks = append(acks, found)
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("find acknowledgements at height %d: %w", height, err)
	}
	ibcAcks := make([]ibc.PacketAcknowledgement, len(acks))
	for i, ack := range acks {
		ibcAcks[i] = ibc.PacketAcknowledgement{
			Acknowledgement: ack.Acknowledgement,
			Packet: ibc.Packet{
				Sequence:         ack.Packet.Sequence,
				SourcePort:       ack.Packet.SourcePort,
				SourceChannel:    ack.Packet.SourceChannel,
				DestPort:         ack.Packet.DestinationPort,
				DestChannel:      ack.Packet.DestinationChannel,
				Data:             ack.Packet.Data,
				TimeoutHeight:    ack.Packet.TimeoutHeight.String(),
				TimeoutTimestamp: ibc.Nanoseconds(ack.Packet.TimeoutTimestamp),
			},
		}
	}
	return ibcAcks, nil
}

// Timeouts implements ibc.Chain, returning all timeouts in block at height.
func (c *Chain) Timeouts(ctx context.Context, height int64) ([]ibc.PacketTimeout, error) {
	var timeouts []*chanTypes.MsgTimeout
	err := RangeBlockMessages(ctx, c.cfg.EncodingConfig.InterfaceRegistry, c.GetFullNode().Client, height, func(msg sdk.Msg) bool {
		found, ok := msg.(*chanTypes.MsgTimeout)
		if ok {
			timeouts = append(timeouts, found)
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("find timeouts at height %d: %w", height, err)
	}
	ibcTimeouts := make([]ibc.PacketTimeout, len(timeouts))
	for i, ack := range timeouts {
		ibcTimeouts[i] = ibc.PacketTimeout{
			Packet: ibc.Packet{
				Sequence:         ack.Packet.Sequence,
				SourcePort:       ack.Packet.SourcePort,
				SourceChannel:    ack.Packet.SourceChannel,
				DestPort:         ack.Packet.DestinationPort,
				DestChannel:      ack.Packet.DestinationChannel,
				Data:             ack.Packet.Data,
				TimeoutHeight:    ack.Packet.TimeoutHeight.String(),
				TimeoutTimestamp: ibc.Nanoseconds(ack.Packet.TimeoutTimestamp),
			},
		}
	}
	return ibcTimeouts, nil
}

// StopAllNodes stops and removes all long running containers (validators and full nodes).
func (c *Chain) StopAllNodes(ctx context.Context) error {
	var eg errgroup.Group
	for _, n := range c.Nodes() {
		eg.Go(func() error {
			if err := n.StopContainer(ctx); err != nil {
				return err
			}
			return n.RemoveContainer(ctx)
		})
	}
	return eg.Wait()
}

// StartAllNodes creates and starts new containers for each node.
// Should only be used if the chain has previously been started with .Start.
func (c *Chain) StartAllNodes(ctx context.Context) error {
	// prevent client calls during this time
	c.findTxMu.Lock()
	defer c.findTxMu.Unlock()
	var eg errgroup.Group
	for _, n := range c.Nodes() {
		eg.Go(func() error {
			if err := n.CreateNodeContainer(ctx); err != nil {
				return err
			}
			return n.StartContainer(ctx)
		})
	}
	return eg.Wait()
}

func (c *Chain) VoteOnProposalAllValidators(ctx context.Context, proposalID uint64, vote string) error {
	var eg errgroup.Group
	for _, n := range c.Nodes() {
		if n.Validator {
			eg.Go(func() error {
				return n.VoteOnProposal(ctx, valKey, proposalID, vote)
			})
		}
	}
	return eg.Wait()
}

// GetTimeoutHeight returns a timeout height of 1000 blocks above the current block height.
// This function should be used when the timeout is never expected to be reached.
func (c *Chain) GetTimeoutHeight(ctx context.Context) (clienttypes.Height, error) {
	height, err := c.Height(ctx)
	if err != nil {
		c.log.Error("Failed to get chain height", zap.Error(err))
		return clienttypes.Height{}, fmt.Errorf("failed to get chain height: %w", err)
	}

	return clienttypes.NewHeight(clienttypes.ParseChainID(c.Config().ChainID), uint64(height)+1000), nil
}
