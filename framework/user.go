package framework

import (
	"context"
	"fmt"
	"github.com/chatton/interchaintest/framework/types"
	"github.com/chatton/interchaintest/testutil/random"
	"github.com/chatton/interchaintest/testutil/wait"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"cosmossdk.io/math"
)

const (
	FaucetAccountKeyName = "faucet"
)

// GetAndFundTestUserWithMnemonic restores a user using the given mnemonic
// and funds it with the native chain denom.
// The caller should wait for some blocks to complete before the funds will be accessible.
func GetAndFundTestUserWithMnemonic(
	ctx context.Context,
	keyNamePrefix, mnemonic string,
	amount math.Int,
	chain types.Chain,
) (types.Wallet, error) {
	chainCfg := chain.Config()
	keyName := fmt.Sprintf("%s-%s-%s", keyNamePrefix, chainCfg.ChainID, random.LowerCaseLetterString(3))
	user, err := chain.BuildWallet(ctx, keyName, mnemonic)
	if err != nil {
		return nil, fmt.Errorf("failed to get source user wallet: %w", err)
	}

	err = chain.SendFunds(ctx, FaucetAccountKeyName, types.WalletAmount{
		Address: user.FormattedAddress(),
		Amount:  amount,
		Denom:   chainCfg.Denom,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get funds from faucet: %w", err)
	}

	return user, nil
}

// GetAndFundTestUsers generates and funds chain users with the native chain denom.
// The caller should wait for some blocks to complete before the funds will be accessible.
func GetAndFundTestUsers(
	t *testing.T,
	ctx context.Context,
	keyNamePrefix string,
	amount math.Int,
	chains ...types.Chain,
) []types.Wallet {
	t.Helper()

	users := make([]types.Wallet, len(chains))
	var eg errgroup.Group
	for i, chain := range chains {
		eg.Go(func() error {
			user, err := GetAndFundTestUserWithMnemonic(ctx, keyNamePrefix, "", amount, chain)
			if err != nil {
				return err
			}
			users[i] = user
			return nil
		})
	}
	require.NoError(t, eg.Wait())

	// TODO(nix 05-17-2022): Map with generics once using go 1.18
	chainHeights := make([]wait.ChainHeighter, len(chains))
	for i := range chains {
		chainHeights[i] = chains[i]
	}
	return users
}
