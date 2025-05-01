package interchaintest

import (
	"github.com/chatton/interchaintest/chain/types"
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

	// Embedded Config to allow for simple JSON definition of a ChainSpec.
	types.Config

	// How many validators and how many full nodes to use
	// when instantiating the chain.
	// If unspecified, NumValidators defaults to 2 and NumFullNodes defaults to 1.
	NumValidators, NumFullNodes *int
}
