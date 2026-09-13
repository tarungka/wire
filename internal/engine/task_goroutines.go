package engine

import (
	"context"
	"sync/atomic"
)

type taskGoroutineKey struct{}

// taskGoroutineStarted accounts for engine-owned workers and callbacks only.
// Shared transport, Go runtime and Pebble goroutines belong to their respective
// subsystems and are excluded. Invoke at goroutine entry and defer the result.
func taskGoroutineStarted(ctx context.Context) func() {
	count, _ := ctx.Value(taskGoroutineKey{}).(*atomic.Int64)
	if count == nil {
		return func() {}
	}
	count.Add(1)
	return func() { count.Add(-1) }
}
