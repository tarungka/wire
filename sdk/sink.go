package sdk

import (
	"context"

	"github.com/tarungka/wire/internal/engine"
)

// Sink consumes events as a terminal operator.
type Sink interface {
	// Open initializes the sink (e.g. connect to database, open file).
	Open(ctx context.Context) error
	// Write writes a single event to the sink.
	Write(ctx context.Context, event Event) error
	// Close releases resources held by the sink.
	Close() error
}

// BatchSink optionally accepts buffered events synchronously. The runtime groups
// up to 100 queued records and flushes when input is idle or before checkpoint,
// watermark and end-of-input boundaries. Configured record-level retry/DLQ/drop
// policies use Write instead, since a batch error cannot attribute individual
// outcomes. Batch errors fail the task; connectors must account for partial
// external delivery and replay. WriteBatch must not retain the supplied slice.
type BatchSink interface {
	Sink
	WriteBatch(ctx context.Context, events []Event) error
}

// TransactionalSink participates in checkpoint-driven two-phase commit.
// It is structurally compatible with the worker's transactional sink operator
// contract and can be returned directly from a registered sink factory.
//
// PreCommit makes writes durable but invisible. Checkpoint runs after PreCommit
// and must serialize the prepared transaction identity; RestoreCheckpoint must
// recover that identity after Open. Commit is called only for a globally
// completed checkpoint, including during recovery, and must be idempotent even
// after a successful external commit whose response was lost.
//
// Close releases local resources without rolling back an externally prepared
// transaction with an unknown decision. Abort is reserved for an unreported
// active transaction or an explicit abort decision. All context-taking methods
// must honor cancellation. The external system must keep prepared transactions
// alive long enough for checkpoint completion and recovery.
//
// Transaction identities must be scoped by job and logical sink task, not just
// checkpoint ID. A connector must fence obsolete writers in its external system.
// This interface does not provide cross-sink atomic visibility.
type TransactionalSink interface {
	Sink
	// RecoverTransactions must establish the external DeploymentGeneration fence
	// and abort orphan transactions, preserving only the selected completed
	// checkpoint for commit. It must reject stale generations and be idempotent.
	RecoverTransactions(ctx context.Context, recovery TransactionRecovery) error
	BeginTransaction(ctx context.Context) error
	PreCommit(ctx context.Context, checkpointID uint64) error
	Commit(ctx context.Context, checkpointID uint64) error
	Abort(ctx context.Context) error
	Checkpoint(checkpointID uint64) ([]byte, error)
	RestoreCheckpoint(state []byte) error
}

// TransactionRecovery carries task authority and the selected global checkpoint
// decision for connector-side fencing and orphan reconciliation.
type TransactionRecovery = engine.TransactionRecovery
