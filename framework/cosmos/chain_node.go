package cosmos

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/chatton/interchaintest/framework/types"
	"github.com/chatton/interchaintest/testutil/toml"
	"github.com/chatton/interchaintest/testutil/wait"
	"hash/fnv"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/docker/go-connections/nat"
	dockerclient "github.com/moby/moby/client"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authTx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	tmjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/p2p"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	libclient "github.com/cometbft/cometbft/rpc/jsonrpc/client"

	"github.com/chatton/interchaintest/dockerutil"
)

// ChainNode represents a node in the test network that is being created.
type ChainNode struct {
	VolumeName   string
	Index        int
	Chain        types.Chain
	Validator    bool
	NetworkID    string
	DockerClient *dockerclient.Client
	Client       rpcclient.Client
	GrpcConn     *grpc.ClientConn
	TestName     string
	Image        types.DockerImage
	preStartNode func(*ChainNode)

	lock sync.Mutex
	log  *zap.Logger

	containerLifecycle *dockerutil.ContainerLifecycle

	// Ports set during StartContainer.
	hostRPCPort   string
	hostAPIPort   string
	hostGRPCPort  string
	hostP2PPort   string
	cometHostname string
}

func NewDockerChainNode(log *zap.Logger, validator bool, chain *Chain, dockerClient *dockerclient.Client, networkID string, testName string, image types.DockerImage, index int) *ChainNode {
	tn := &ChainNode{
		log: log.With(
			zap.Bool("validator", validator),
			zap.Int("i", index),
		),
		Validator:    validator,
		Chain:        chain,
		DockerClient: dockerClient,
		NetworkID:    networkID,
		TestName:     testName,
		Image:        image,
		Index:        index,
	}

	tn.containerLifecycle = dockerutil.NewContainerLifecycle(log, dockerClient, tn.Name())

	return tn
}

// WithPreStartNode sets the preStartNode function for the ChainNode.
func (tn *ChainNode) WithPreStartNode(preStartNode func(*ChainNode)) *ChainNode {
	tn.preStartNode = preStartNode
	return tn
}

// ChainNodes is a collection of ChainNode.
type ChainNodes []*ChainNode

const (
	valKey      = "validator"
	blockTime   = 2 // seconds
	p2pPort     = "26656/tcp"
	rpcPort     = "26657/tcp"
	grpcPort    = "9090/tcp"
	apiPort     = "1317/tcp"
	privValPort = "1234/tcp"

	cometMockRawPort = "22331"
)

var sentryPorts = nat.PortMap{
	nat.Port(p2pPort):     {},
	nat.Port(rpcPort):     {},
	nat.Port(grpcPort):    {},
	nat.Port(apiPort):     {},
	nat.Port(privValPort): {},
}

