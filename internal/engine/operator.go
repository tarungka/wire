package engine

import "context"

// Operator is the base interface for all stream processing operators.
type Operator interface {
	// Open initializes the operator. Called once before processing starts.
	Open(ctx context.Context) error
	// Close releases resources. Called once after processing ends.
	Close() error
	// Checkpoint snapshots the operator state for the given checkpoint ID.
	Checkpoint(checkpointID uint64) ([]byte, error)
}

// StateHandleOperator exposes backend snapshot identity for artifact replication.
// CheckpointState replaces Checkpoint during a capture; callers must not invoke
// both for the same boundary. RestoreState runs after Open and before processing.
type StateHandleOperator interface {
	Operator
	CheckpointState(checkpointID uint64) (SnapshotHandle, error)
	RestoreState(SnapshotHandle) error
}

// CheckpointRestorer restores opaque bytes returned by Operator.Checkpoint.
// The runtime invokes it after Open and before processing. Typed backend
// handles use StateHandleOperator.RestoreState instead.
type CheckpointRestorer interface {
	RestoreCheckpoint([]byte) error
}

// MapOperator transforms each input event into exactly one output event.
type MapOperator interface {
	Operator
	Map(ctx context.Context, event Event) (Event, error)
}

// FlatMapOperator transforms each input event into zero or more output events.
type FlatMapOperator interface {
	Operator
	FlatMap(ctx context.Context, event Event, emit func(Event)) error
}

// SourceOperator produces events from an external source.
type SourceOperator interface {
	Operator
	// ReadBatch returns the next batch of events. Returns nil slice at end of input.
	ReadBatch(ctx context.Context) ([]Event, error)
	// GenerateWatermark is retained for source compatibility. Runtime strategy
	// selection defaults to bounded out-of-orderness instead of this callback.
	GenerateWatermark() int64
}

// SinkOperator consumes events as a terminal operator.
type SinkOperator interface {
	Operator
	Write(ctx context.Context, event Event) error
}

// BatchSinkOperator optionally consumes buffered events synchronously. An error
// fails the task; the connector must account for partial external delivery.
type BatchSinkOperator interface {
	SinkOperator
	WriteBatch(context.Context, []Event) error
}

// TransactionalSink extends SinkOperator with two-phase commit (2PC) support.
// Sinks that implement this interface participate in the checkpoint protocol:
//   - BeginTransaction: open a new transaction (called at startup and after each Commit)
//   - PreCommit: flush buffered data and prepare the transaction for commit
//   - Commit: finalize the transaction after global checkpoint completion
//   - Abort: rollback an active transaction or one with an explicit abort decision
//
// PreCommit must durably prepare external writes. Checkpoint is called afterward
// and must capture enough identity to repeat Commit after RestoreCheckpoint.
// Commit must be idempotent, including when an earlier call succeeded externally
// but its response was lost. Close must release local resources without aborting
// a prepared transaction whose global decision is unknown. The runtime preserves
// such transactions for decision-driven recovery rather than guessing on exit.
type TransactionalSink interface {
	SinkOperator
	BeginTransaction(ctx context.Context) error
	PreCommit(ctx context.Context, checkpointID uint64) error
	Commit(ctx context.Context, checkpointID uint64) error
	Abort(ctx context.Context) error
}

// TransactionRecovery identifies the new writer and its selected global decision.
// The connector must fence the previous writer for this job/task before cleanup.
// Only CompletedCheckpointID is eligible for commit. Other uncommitted external
// transactions must be aborted, including older aborted checkpoint IDs; a numeric
// less-than comparison is not sufficient evidence of a commit decision.
type TransactionRecovery struct {
	// DeploymentGeneration must be compared atomically in the external store.
	DeploymentGeneration  uint64
	JobID                 string
	TaskID                string
	EpochID               uint64
	AttemptID             string
	CompletedCheckpointID uint64 // Zero means no globally completed snapshot.
}

// RecoverableTransactionalSink is required for distributed task startup.
// RecoverTransactions runs after checkpoint handle restoration, before Commit,
// BeginTransaction, RUNNING or input processing. It must atomically fence stale
// writers in the external system and resolve orphaned active/prepared work,
// preserving the selected completed transaction for idempotent Commit. Repeated
// recovery calls must be safe, including after failure midway through cleanup.
type RecoverableTransactionalSink interface {
	TransactionalSink
	RecoverTransactions(context.Context, TransactionRecovery) error
}
