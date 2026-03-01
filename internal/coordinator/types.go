package coordinator

import "time"

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
	ID          string    `codec:"id"`
	Name        string    `codec:"name"`
	Status      JobStatus `codec:"status"`
	Parallelism int       `codec:"parallelism"`
	ConfigHash  string    `codec:"config_hash"`
	CreatedAt   time.Time `codec:"created_at"`
	UpdatedAt   time.Time `codec:"updated_at"`
}

// TaskAssignmentMap maps task IDs to the worker IDs they are assigned to.
type TaskAssignmentMap struct {
	JobID       string            `codec:"job_id"`
	Assignments map[string]string `codec:"assignments"` // task_id → worker_id
}

// CheckpointMeta holds persisted metadata for a single checkpoint.
type CheckpointMeta struct {
	ID         uint64            `codec:"id"`
	JobID      string            `codec:"job_id"`
	Status     CheckpointStatus  `codec:"status"`
	Offsets    map[string]int64  `codec:"offsets"`     // source → offset
	StatePaths map[string]string `codec:"state_paths"` // task_id → path
	Timestamp  time.Time         `codec:"timestamp"`
}

// WorkerMeta holds persisted metadata for a registered worker.
type WorkerMeta struct {
	ID                 string    `codec:"id"`
	Address            string    `codec:"address"`
	TaskSlotsTotal     int       `codec:"task_slots_total"`
	TaskSlotsAvailable int       `codec:"task_slots_available"`
	LastHeartbeat      time.Time `codec:"last_heartbeat"`
	RunningTasks       []string  `codec:"running_tasks"`
}

// ClusterConfig holds cluster-wide configuration parameters.
type ClusterConfig struct {
	CheckpointInterval time.Duration `codec:"checkpoint_interval"`
	DefaultParallelism int           `codec:"default_parallelism"`
}

// LeaderInfo describes the current cluster leader.
type LeaderInfo struct {
	NodeID  string `codec:"node_id"  json:"leader_id"`
	Address string `codec:"address"  json:"leader_http_addr"`
	Epoch   uint64 `codec:"epoch"    json:"leader_epoch"`
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
