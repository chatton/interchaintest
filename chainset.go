package interchaintest

import (
	"context"
	"fmt"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"sync"

	"github.com/chatton/interchaintest/v1/ibc"
)

// chainSet is an unordered collection of ibc.Chain,
// to group methods that apply actions against all chains in the set.
//
// The main purpose of the chainSet is to unify test setup when working with any number of chains.
type chainSet struct {
	log *zap.Logger

	chains map[ibc.Chain]struct{}
}

func newChainSet(log *zap.Logger, chains []ibc.Chain) *chainSet {
	cs := &chainSet{
		log: log,

		chains: make(map[ibc.Chain]struct{}, len(chains)),
	}

	for _, chain := range chains {
		cs.chains[chain] = struct{}{}
	}

	return cs
}

// Initialize concurrently calls Initialize against each chain in the set.
// Each chain may run a docker pull command,
// so with a cold image cache, running concurrently may save some time.
func (cs *chainSet) Initialize(ctx context.Context, testName string, cli *client.Client, networkID string) error {
	var eg errgroup.Group

	for c := range cs.chains {
		cs.log.Info("Initializing chain", zap.String("chain_id", c.Config().ChainID))
		eg.Go(func() error {
			if err := c.Initialize(ctx, testName, cli, networkID); err != nil {
				return fmt.Errorf("failed to initialize chain %s: %w", c.Config().Name, err)
			}

			cs.log.Info("Initialized chain", zap.String("chain_id", c.Config().ChainID))

			return nil
		})
	}

	return eg.Wait()
}

// CreateCommonAccount creates a key with the given name on each chain in the set,
// and returns the bech32 representation of each account created.
// The typical use of CreateCommonAccount is to create a faucet account on each chain.
//
// The keys are created concurrently because creating keys on one chain
// should have no effect on any other chain.
func (cs *chainSet) CreateCommonAccount(ctx context.Context, keyName string) (faucetAddresses map[ibc.Chain]string, err error) {
	var mu sync.Mutex
	faucetAddresses = make(map[ibc.Chain]string, len(cs.chains))

	eg, egCtx := errgroup.WithContext(ctx)

	for c := range cs.chains {
		eg.Go(func() error {
			wallet, err := c.BuildWallet(egCtx, keyName, "")
			if err != nil {
				return err
			}

			mu.Lock()
			faucetAddresses[c] = wallet.FormattedAddress()
			mu.Unlock()

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to create common account with name %s: %w", keyName, err)
	}

	return faucetAddresses, nil
}

// Start concurrently calls Start against each chain in the set.
func (cs *chainSet) Start(ctx context.Context, testName string, additionalGenesisWallets map[ibc.Chain][]ibc.WalletAmount) error {
	eg, egCtx := errgroup.WithContext(ctx)

	for c := range cs.chains {
		eg.Go(func() error {
			chainCfg := c.Config()
			// standard chain startup
			if err := c.Start(testName, egCtx, additionalGenesisWallets[c]...); err != nil {
				return fmt.Errorf("failed to start chain %s: %w", chainCfg.Name, err)
			}

			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}

	eg, egCtx = errgroup.WithContext(ctx)

	return eg.Wait()
}

// Close frees any resources associated with the chainSet.
func (cs *chainSet) Close() error {
	return nil
}