// NewClient creates and assigns a new Tendermint RPC client to the ChainNode.
func (tn *ChainNode) NewClient(addr string) error {
	httpClient, err := libclient.DefaultHTTPClient(addr)
	if err != nil {
		return err
	}

	httpClient.Timeout = 10 * time.Second
	rpcClient, err := rpchttp.NewWithClient(addr, "/websocket", httpClient)
	if err != nil {
		return err
	}

	tn.Client = rpcClient

	grpcConn, err := grpc.NewClient(
		tn.hostGRPCPort, grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	tn.GrpcConn = grpcConn

	return nil
}

// CliContext creates a new Cosmos SDK client context.
func (tn *ChainNode) CliContext() client.Context {
	cfg := tn.Chain.Config()
	return client.Context{
		Client:            tn.Client,
		GRPCClient:        tn.GrpcConn,
		ChainID:           cfg.ChainID,
		InterfaceRegistry: cfg.EncodingConfig.InterfaceRegistry,
		Input:             os.Stdin,
		Output:            os.Stdout,
		OutputFormat:      "json",
		LegacyAmino:       cfg.EncodingConfig.Amino,
		TxConfig:          cfg.EncodingConfig.TxConfig,
	}
}

// Name of the test node container.
func (tn *ChainNode) Name() string {
	return fmt.Sprintf("%s-%s-%d-%s", tn.Chain.Config().ChainID, tn.NodeType(), tn.Index, dockerutil.SanitizeContainerName(tn.TestName))
}

func (tn *ChainNode) NodeType() string {
	nodeType := "fn"
	if tn.Validator {
		nodeType = "val"
	}
	return nodeType
}

func (tn *ChainNode) ContainerID() string {
	return tn.containerLifecycle.ContainerID()
}

// hostname of the test node container.
func (tn *ChainNode) HostName() string {
	return dockerutil.CondenseHostName(tn.Name())
}

// hostname of the comet mock container.
func (tn *ChainNode) HostnameCometMock() string {
	return tn.cometHostname
}

func (tn *ChainNode) GenesisFileContent(ctx context.Context) ([]byte, error) {
	gen, err := tn.ReadFile(ctx, "config/genesis.json")
	if err != nil {
		return nil, fmt.Errorf("getting genesis.json content: %w", err)
	}

	return gen, nil
}

func (tn *ChainNode) OverwriteGenesisFile(ctx context.Context, content []byte) error {
	err := tn.WriteFile(ctx, content, "config/genesis.json")
	if err != nil {
		return fmt.Errorf("overwriting genesis.json: %w", err)
	}

	return nil
}

func (tn *ChainNode) PrivValFileContent(ctx context.Context) ([]byte, error) {
	gen, err := tn.ReadFile(ctx, "config/priv_validator_key.json")
	if err != nil {
		return nil, fmt.Errorf("getting priv_validator_key.json content: %w", err)
	}

	return gen, nil
}

func (tn *ChainNode) OverwritePrivValFile(ctx context.Context, content []byte) error {
	fw := dockerutil.NewFileWriter(tn.logger(), tn.DockerClient, tn.TestName)
	if err := fw.WriteFile(ctx, tn.VolumeName, "config/priv_validator_key.json", content); err != nil {
		return fmt.Errorf("overwriting priv_validator_key.json: %w", err)
	}

	return nil
}

func (tn *ChainNode) copyGentx(ctx context.Context, destVal *ChainNode) error {
	nid, err := tn.NodeID(ctx)
	if err != nil {
		return fmt.Errorf("getting node ID: %w", err)
	}

	relPath := fmt.Sprintf("config/gentx/gentx-%s.json", nid)

	gentx, err := tn.ReadFile(ctx, relPath)
	if err != nil {
		return fmt.Errorf("getting gentx content: %w", err)
	}

	err = destVal.WriteFile(ctx, gentx, relPath)
	if err != nil {
		return fmt.Errorf("overwriting gentx: %w", err)
	}

	return nil
}

type PrivValidatorKey struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type PrivValidatorKeyFile struct {
	Address string           `json:"address"`
	PubKey  PrivValidatorKey `json:"pub_key"`
	PrivKey PrivValidatorKey `json:"priv_key"`
}

// Bind returns the home folder bind point for running the node.
func (tn *ChainNode) Bind() []string {
	return []string{fmt.Sprintf("%s:%s", tn.VolumeName, tn.HomeDir())}
}

func (tn *ChainNode) HomeDir() string {
	return path.Join("/var/cosmos-chain", tn.Chain.Config().Name)
}

// SetTestConfig modifies the config to reasonable values for use within interchaintest.
func (tn *ChainNode) SetTestConfig(ctx context.Context) error {
	c := make(toml.Toml)

	// Set Log Level to info
	c["log_level"] = "info"

	p2p := make(toml.Toml)

	// Allow p2p strangeness
	p2p["allow_duplicate_ip"] = true
	p2p["addr_book_strict"] = false

	c["p2p"] = p2p

	consensus := make(toml.Toml)

	blockT := (time.Duration(blockTime) * time.Second).String()
	consensus["timeout_commit"] = blockT
	consensus["timeout_propose"] = blockT

	c["consensus"] = consensus

	rpc := make(toml.Toml)

	// Enable public RPC
	rpc["laddr"] = "tcp://0.0.0.0:26657"
	rpc["allowed_origins"] = []string{"*"}
	c["rpc"] = rpc

	if err := toml.ModifyConfigFile(
		ctx,
		tn.logger(),
		tn.DockerClient,
		tn.TestName,
		tn.VolumeName,
		"config/config.toml",
		c,
	); err != nil {
		return err
	}

	a := make(toml.Toml)
	a["minimum-gas-prices"] = tn.Chain.Config().GasPrices

	grpc := make(toml.Toml)

	// Enable public GRPC
	grpc["address"] = "0.0.0.0:9090"

	a["grpc"] = grpc

	api := make(toml.Toml)

	// Enable public REST API
	api["enable"] = true
	api["swagger"] = true
	api["address"] = "tcp://0.0.0.0:1317"

	a["api"] = api

	return toml.ModifyConfigFile(
		ctx,
		tn.logger(),
		tn.DockerClient,
		tn.TestName,
		tn.VolumeName,
		"config/app.toml",
		a,
	)
}

// SetPeers modifies the config persistent_peers for a node.
func (tn *ChainNode) SetPeers(ctx context.Context, peers string) error {
	c := make(toml.Toml)
	p2p := make(toml.Toml)

	// Set peers
	p2p["persistent_peers"] = peers
	c["p2p"] = p2p

	return toml.ModifyConfigFile(
		ctx,
		tn.logger(),
		tn.DockerClient,
		tn.TestName,
		tn.VolumeName,
		"config/config.toml",
		c,
	)
}

func (tn *ChainNode) Height(ctx context.Context) (int64, error) {
	res, err := tn.Client.Status(ctx)
	if err != nil {
		return 0, fmt.Errorf("tendermint rpc client status: %w", err)
	}
	height := res.SyncInfo.LatestBlockHeight
	return height, nil
}

// TxCommand is a helper to retrieve a full command for broadcasting a tx
// with the chain node binary.
func (tn *ChainNode) TxCommand(keyName string, command ...string) []string {
	command = append([]string{"tx"}, command...)
	gasPriceFound, gasAdjustmentFound, gasFound, feesFound := false, false, false, false
	for i := 0; i < len(command); i++ {
		if command[i] == "--gas-prices" {
			gasPriceFound = true
		}
		if command[i] == "--gas-adjustment" {
			gasAdjustmentFound = true
		}
		if command[i] == "--fees" {
			feesFound = true
		}
		if command[i] == "--gas" {
			gasFound = true
		}
	}
	if !gasPriceFound && !feesFound {
		command = append(command, "--gas-prices", tn.Chain.Config().GasPrices)
	}
	if !gasAdjustmentFound {
		command = append(command, "--gas-adjustment", strconv.FormatFloat(tn.Chain.Config().GasAdjustment, 'f', -1, 64))
	}
	if !gasFound && !feesFound && tn.Chain.Config().Gas != "" {
		command = append(command, "--gas", tn.Chain.Config().Gas)
	}
	return tn.NodeCommand(append(command,
		"--from", keyName,
		"--keyring-backend", keyring.BackendTest,
		"--output", "json",
		"-y",
		"--chain-id", tn.Chain.Config().ChainID,
	)...)
}

// ExecTx executes a transaction, waits for 2 blocks if successful, then returns the tx hash.
func (tn *ChainNode) ExecTx(ctx context.Context, keyName string, command ...string) (string, error) {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	stdout, _, err := tn.Exec(ctx, tn.TxCommand(keyName, command...), tn.Chain.Config().Env)
	if err != nil {
		return "", err
	}
	output := CosmosTx{}
	err = json.Unmarshal(stdout, &output)
	if err != nil {
		return "", err
	}
	if output.Code != 0 {
		return output.TxHash, fmt.Errorf("transaction failed with code %d: %s", output.Code, output.RawLog)
	}
	if err := wait.ForBlocks(ctx, 2, tn); err != nil {
		return "", err
	}
	// The transaction can at first appear to succeed, but then fail when it's actually included in a block.
	stdout, _, err = tn.ExecQuery(ctx, "tx", output.TxHash)
	if err != nil {
		return "", err
	}
	output = CosmosTx{}
	err = json.Unmarshal(stdout, &output)
	if err != nil {
		return "", err
	}
	if output.Code != 0 {
		return output.TxHash, fmt.Errorf("transaction failed with code %d: %s", output.Code, output.RawLog)
	}
	return output.TxHash, nil
}

// TxHashToResponse returns the sdk transaction response struct for a given transaction hash.
func (tn *ChainNode) TxHashToResponse(ctx context.Context, txHash string) (*sdk.TxResponse, error) {
	stdout, stderr, err := tn.ExecQuery(ctx, "tx", txHash)
	if err != nil {
		tn.log.Info("TxHashToResponse returned an error",
			zap.String("tx_hash", txHash),
			zap.Error(err),
			zap.String("stderr", string(stderr)),
		)
	}

	i := &sdk.TxResponse{}

	// ignore the error since some types do not unmarshal (ex: height of int64 vs string)
	_ = json.Unmarshal(stdout, &i)
	return i, nil
}

// NodeCommand is a helper to retrieve a full command for a chain node binary.
// when interactions with the RPC endpoint are necessary.
// For example, if chain node binary is `gaiad`, and desired command is `gaiad keys show key1`,
// pass ("keys", "show", "key1") for command to return the full command.
// Will include additional flags for node URL, home directory, and chain ID.
func (tn *ChainNode) NodeCommand(command ...string) []string {
	command = tn.BinCommand(command...)

	endpoint := fmt.Sprintf("tcp://%s:26657", tn.HostName())

	return append(command,
		"--node", endpoint,
	)
}

// BinCommand is a helper to retrieve a full command for a chain node binary.
// For example, if chain node binary is `gaiad`, and desired command is `gaiad keys show key1`,
// pass ("keys", "show", "key1") for command to return the full command.
// Will include additional flags for home directory and chain ID.
func (tn *ChainNode) BinCommand(command ...string) []string {
	command = append([]string{tn.Chain.Config().Bin}, command...)
	return append(command,
		"--home", tn.HomeDir(),
	)
}

// ExecBin is a helper to execute a command for a chain node binary.
// For example, if chain node binary is `gaiad`, and desired command is `gaiad keys show key1`,
// pass ("keys", "show", "key1") for command to execute the command against the node.
// Will include additional flags for home directory and chain ID.
func (tn *ChainNode) ExecBin(ctx context.Context, command ...string) ([]byte, []byte, error) {
	return tn.Exec(ctx, tn.BinCommand(command...), tn.Chain.Config().Env)
}

// QueryCommand is a helper to retrieve the full query command. For example,
// if chain node binary is gaiad, and desired command is `gaiad query gov params`,
// pass ("gov", "params") for command to return the full command with all necessary
// flags to query the specific node.
func (tn *ChainNode) QueryCommand(command ...string) []string {
	command = append([]string{"query"}, command...)
	return tn.NodeCommand(append(command,
		"--output", "json",
	)...)
}

// ExecQuery is a helper to execute a query command. For example,
// if chain node binary is gaiad, and desired command is `gaiad query gov params`,
// pass ("gov", "params") for command to execute the query against the node.
// Returns response in json format.
func (tn *ChainNode) ExecQuery(ctx context.Context, command ...string) ([]byte, []byte, error) {
	return tn.Exec(ctx, tn.QueryCommand(command...), tn.Chain.Config().Env)
}

// CondenseMoniker fits a moniker into the cosmos character limit for monikers.
// If the moniker already fits, it is returned unmodified.
// Otherwise, the middle is truncated, and a hash is appended to the end
// in case the only unique data was in the middle.
func CondenseMoniker(m string) string {
	if len(m) <= stakingtypes.MaxMonikerLength {
		return m
	}

	// Get the hash suffix, a 32-bit uint formatted in base36.
	// fnv32 was chosen because a 32-bit number ought to be sufficient
	// as a distinguishing suffix, and it will be short enough so that
	// less of the middle will be truncated to fit in the character limit.
	// It's also non-cryptographic, not that this function will ever be a bottleneck in tests.
	h := fnv.New32()
	h.Write([]byte(m))
	suffix := "-" + strconv.FormatUint(uint64(h.Sum32()), 36)

	wantLen := stakingtypes.MaxMonikerLength - len(suffix)

	// Half of the want length, minus 2 to account for half of the ... we add in the middle.
	keepLen := (wantLen / 2) - 2

	return m[:keepLen] + "..." + m[len(m)-keepLen:] + suffix
}

// InitHomeFolder initializes a home folder for the given node.
func (tn *ChainNode) InitHomeFolder(ctx context.Context) error {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	_, _, err := tn.ExecBin(ctx,
		"init", CondenseMoniker(tn.Name()),
		"--chain-id", tn.Chain.Config().ChainID,
	)
	return err
}

// WriteFile accepts file contents in a byte slice and writes the contents to
// the docker filesystem. relPath describes the location of the file in the
// docker volume relative to the home directory.
func (tn *ChainNode) WriteFile(ctx context.Context, content []byte, relPath string) error {
	fw := dockerutil.NewFileWriter(tn.logger(), tn.DockerClient, tn.TestName)
	return fw.WriteFile(ctx, tn.VolumeName, relPath, content)
}

// CopyFile adds a file from the host filesystem to the docker filesystem
// relPath describes the location of the file in the docker volume relative to
// the home directory.
func (tn *ChainNode) CopyFile(ctx context.Context, srcPath, dstPath string) error {
	content, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return tn.WriteFile(ctx, content, dstPath)
}

// ReadFile reads the contents of a single file at the specified path in the docker filesystem.
// relPath describes the location of the file in the docker volume relative to the home directory.
func (tn *ChainNode) ReadFile(ctx context.Context, relPath string) ([]byte, error) {
	fr := dockerutil.NewFileRetriever(tn.logger(), tn.DockerClient, tn.TestName)
	gen, err := fr.SingleFileContent(ctx, tn.VolumeName, relPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file at %s: %w", relPath, err)
	}
	return gen, nil
}

// CreateKey creates a key in the keyring backend test for the given node.
func (tn *ChainNode) CreateKey(ctx context.Context, name string) error {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	_, _, err := tn.ExecBin(ctx,
		"keys", "add", name,
		"--coin-type", tn.Chain.Config().CoinType,
		"--keyring-backend", keyring.BackendTest,
	)
	return err
}

// RecoverKey restores a key from a given mnemonic.
func (tn *ChainNode) RecoverKey(ctx context.Context, keyName, mnemonic string) error {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	command := []string{
		"sh",
		"-c",
		fmt.Sprintf(`echo %q | %s keys add %s --recover --keyring-backend %s --coin-type %s --home %s --output json`, mnemonic, tn.Chain.Config().Bin, keyName, keyring.BackendTest, tn.Chain.Config().CoinType, tn.HomeDir()),
	}

	_, _, err := tn.Exec(ctx, command, tn.Chain.Config().Env)
	return err
}

// AddGenesisAccount adds a genesis account for each key.
func (tn *ChainNode) AddGenesisAccount(ctx context.Context, address string, genesisAmount []sdk.Coin) error {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	amount := ""
	for i, coin := range genesisAmount {
		if i != 0 {
			amount += ","
		}
		amount += fmt.Sprintf("%s%s", coin.Amount.String(), coin.Denom)
	}

	// Adding a genesis account should complete instantly,
	// so use a 1-minute timeout to more quickly detect if Docker has locked up.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	command := []string{"genesis", "add-genesis-account", address, amount}
	_, _, err := tn.ExecBin(ctx, command...)

	return err
}

// Gentx generates the gentx for a given node.
func (tn *ChainNode) Gentx(ctx context.Context, name string, genesisSelfDelegation sdk.Coin) error {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	command := []string{"genesis"}
	command = append(command, "gentx", valKey, fmt.Sprintf("%s%s", genesisSelfDelegation.Amount.String(), genesisSelfDelegation.Denom),
		"--gas-prices", tn.Chain.Config().GasPrices,
		"--gas-adjustment", fmt.Sprint(tn.Chain.Config().GasAdjustment),
		"--keyring-backend", keyring.BackendTest,
		"--chain-id", tn.Chain.Config().ChainID,
	)

	_, _, err := tn.ExecBin(ctx, command...)
	return err
}

// CollectGentxs runs collect gentxs on the node's home folders.
func (tn *ChainNode) CollectGentxs(ctx context.Context) error {
	command := []string{tn.Chain.Config().Bin}
	command = append(command, "genesis")
	command = append(command, "collect-gentxs", "--home", tn.HomeDir())

	tn.lock.Lock()
	defer tn.lock.Unlock()

	_, _, err := tn.Exec(ctx, command, tn.Chain.Config().Env)
	return err
}

type CosmosTx struct {
	TxHash string `json:"txhash"`
	Code   int    `json:"code"`
	RawLog string `json:"raw_log"`
}

func (tn *ChainNode) GetTransaction(clientCtx client.Context, txHash string) (*sdk.TxResponse, error) {
	// Retry because sometimes the tx is not committed to state yet.
	var txResp *sdk.TxResponse
	err := retry.Do(func() error {
		var err error
		txResp, err = authTx.QueryTx(clientCtx, txHash)
		return err
	},
		// retry for total of 3 seconds
		retry.Attempts(15),
		retry.Delay(200*time.Millisecond),
		retry.DelayType(retry.FixedDelay),
		retry.LastErrorOnly(true),
	)
	return txResp, err
}

func (tn *ChainNode) ExportState(ctx context.Context, height int64) (string, error) {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	var (
		doc     = "state_export.json"
		docPath = path.Join(tn.HomeDir(), doc)
		command = []string{"export", "--height", fmt.Sprint(height), "--home", tn.HomeDir(), "--output-document", docPath}
	)

	_, _, err := tn.ExecBin(ctx, command...)
	if err != nil {
		return "", err
	}

	content, err := tn.ReadFile(ctx, doc)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (tn *ChainNode) UnsafeResetAll(ctx context.Context) error {
	tn.lock.Lock()
	defer tn.lock.Unlock()

	command := []string{tn.Chain.Config().Bin, "comet", "unsafe-reset-all", "--home", tn.HomeDir()}
	_, _, err := tn.Exec(ctx, command, tn.Chain.Config().Env)
	return err
}

func (tn *ChainNode) CreateNodeContainer(ctx context.Context) error {
	chainCfg := tn.Chain.Config()

	var cmd []string
	if chainCfg.NoHostMount {
		startCmd := fmt.Sprintf("cp -r %s %s_nomnt && %s start --home %s_nomnt", tn.HomeDir(), tn.HomeDir(), chainCfg.Bin, tn.HomeDir())
		if len(chainCfg.AdditionalStartArgs) > 0 {
			startCmd = fmt.Sprintf("%s %s", startCmd, chainCfg.AdditionalStartArgs)
		}
		cmd = []string{"sh", "-c", startCmd}
	} else {
		cmd = []string{chainCfg.Bin, "start", "--home", tn.HomeDir()}
		if len(chainCfg.AdditionalStartArgs) > 0 {
			cmd = append(cmd, chainCfg.AdditionalStartArgs...)
		}
	}

	usingPorts := nat.PortMap{}
	for k, v := range sentryPorts {
		usingPorts[k] = v
	}
	for _, port := range chainCfg.ExposeAdditionalPorts {
		usingPorts[nat.Port(port)] = []nat.PortBinding{}
	}

	// to prevent port binding conflicts, host port overrides are only exposed on the first validator node.
	if tn.Validator && tn.Index == 0 && chainCfg.HostPortOverride != nil {
		var fields []zap.Field

		i := 0
		for intP, extP := range chainCfg.HostPortOverride {
			port := nat.Port(fmt.Sprintf("%d/tcp", intP))

			usingPorts[port] = []nat.PortBinding{
				{
					HostPort: fmt.Sprintf("%d", extP),
				},
			}

			fields = append(fields, zap.String(fmt.Sprintf("port_overrides_%d", i), fmt.Sprintf("%s:%d", port, extP)))
			i++
		}

		tn.log.Info("Port overrides", fields...)
	}

	return tn.containerLifecycle.CreateContainer(ctx, tn.TestName, tn.NetworkID, tn.Image, usingPorts, "", tn.Bind(), nil, tn.HostName(), cmd, chainCfg.Env, []string{})
}

func (tn *ChainNode) StartContainer(ctx context.Context) error {
	if err := tn.containerLifecycle.StartContainer(ctx); err != nil {
		return err
	}

	// Set the host ports once since they will not change after the container has started.
	hostPorts, err := tn.containerLifecycle.GetHostPorts(ctx, rpcPort, grpcPort, apiPort, p2pPort)
	if err != nil {
		return err
	}
	tn.hostRPCPort, tn.hostGRPCPort, tn.hostAPIPort, tn.hostP2PPort = hostPorts[0], hostPorts[1], hostPorts[2], hostPorts[3]

	err = tn.NewClient("tcp://" + tn.hostRPCPort)
	if err != nil {
		return err
	}

	time.Sleep(5 * time.Second)
	return retry.Do(func() error {
		stat, err := tn.Client.Status(ctx)
		if err != nil {
			return err
		}
		// TODO: re-enable this check, having trouble with it for some reason
		if stat != nil && stat.SyncInfo.CatchingUp {
			return fmt.Errorf("still catching up: height(%d) catching-up(%t)",
				stat.SyncInfo.LatestBlockHeight, stat.SyncInfo.CatchingUp)
		}
		return nil
	}, retry.Context(ctx), retry.Attempts(40), retry.Delay(3*time.Second), retry.DelayType(retry.FixedDelay))
}

func (tn *ChainNode) RemoveContainer(ctx context.Context) error {
	return tn.containerLifecycle.RemoveContainer(ctx)
}

// InitValidatorGenTx creates the node files and signs a genesis transaction.
func (tn *ChainNode) InitValidatorGenTx(
	ctx context.Context,
	genesisAmounts []sdk.Coin,
	genesisSelfDelegation sdk.Coin,
) error {
	if err := tn.CreateKey(ctx, valKey); err != nil {
		return err
	}
	bech32, err := tn.AccountKeyBech32(ctx, valKey)
	if err != nil {
		return err
	}
	if err := tn.AddGenesisAccount(ctx, bech32, genesisAmounts); err != nil {
		return err
	}
	return tn.Gentx(ctx, valKey, genesisSelfDelegation)
}

func (tn *ChainNode) InitFullNodeFiles(ctx context.Context) error {
	if err := tn.InitHomeFolder(ctx); err != nil {
		return err
	}

	return tn.SetTestConfig(ctx)
}

// NodeID returns the persistent ID of a given node.
func (tn *ChainNode) NodeID(ctx context.Context) (string, error) {
	// This used to call p2p.LoadNodeKey against the file on the host,
	// but because we are transitioning to operating on Docker volumes,
	// we only have to tmjson.Unmarshal the raw content.
	j, err := tn.ReadFile(ctx, "config/node_key.json")
	if err != nil {
		return "", fmt.Errorf("getting node_key.json content: %w", err)
	}

	var nk p2p.NodeKey
	if err := tmjson.Unmarshal(j, &nk); err != nil {
		return "", fmt.Errorf("unmarshaling node_key.json: %w", err)
	}

	return string(nk.ID()), nil
}

// KeyBech32 retrieves the named key's address in bech32 format from the node.
// bech is the bech32 prefix (acc|val|cons). If empty, defaults to the account key (same as "acc").
func (tn *ChainNode) KeyBech32(ctx context.Context, name string, bech string) (string, error) {
	command := []string{
		tn.Chain.Config().Bin, "keys", "show", "--address", name,
		"--home", tn.HomeDir(),
		"--keyring-backend", keyring.BackendTest,
	}

	if bech != "" {
		command = append(command, "--bech", bech)
	}

	stdout, stderr, err := tn.Exec(ctx, command, tn.Chain.Config().Env)
	if err != nil {
		return "", fmt.Errorf("failed to show key %q (stderr=%q): %w", name, stderr, err)
	}

	return string(bytes.TrimSuffix(stdout, []byte("\n"))), nil
}

// AccountKeyBech32 retrieves the named key's address in bech32 account format.
func (tn *ChainNode) AccountKeyBech32(ctx context.Context, name string) (string, error) {
	return tn.KeyBech32(ctx, name, "")
}

// PeerString returns the string for connecting the nodes passed in.
func (nodes ChainNodes) PeerString(ctx context.Context) string {
	return nodes.hostString(ctx, "Peer", 26656)
}

// RPCString returns the string for connecting the nodes passed in.
func (nodes ChainNodes) RPCString(ctx context.Context) string {
	return nodes.hostString(ctx, "RPC", 26657)
}

// hostString generates a comma-separated string of node addresses in the format "id@hostname:port" for the given nodes.
func (nodes ChainNodes) hostString(ctx context.Context, msg string, port int) string {
	addrs := make([]string, len(nodes))
	for i, n := range nodes {
		id, err := n.NodeID(ctx)
		if err != nil {
			// TODO: would this be better to panic?
			// When would NodeId return an error?
			break
		}
		hostName := n.HostName()
		addr := fmt.Sprintf("%s@%s:%d", id, hostName, port)
		nodes.logger().Info(msg,
			zap.String("host_name", hostName),
			zap.String("address", addr),
			zap.String("container", n.Name()),
		)
		addrs[i] = addr
	}
	return strings.Join(addrs, ",")
}

// LogGenesisHashes logs the genesis hashes for the various nodes.
func (nodes ChainNodes) LogGenesisHashes(ctx context.Context) error {
	for _, n := range nodes {
		gen, err := n.GenesisFileContent(ctx)
		if err != nil {
			return err
		}

		n.logger().Info("Genesis", zap.String("hash", fmt.Sprintf("%X", sha256.Sum256(gen))))
	}
	return nil
}

func (nodes ChainNodes) logger() *zap.Logger {
	if len(nodes) == 0 {
		return zap.NewNop()
	}
	return nodes[0].logger()
}

func (tn *ChainNode) Exec(ctx context.Context, cmd []string, env []string) ([]byte, []byte, error) {
	job := dockerutil.NewImage(tn.logger(), tn.DockerClient, tn.NetworkID, tn.TestName, tn.Image.Repository, tn.Image.Version)
	opts := dockerutil.ContainerOptions{
		Env:   env,
		Binds: tn.Bind(),
	}
	res := job.Run(ctx, cmd, opts)
	return res.Stdout, res.Stderr, res.Err
}

func (tn *ChainNode) logger() *zap.Logger {
	return tn.log.With(
		zap.String("chain_id", tn.Chain.Config().ChainID),
		zap.String("test", tn.TestName),
	)
}

// GetHostAddress returns the host-accessible url for a port in the container.
// This is useful for finding the url & random host port for ports exposed via Config.ExposeAdditionalPorts.
func (tn *ChainNode) GetHostAddress(ctx context.Context, portID string) (string, error) {
	ports, err := tn.containerLifecycle.GetHostPorts(ctx, portID)
	if err != nil {
		return "", err
	}
	if len(ports) == 0 || ports[0] == "" {
		return "", fmt.Errorf("no port with id '%s' found", portID)
	}
	return "http://" + ports[0], nil
}
