package ghaccount

import (
	"context"
	"time"
)

// pickBudget bounds one shared permission-check run across all candidate
// accounts; each step inside keeps its own stepTimeout.
const pickBudget = 30 * time.Second

// shared runs fn once per key for all concurrent callers. The work is detached
// from the leader's cancellation and deadline and gets its own budget, so a
// short-lived caller (a 3 s project read) cannot starve the others. Each caller
// waits on its own ctx and gives up alone. fn's ctx is the detached one.
func (r *Resolver) shared(ctx context.Context, key string, budget time.Duration, fn func(context.Context) (any, error)) (any, error) {
	ch := r.flight.DoChan(key, func() (any, error) {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
		defer cancel()
		return fn(wctx)
	})
	select {
	case res := <-ch:
		return res.Val, res.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
