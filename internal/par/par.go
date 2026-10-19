// Package par runs bounded parallel work for the stages and the adapters.
package par

import (
	"context"
	"sync"
)

// ForEach calls fn for every item with at most n calls in flight. The first error cancels the
// context passed to the calls still running, stops new ones starting, and is returned.
func ForEach[T any](ctx context.Context, n int, items []T, fn func(ctx context.Context, i int, item T) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	sem := make(chan struct{}, max(n, 1))
	var wg sync.WaitGroup
	for i, item := range items {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(ctx, i, item); err != nil {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	return context.Cause(ctx)
}
