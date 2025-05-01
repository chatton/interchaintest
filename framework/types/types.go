package types

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	dockerimagetypes "github.com/docker/docker/api/types/image"
	"github.com/moby/moby/client"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/types/module/testutil"
)

// ChainSpec is a wrapper around a types.Config
// that allows callers to easily reference one of the built-in chain configs
// and optionally provide overrides for some settings.
type ChainSpec struct {
	// Name is the name of the built-in config to use as a basis for this chain spec.
	// Required unless every other field is set.
	Name string

	// ChainName sets the Name of the embedded ibc.Config, i.e. the name of the chain.
	ChainName string

	// Version of the docker image to use.
	// Must be set.
	Version string

	// NoHostMount is a pointers in ChainSpec
	// so zero-overrides can be detected from omitted overrides.
	NoHostMount *bool

	// Embedded chain config Config.
	Config

	// How many validators and how many full nodes to use
	// when instantiating the chain.
	// If unspecified, NumValidators defaults to 2 and NumFullNodes defaults to 1.
	NumValidators, NumFullNodes *int
}

// Config defines the chain parameters requires to run an interchaintest testnet for a chain.
type Config struct {
	// Chain type, e.g. cosmos.
	Type string `yaml:"type"`
	// Chain name, e.g. cosmoshub.
	Name string `yaml:"name"`
	// Chain ID, e.g. cosmoshub-4
	ChainID string `yaml:"chain-id"`
	// Docker images required for running chain nodes.
	Images []DockerImage `yaml:"images"`
	// Binary to execute for the chain node daemon.
	Bin string `yaml:"bin"`
	// Bech32 prefix for chain addresses, e.g. cosmos.
	Bech32Prefix string `yaml:"bech32-prefix"`
	// Denomination of native currency, e.g. uatom.
	Denom string `yaml:"denom"`
	// Coin type
	CoinType string `default:"118" yaml:"coin-type"`
	// Minimum gas prices for sending transactions, in native currency denom.
	GasPrices string `yaml:"gas-prices"`
	// Adjustment multiplier for gas fees.
	GasAdjustment float64 `yaml:"gas-adjustment"`
	// Default gas limit for transactions. May be empty, "auto", or a number.
	Gas string `yaml:"gas" default:"auto"`
	// Trusting period of the chain.
	TrustingPeriod string `yaml:"trusting-period"`
	// Do not use docker host mount.
	NoHostMount bool `yaml:"no-host-mount"`
	// When true, will skip validator gentx flow
	SkipGenTx bool
	// When provided, genesis file contents will be altered before sharing for genesis.
	ModifyGenesis func(Config, []byte) ([]byte, error)
	// Override config parameters for files at filepath.
	ConfigFileOverrides map[string]any
	// Non-nil will override the encoding config, used for cosmos chains only.
	EncodingConfig *testutil.TestEncodingConfig
	// To avoid port binding conflicts, ports are only exposed on the 0th validator.
	HostPortOverride map[int]int `yaml:"host-port-override"`
	// ExposeAdditionalPorts exposes each port id to the host on a random port. ex: "8080/tcp"
	// Access the address with ChainNode.GetHostAddress
	ExposeAdditionalPorts []string
	// Additional start command arguments
	AdditionalStartArgs []string
	// Environment variables for chain nodes
	Env []string
}

func (c Config) Clone() Config {
	x := c

	images := make([]DockerImage, len(c.Images))
	copy(images, c.Images)
	x.Images = images

	additionalPorts := make([]string, len(c.ExposeAdditionalPorts))
	copy(additionalPorts, c.ExposeAdditionalPorts)
	x.ExposeAdditionalPorts = additionalPorts

	return x
}

func (c Config) VerifyCoinType() (string, error) {
	// If coin-type is left blank in the Config,
	// the Cosmos SDK default of 118 is used.
	if c.CoinType == "" {
		typ := reflect.TypeOf(c)
		f, _ := typ.FieldByName("CoinType")
		coinType := f.Tag.Get("default")
		_, err := strconv.ParseUint(coinType, 10, 32)
		if err != nil {
			return "", err
		}
		return coinType, nil
	} else {
		_, err := strconv.ParseUint(c.CoinType, 10, 32)
		if err != nil {
			return "", err
		}
		return c.CoinType, nil
	}
}

