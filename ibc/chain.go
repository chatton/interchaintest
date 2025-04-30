package ibc

import (
	"context"

	"github.com/moby/moby/client"

	"cosmossdk.io/math"
)

type Chain interface {
	// Config fetches the chain configuration.
	Config() Config

	// Initialize initializes node structs so that things like initializing keys can be done before starting the chain
	Initialize(ctx context.Context, testName string, cli *client.Client, networkID string) error

	// Start sets up everything needed (validators, gentx, fullnodes, peering, additional accounts) for chain to start from genesis.
	Start(ctx context.Context, additionalGenesisWallets ...WalletAmount) error

	// Stop stops the running chain and gracefully cleans up associated resources. It returns an error if the operation fails.
	Stop(ctx context.Context) error
	// Exec runs an arbitrary command using Chain's docker environment.
	// Whether the invoked command is run in a one-off container or execing into an already running container
	// is up to the chain implementation.
	//
	// "env" are environment variables in the format "MY_ENV_VAR=value"
	Exec(ctx context.Context, cmd []string, env []string) (stdout, stderr []byte, err error)

	// ExportState exports the chain state at specific height.
	ExportState(ctx context.Context, height int64) (string, error)

	// GetRPCAddress retrieves the rpc address that can be reached by other containers in the docker network.
	GetRPCAddress() string

	// GetGRPCAddress retrieves the grpc address that can be reached by other containers in the docker network.
	GetGRPCAddress() string

	// GetHostRPCAddress returns the rpc address that can be reached by processes on the host machine.
	// Note that this will not return a valid value until after Start returns.
	GetHostRPCAddress() string

	// GetHostPeerAddress returns the p2p address that can be reached by processes on the host machine.
	// Note that this will not return a valid value until after Start returns.
	GetHostPeerAddress() string

	// GetHostGRPCAddress returns the grpc address that can be reached by processes on the host machine.
	// Note that this will not return a valid value until after Start returns.
	GetHostGRPCAddress() string

	// HomeDir is the home directory of a node running in a docker container. Therefore, this maps to
	// the container's filesystem (not the host).
	HomeDir() string

	// CreateKey creates a test key in the "user" node (either the first fullnode or the first validator if no fullnodes).
	CreateKey(ctx context.Context, keyName string) error

	// RecoverKey recovers an existing user from a given mnemonic.
	RecoverKey(ctx context.Context, name, mnemonic string) error

	// GetAddress fetches the bech32 address for a test key on the "user" node (either the first fullnode or the first validator if no fullnodes).
	GetAddress(ctx context.Context, keyName string) ([]byte, error)

	// SendFunds sends funds to a wallet from a user account.
	SendFunds(ctx context.Context, keyName string, amount WalletAmount) error

	// Height returns the current block height or an error if unable to get current height.
	Height(ctx context.Context) (int64, error)

	// GetBalance fetches the current balance for a specific account address and denom.
	GetBalance(ctx context.Context, address string, denom string) (math.Int, error)

	// GetGasFeesInNativeDenom gets the fees in native denom for an amount of spent gas.
	GetGasFeesInNativeDenom(gasPaid int64) int64

	// BuildWallet will return a chain-specific wallet
	// If mnemonic != "", it will restore using that mnemonic
	// If mnemonic == "", it will create a new key, mnemonic will not be populated
	BuildWallet(ctx context.Context, keyName string, mnemonic string) (Wallet, error)
}

// TransferOptions defines the options for an IBC packet transfer.
type TransferOptions struct {
	Timeout          *IBCTimeout
	Memo             string
	AbsoluteTimeouts bool
	Port             string // default: transfer
}
