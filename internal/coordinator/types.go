package coordinator

import (
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

// CoordinatorState represents the current operational state of a coordinator node.
type CoordinatorState uint8

const (
	// StateStandby indicates the coordinator is waiting to become the leader.
	StateStandby CoordinatorState = iota
	// StateCandidate indicates the coordinator is actively campaigning for leadership.
	StateCandidate
	// StateLeader indicates the coordinator is the active leader serving requests.
	StateLeader
)

func (s CoordinatorState) String() string {
	switch s {
	case StateStandby:
		return "STANDBY"
	case StateCandidate:
		return "CANDIDATE"
	case StateLeader:
		return "LEADER"
	default:
		return "UNKNOWN"
	}
}

// JobStatus represents the lifecycle state of a job.
type JobStatus uint8

const (
	JobCreated   JobStatus = iota // Job has been submitted but not yet deployed.
	JobDeploying                  // Job is being deployed to workers.
	JobRunning                    // Job is actively processing data.
	JobFinishing                  // Job is draining and completing gracefully.
	JobFinished                   // Job completed successfully.
	JobFailing                    // Job encountered an error and is shutting down.
	JobFailed                     // Job terminated due to an error.
	JobCanceling                  // Job cancellation was requested.
	JobCanceled                   // Job was canceled by the user.
	JobPaused                     // Job is paused (savepoint taken).
)

func (s JobStatus) String() string {
	switch s {
	case JobCreated:
		return "CREATED"
	case JobDeploying:
		return "DEPLOYING"
	case JobRunning:
		return "RUNNING"
	case JobFinishing:
		return "FINISHING"
	case JobFinished:
		return "FINISHED"
	case JobFailing:
		return "FAILING"
	case JobFailed:
		return "FAILED"
	case JobCanceling:
		return "CANCELING"
	case JobCanceled:
		return "CANCELED"
	case JobPaused:
		return "PAUSED"
	default:
		return "UNKNOWN"
	}
}

// IsTerminal returns true if the job is in a final state.
func (s JobStatus) IsTerminal() bool {
	return s == JobFinished || s == JobFailed || s == JobCanceled
}

// CheckpointStatus represents the lifecycle state of a checkpoint.
type CheckpointStatus uint8

const (
	CheckpointTriggered  CheckpointStatus = iota // Checkpoint has been triggered.
	CheckpointInProgress                         // Checkpoint is being taken.
	CheckpointCompleted                          // Checkpoint completed successfully.
	CheckpointFailed                             // Checkpoint failed.
	CheckpointAborted                            // Checkpoint was aborted (e.g. during recovery).
)

func (s CheckpointStatus) String() string {
	switch s {
	case CheckpointTriggered:
		return "TRIGGERED"
	case CheckpointInProgress:
		return "IN_PROGRESS"
	case CheckpointCompleted:
		return "COMPLETED"
	case CheckpointFailed:
		return "FAILED"
	case CheckpointAborted:
		return "ABORTED"
	default:
		return "UNKNOWN"
	}
}

// JobMeta holds the persisted metadata for a single job.
type JobMeta struct {
	RestartPolicy                 *rpc.RestartPolicy    `codec:"restart_policy,omitempty"`
	LastCheckpointTrigger         time.Time             `codec:"last_checkpoint_trigger,omitempty"`
	CheckpointPolicy              *rpc.CheckpointPolicy `codec:"checkpoint_policy,omitempty"`
	DeploymentGeneration          uint64                `codec:"deployment_generation,omitempty"`
	CheckpointOutcomes            []bool                `codec:"checkpoint_outcomes,omitempty"`
	CheckpointAttempts            uint64                `codec:"checkpoint_attempts,omitempty"`
	CheckpointFailures            uint64                `codec:"checkpoint_failures,omitempty"`
	ConsecutiveCheckpointFailures int                   `codec:"consecutive_checkpoint_failures,omitempty"`
	LastCheckpointCompletion      time.Time             `codec:"last_checkpoint_completion,omitempty"`
	CheckpointFailure             string                `codec:"checkpoint_failure,omitempty"`
	// RescaleCheckpoint selects a completed savepoint for changed ownership.
	RescaleRollback   *RescaleRollback `codec:"rescale_rollback,omitempty"`
	RescaleFailure    string           `codec:"rescale_failure,omitempty"`
	RescaleRequested  bool             `codec:"rescale_requested,omitempty"`
	RecoveryAttempts  int              `codec:"recovery_attempts,omitempty"`
	RunningSince      time.Time        `codec:"running_since,omitempty"`
	RescaleCheckpoint uint64           `codec:"rescale_checkpoint,omitempty"`
	ID                string           `codec:"id"`
	Name              string           `codec:"name"`
	Status            JobStatus        `codec:"status"`
	Parallelism       int              `codec:"parallelism"`
	ConfigHash        string           `codec:"config_hash"`
	CreatedAt         time.Time        `codec:"created_at"`
	UpdatedAt         time.Time        `codec:"updated_at"`
	StartedAt         time.Time        `codec:"started_at,omitempty"`
	FinishedAt        time.Time        `codec:"finished_at,omitempty"`
	RestartCount      int              `codec:"restart_count,omitempty"`
	LatestCheckpoint  uint64           `codec:"latest_checkpoint,omitempty"`
	Config            []byte           `codec:"config,omitempty"`
	SavepointPath     string           `codec:"savepoint_path,omitempty"`
}

// TaskAssignmentMap maps task IDs to the worker IDs they are assigned to.
type TaskAssignmentMap struct {
	RecoveryAttemptCharged bool                                       `codec:"recovery_attempt_charged,omitempty"`
	RestoreCheckpoints     map[string]rpc.CheckpointRestoreDescriptor `codec:"restore_checkpoints,omitempty"`
	RescaleParts           map[string][]RescaleStatePart              `codec:"rescale_parts,omitempty"`
	TaskDescriptors        []rpc.TaskDescriptor                       `codec:"task_descriptors,omitempty"`
	EpochID                uint64                                     `codec:"eid,omitempty"`
	AttemptID              string                                     `codec:"attempt_id,omitempty"`
	Replicas               map[string]string                          `codec:"replicas,omitempty"`
	JobID                  string                                     `codec:"job_id"`
	Assignments            map[string]string                          `codec:"assignments"` // task_id → worker_id
}

// CheckpointMeta holds persisted metadata for a single checkpoint.
type CheckpointMeta struct {
	Final           bool                 `codec:"final,omitempty"`
	AttemptID       string               `codec:"attempt_id,omitempty"`
	InvalidReason   string               `codec:"invalid_reason,omitempty"`
	ManifestVersion int                  `codec:"manifest_version,omitempty"`
	TaskManifests   map[string][]byte    `codec:"task_manifests,omitempty"`
	TaskDescriptors []rpc.TaskDescriptor `codec:"task_descriptors,omitempty"`
	NumKeyGroups    int                  `codec:"key_groups,omitempty"`
	SavepointID     string               `codec:"savepoint_id,omitempty"`
	Replicas        map[string]string    `codec:"replicas,omitempty"`
	EpochID         uint64               `codec:"epoch_id,omitempty"`
	Tasks           map[string]string    `codec:"tasks,omitempty"`
	ID              uint64               `codec:"id"`
	JobID           string               `codec:"job_id"`
	Status          CheckpointStatus     `codec:"status"`
	Offsets         map[string]int64     `codec:"offsets"`     // source → offset
	StatePaths      map[string]string    `codec:"state_paths"` // task_id → path
	Timestamp       time.Time            `codec:"timestamp"`
}

// SavepointStatus represents the lifecycle state of a savepoint.
type SavepointStatus uint8

const (
	SavepointInProgress SavepointStatus = iota // Savepoint is being taken.
	SavepointCompleted                         // Savepoint completed successfully.
	SavepointFailed                            // Savepoint failed.
)

func (s SavepointStatus) String() string {
	switch s {
	case SavepointInProgress:
		return "IN_PROGRESS"
	case SavepointCompleted:
		return "COMPLETED"
	case SavepointFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// SavepointMeta holds persisted metadata for a single savepoint.
type SavepointMeta struct {
	NumKeyGroups   int             `codec:"key_groups,omitempty"`
	CheckpointID   uint64          `codec:"checkpoint_id,omitempty"`
	EpochID        uint64          `codec:"epoch_id,omitempty"`
	ID             string          `codec:"id"`
	JobID          string          `codec:"job_id"`
	Status         SavepointStatus `codec:"status"`
	Path           string          `codec:"path"`
	TriggerTime    time.Time       `codec:"trigger_time"`
	CompletionTime time.Time       `codec:"completion_time,omitempty"`
}

// WorkerMeta holds persisted metadata for a registered worker.
type WorkerMeta struct {
	Lost                 bool                     `codec:"-" json:"-"`
	Resources            *rpc.ResourceReport      `codec:"-" json:"-"`
	TaskReports          []rpc.RunningTaskSummary `codec:"-" json:"-"`
	RPCPeerEpoch         uint64                   `codec:"-" json:"-"`
	RPCClient            *rpc.Client              `codec:"-" json:"-"`
	SupportsReservations bool                     `codec:"slot_reservations,omitempty"`
	CheckpointAddress    string                   `codec:"checkpoint_address,omitempty"`
	ID                   string                   `codec:"id"`
	Address              string                   `codec:"address"`
	TaskSlotsTotal       int                      `codec:"task_slots_total"`
	TaskSlotsAvailable   int                      `codec:"task_slots_available"`
	LastHeartbeat        time.Time                `codec:"-"`
	RunningTasks         []string                 `codec:"running_tasks"`
}

// ClusterConfig holds cluster-wide configuration parameters.
type ClusterConfig struct {
	CheckpointInterval time.Duration `codec:"checkpoint_interval"`
	DefaultParallelism int           `codec:"default_parallelism"`
}

// LeaderInfo describes the current cluster leader.
type LeaderInfo struct {
	RPCAddress string `codec:"rpc_address" json:"leader_rpc_addr"`
	NodeID     string `codec:"node_id"  json:"leader_id"`
	Address    string `codec:"address"  json:"leader_http_addr"`
	Epoch      uint64 `codec:"epoch"    json:"leader_epoch"`
}

// CommandType identifies the type of a coordinator command.
type CommandType uint8

const (
	CmdDeployTask   CommandType = iota // Deploy a task to a worker.
	CmdCancelTask                      // Cancel a running task.
	CmdTriggerCkpt                     // Trigger a checkpoint.
	CmdAbortCkpt                       // Abort an in-flight checkpoint.
	CmdUpdateConfig                    // Update cluster configuration.
)

// CoordinatorCommand is a fenced command sent from the coordinator to workers.
type CoordinatorCommand struct {
	Epoch   uint64      `codec:"epoch"`
	Type    CommandType `codec:"type"`
	Payload []byte      `codec:"payload"`
}

// RescaleRollback retains the last working topology until the new tasks all run.
type RescaleRollback struct {
	PlacementFailedSince time.Time `codec:"placement_failed_since,omitempty"`
	Config               []byte    `codec:"config"`
	Parallelism          int       `codec:"parallelism"`
	Checkpoint           uint64    `codec:"checkpoint"`
	Attempted            bool      `codec:"attempted"`
}
