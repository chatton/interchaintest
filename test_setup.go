package interchaintest

import (
	"github.com/moby/moby/client"

	"github.com/chatton/interchaintest/dockerutil"
)

const (
	FaucetAccountKeyName = "faucet"
)

// DockerSetup returns a new Docker Client and the ID of a configured network, associated with t.
//
// If any part of the setup fails, t.Fatal is called.
func DockerSetup(t dockerutil.DockerSetupTestingT) (*client.Client, string) {
	t.Helper()
	return dockerutil.DockerSetup(t)
}
