package testutil

import (
	"context"
)

type BlockPoller[T any] struct {
	CurrentHeight func(ctx context.Context) (int64, error)
	PollFunc      func(ctx context.Context, height int64) (T, error)
}

func (p BlockPoller[T]) DoPoll(ctx context.Context, startHeight, maxHeight int64) (T, error) {
	if maxHeight < startHeight {
		panic("maxHeight must be greater than or equal to startHeight")
	}

	var (
		pollErr error
		zero    T
	)

	cursor := startHeight
	for cursor <= maxHeight {
		curHeight, err := p.CurrentHeight(ctx)
		if err != nil {
			return zero, err
		}
		if cursor > curHeight {
			continue
		}

		found, findErr := p.PollFunc(ctx, cursor)

		if findErr != nil {
			pollErr = findErr
			cursor++
			continue
		}

		return found, nil
	}
	return zero, pollErr
}