type DockerImage struct {
	Repository string `json:"repository" yaml:"repository"`
	Version    string `json:"version" yaml:"version"`
	UIDGID     string `json:"uid-gid" yaml:"uid-gid"`
}

func NewDockerImage(repository, version, uidGID string) DockerImage {
	return DockerImage{
		Repository: repository,
		Version:    version,
		UIDGID:     uidGID,
	}
}

// IsFullyConfigured reports whether all of i's required fields are present.
// Version is not required, as it can be superseded by a ChainSpec version.
func (i DockerImage) IsFullyConfigured() bool {
	return i.Validate() == nil
}

// Validate returns an error describing which of i's required fields are missing
// and returns nil if all required fields are present. Version is not required,
// as it can be superseded by a ChainSpec version.
func (i DockerImage) Validate() error {
	var missing []string

	if i.Repository == "" {
		missing = append(missing, "Repository")
	}
	if i.UIDGID == "" {
		missing = append(missing, "UidGid")
	}

	if len(missing) > 0 {
		fields := strings.Join(missing, ", ")
		return fmt.Errorf("DockerImage is missing fields: %s", fields)
	}

	return nil
}

// Ref returns the reference to use when e.g. creating a container.
func (i DockerImage) Ref() string {
	if i.Version == "" {
		return i.Repository + ":latest"
	}

	return i.Repository + ":" + i.Version
}

func (i DockerImage) PullImage(ctx context.Context, client *client.Client) error {
	ref := i.Ref()
	_, _, err := client.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		rc, err := client.ImagePull(ctx, ref, dockerimagetypes.PullOptions{})
		if err != nil {
			return fmt.Errorf("pull image %s: %w", ref, err)
		}
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}
	return nil
}

type WalletAmount struct {
	Address string
	Denom   string
	Amount  math.Int
}

type IBCTimeout struct {
	NanoSeconds uint64
	Height      int64
}

type ChannelCounterparty struct {
	PortID    string `json:"port_id"`
	ChannelID string `json:"channel_id"`
}

type ChannelOutput struct {
	State          string              `json:"state"`
	Ordering       string              `json:"ordering"`
	Counterparty   ChannelCounterparty `json:"counterparty"`
	ConnectionHops []string            `json:"connection_hops"`
	Version        string              `json:"version"`
	PortID         string              `json:"port_id"`
	ChannelID      string              `json:"channel_id"`
}

type ClientOutput struct {
	ClientID    string      `json:"client_id"`
	ClientState ClientState `json:"client_state"`
}

type ClientState struct {
	ChainID string `json:"chain_id"`
}

type ClientOutputs []*ClientOutput

type Wallet interface {
	KeyName() string
	FormattedAddress() string
	Mnemonic() string
	Address() []byte
}

type RelayerImplementation int64

const (
	CosmosRly RelayerImplementation = iota
	Hermes
	Hyperspace
)

// ChannelFilter provides the means for either creating an allowlist or a denylist of channels on the src chain
// which will be used to narrow down the list of channels a user wants to relay on.
type ChannelFilter struct {
	Rule        string
	ChannelList []string
}

type PathUpdateOptions struct {
	ChannelFilter *ChannelFilter
	SrcClientID   *string
	SrcConnID     *string
	SrcChainID    *string
	DstClientID   *string
	DstConnID     *string
	DstChainID    *string
}

type ICSConfig struct {
	ProviderVerOverride     string         `yaml:"provider,omitempty" json:"provider,omitempty"`
	ConsumerVerOverride     string         `yaml:"consumer,omitempty" json:"consumer,omitempty"`
	ConsumerCopyProviderKey func(int) bool `yaml:"-" json:"-"`
}

// GenesisConfig is used to start a chain from a pre-defined genesis state.
type GenesisConfig struct {
	// Genesis file contents for the chain (e.g. genesis.json for CometBFT chains).
	Contents []byte

	// If true, all validators will be emulated in the genesis file.
	// By default, only the first 2/3 (sorted by Voting Power desc) of validators will be emulated.
	AllValidators bool

	// MaxVals is a safeguard so that we don't accidentally emulate too many validators. Defaults to 10.
	// If more than MaxVals validators are required to meet 2/3 VP, the test will fail.
	MaxVals int
}
