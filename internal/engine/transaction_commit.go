package engine

import (
	"context"
	"fmt"
	"time"
)

const (
	transactionCommitAttempts = 5
	transactionCommitBackoff  = 100 * time.Millisecond
)

// commitTransaction repeats the same durable decision, never a new transaction.
// Commit must be idempotent even when the external operation succeeded but its
// response was lost. Exhaustion leaves recovery responsible for this decision.
func commitTransaction(ctx context.Context, sink TransactionalSink, checkpointID uint64) error {
	return retryTransactionCommit(ctx, sink, checkpointID, transactionCommitAttempts, transactionCommitBackoff)
}

func retryTransactionCommit(ctx context.Context, sink TransactionalSink, checkpointID uint64, attempts int, backoff time.Duration) error {
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrCommitFailed, err)
		}
		if last = safeInvoke(func() error { return sink.Commit(ctx, checkpointID) }); last == nil {
			return nil
		}
		if attempt+1 == attempts {
			break
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: %w", ErrCommitFailed, ctx.Err())
		case <-timer.C:
		}
		backoff *= 2
	}
	return fmt.Errorf("%w after %d attempts: %w", ErrCommitFailed, attempts, last)
}
