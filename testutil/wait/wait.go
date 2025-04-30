package wait

import (
	"context"
	"fmt"
	"golang.org/x/sync/errgroup"
	"time"
)

// ChainHeighter fetches the current chain block height.
type ChainHeighter interface {
	Height(ctx context.Context) (int64, error)
}

// ForBlocks blocks until all chains reach a block height delta equal to or greater than the delta argument.
// If a ChainHeighter does not monotonically increase the height, this function may block program execution indefinitely.
func ForBlocks(ctx context.Context, delta int, chains ...ChainHeighter) error {
	if len(chains) == 0 {
		panic("missing chains")
	}
	eg, egCtx := errgroup.WithContext(ctx)
	for i := range chains {
		chain := chains[i]
		eg.Go(func() error {
			h := &height{Chain: chain}
			return h.ForDelta(egCtx, delta)
		})
	}
	return eg.Wait()
}

// ForBlocksUtil iterates from 0 to maxBlocks and calls fn function with the current iteration index as a parameter.
// If fn returns nil, the loop is terminated and the function returns nil.
// If fn returns an error and the loop has iterated over all maxBlocks without success, the error is returned.
func ForBlocksUtil(maxBlocks int, fn func(i int) error) error {
	for i := 0; i < maxBlocks; i++ {
		if err := fn(i); err == nil {
			break
		} else if i == maxBlocks-1 {
			return err
		}
	}
	return nil
}

// ForNodesInSync returns an error if the nodes are not in sync with the chain.
func ForNodesInSync(ctx context.Context, chain ChainHeighter, nodes []ChainHeighter) error {
	var chainHeight int64
	nodeHeights := make([]int64, len(nodes))
	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() (err error) {
		chainHeight, err = chain.Height(egCtx)
		return err
	})
	for i, n := range nodes {
		eg.Go(func() (err error) {
			nodeHeights[i], err = n.Height(egCtx)
			return err
		})
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	for _, h := range nodeHeights {
		if h < chainHeight {
			return fmt.Errorf("node is not yet in sync: %d < %d", h, chainHeight)
		}
	}
	// all nodes >= chainHeight
	return nil
}

// ForInSync blocks until all nodes have heights greater than or equal to the chain height.
func ForInSync(ctx context.Context, chain ChainHeighter, nodes ...ChainHeighter) error {
	if len(nodes) == 0 {
		panic("missing nodes")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := ForNodesInSync(ctx, chain, nodes); err != nil {
				continue
			}
			return nil
		}
	}
}

type height struct {
	Chain ChainHeighter

	starting int64
	current  int64
}

func (h *height) ForDelta(ctx context.Context, delta int) error {
	for h.delta() < delta {
		cur, err := h.Chain.Height(ctx)
		if err != nil {
			return err
		}
		// We assume the chain will eventually return a non-zero height, otherwise
		// this may block indefinitely.
		if cur == 0 {
			continue
		}
		h.update(cur)
	}
	return nil
}

func (h *height) delta() int {
	if h.starting == 0 {
		return 0
	}
	return int(h.current - h.starting)
}

func (h *height) update(height int64) {
	if h.starting == 0 {
		h.starting = height
	}
	h.current = height
}

// ForCondition waits for a condition function to return true within a timeout, polling at the specified interval.
// The context controls the timeout and cancellation of the waiting process.
func ForCondition(ctx context.Context, timeoutAfter, pollingInterval time.Duration, fn func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeoutAfter)
	defer cancel()

	ticker := time.NewTicker(pollingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("condition not met within %.2f seconds: %w", timeoutAfter.Seconds(), ctx.Err())
		case <-ticker.C:
			ok, err := fn()
			if err != nil {
				return fmt.Errorf("error checking condition: %w", err)
			}
			if ok {
				return nil
			}
		}
	}
}
