package rpc

// ---------- Enums ----------

// TaskStatus represents the lifecycle state of a task.
type TaskStatus uint8

const (
	TaskStatusUnknown    TaskStatus = 0
	TaskStatusCreated    TaskStatus = 1
	TaskStatusScheduled  TaskStatus = 2
	TaskStatusDeploying  TaskStatus = 3
	TaskStatusRunning    TaskStatus = 4
	TaskStatusFinishing  TaskStatus = 5
	TaskStatusCanceling  TaskStatus = 6
	TaskStatusFailed     TaskStatus = 7
	TaskStatusCanceled   TaskStatus = 8
	TaskStatusRecovering TaskStatus = 9
	TaskStatusFinished   TaskStatus = 10
)

// String returns the human-readable name of the task status.
func (s TaskStatus) String() string {
	switch s {
	case TaskStatusUnknown:
		return "Unknown"
	case TaskStatusCreated:
		return "Created"
	case TaskStatusScheduled:
		return "Scheduled"
	case TaskStatusDeploying:
		return "Deploying"
	case TaskStatusRunning:
		return "Running"
	case TaskStatusFinishing:
		return "Finishing"
	case TaskStatusCanceling:
		return "Canceling"
	case TaskStatusFailed:
		return "Failed"
	case TaskStatusCanceled:
		return "Canceled"
	case TaskStatusRecovering:
		return "Recovering"
	case TaskStatusFinished:
		return "Finished"
	default:
		return "Unknown"
	}
}

// OperatorType represents the kind of operator in the job graph.
type OperatorType uint8

const (
	OperatorTypeUnknown OperatorType = 0
	OperatorTypeSource  OperatorType = 1
	OperatorTypeMap     OperatorType = 2
	OperatorTypeFlatMap OperatorType = 3
	OperatorTypeFilter  OperatorType = 4
	OperatorTypeKeyBy   OperatorType = 5
	OperatorTypeWindow  OperatorType = 6
	OperatorTypeReduce  OperatorType = 7
	OperatorTypeJoin    OperatorType = 8
	OperatorTypeProcess OperatorType = 9
	OperatorTypeSink    OperatorType = 10
)

// String returns the human-readable name of the operator type.
func (o OperatorType) String() string {
	switch o {
	case OperatorTypeUnknown:
		return "Unknown"
	case OperatorTypeSource:
		return "Source"
	case OperatorTypeMap:
		return "Map"
	case OperatorTypeFlatMap:
		return "FlatMap"
	case OperatorTypeFilter:
		return "Filter"
	case OperatorTypeKeyBy:
		return "KeyBy"
	case OperatorTypeWindow:
		return "Window"
	case OperatorTypeReduce:
		return "Reduce"
	case OperatorTypeJoin:
		return "Join"
	case OperatorTypeProcess:
		return "Process"
	case OperatorTypeSink:
		return "Sink"
	default:
		return "Unknown"
	}
}

// ShuffleStrategy defines how data is partitioned between operators.
type ShuffleStrategy uint8

const (
	ShuffleStrategyUnknown   ShuffleStrategy = 0
	ShuffleStrategyForward   ShuffleStrategy = 1
	ShuffleStrategyHash      ShuffleStrategy = 2
	ShuffleStrategyBroadcast ShuffleStrategy = 3
	ShuffleStrategyRebalance ShuffleStrategy = 4
)

// String returns the human-readable name of the shuffle strategy.
func (s ShuffleStrategy) String() string {
	switch s {
	case ShuffleStrategyUnknown:
		return "Unknown"
	case ShuffleStrategyForward:
		return "Forward"
	case ShuffleStrategyHash:
		return "Hash"
	case ShuffleStrategyBroadcast:
		return "Broadcast"
	case ShuffleStrategyRebalance:
		return "Rebalance"
	default:
		return "Unknown"
	}
}

// CheckpointType identifies the trigger mode for a checkpoint.
type CheckpointType uint8

const (
	CheckpointTypeUnknown   CheckpointType = 0
	CheckpointTypePeriodic  CheckpointType = 1
	CheckpointTypeSavepoint CheckpointType = 2
)

// String returns the human-readable name of the checkpoint type.
func (c CheckpointType) String() string {
	switch c {
	case CheckpointTypeUnknown:
		return "Unknown"
	case CheckpointTypePeriodic:
		return "Periodic"
	case CheckpointTypeSavepoint:
		return "Savepoint"
	default:
		return "Unknown"
	}
}

// RestartStrategyType defines how a failed job should be restarted.
type RestartStrategyType uint8

const (
	RestartStrategyUnknown            RestartStrategyType = 0
	RestartStrategyFixedDelay         RestartStrategyType = 1
	RestartStrategyExponentialBackoff RestartStrategyType = 2
	RestartStrategyNoRestart          RestartStrategyType = 3
)

// String returns the human-readable name of the restart strategy.
func (r RestartStrategyType) String() string {
	switch r {
	case RestartStrategyUnknown:
		return "Unknown"
	case RestartStrategyFixedDelay:
		return "FixedDelay"
	case RestartStrategyExponentialBackoff:
		return "ExponentialBackoff"
	case RestartStrategyNoRestart:
		return "NoRestart"
	default:
		return "Unknown"
	}
}

// CommandType identifies a coordinator-to-worker command in heartbeat responses.
type CommandType uint8

const (
	CommandTypeNone         CommandType = 0
	CommandTypeCancelJob    CommandType = 1
	CommandTypeDeployTask   CommandType = 2
	CommandTypeCancelTask   CommandType = 3
	CommandTypeTakeSnapshot CommandType = 4
	CommandTypeUpdateConfig CommandType = 5
)

// String returns the human-readable name of the command type.
func (c CommandType) String() string {
	switch c {
	case CommandTypeNone:
		return "None"
	case CommandTypeCancelJob:
		return "CancelJob"
	case CommandTypeDeployTask:
		return "DeployTask"
	case CommandTypeCancelTask:
		return "CancelTask"
	case CommandTypeTakeSnapshot:
		return "TakeSnapshot"
	case CommandTypeUpdateConfig:
		return "UpdateConfig"
	default:
		return "Unknown"
	}
}

// DirectiveType identifies a coordinator-to-worker directive in update task status responses.
type DirectiveType uint8

const (
	DirectiveTypeNone             DirectiveType = 0
	DirectiveTypeCancelTask       DirectiveType = 1
	DirectiveTypeTriggerSavepoint DirectiveType = 2
)

// String returns the human-readable name of the directive type.
func (d DirectiveType) String() string {
	switch d {
	case DirectiveTypeNone:
		return "None"
	case DirectiveTypeCancelTask:
		return "CancelTask"
	case DirectiveTypeTriggerSavepoint:
		return "TriggerSavepoint"
	default:
		return "Unknown"
	}
}

// ---------- SubmitJob RPC ----------

// SubmitJobRequest is sent from the Coordinator to deploy a job to a Worker.
type SubmitJobRequest struct {
	JobID       string                 `codec:"jid"`
	JobName     string                 `codec:"jn"`
	Graph       JobGraph               `codec:"g"`
	Config      JobConfig              `codec:"cfg"`
	EpochID     uint64                 `codec:"eid"`
	RestoreInfo *CheckpointRestoreInfo `codec:"ri,omitempty"`
}

// SubmitJobResponse is the Worker's reply to SubmitJob.
type SubmitJobResponse struct {
	Accepted     bool                   `codec:"a"`
	Message      string                 `codec:"m,omitempty"`
	TaskStatuses []TaskDeploymentStatus `codec:"ts,omitempty"`
}

// JobGraph describes the DAG of operators and edges.
type JobGraph struct {
	Operators []OperatorDescriptor `codec:"ops"`
	Edges     []EdgeDescriptor     `codec:"edges"`
}

// OperatorDescriptor describes a single operator in the job graph.
type OperatorDescriptor struct {
	OperatorID  string       `codec:"oid"`
	Name        string       `codec:"n"`
	Type        OperatorType `codec:"t"`
	Parallelism int32        `codec:"p"`
	ClassName   string       `codec:"cn,omitempty"`
	Config      []byte       `codec:"cfg,omitempty"`
}

// EdgeDescriptor describes a connection between two operators.
type EdgeDescriptor struct {
	SourceOperatorID string          `codec:"src"`
	TargetOperatorID string          `codec:"tgt"`
	Shuffle          ShuffleStrategy `codec:"sh"`
	KeySelector      string          `codec:"ks,omitempty"`
}

// TaskDescriptor describes a single task instance to deploy.
type TaskDescriptor struct {
	TaskID       string                  `codec:"tid"`
	OperatorID   string                  `codec:"oid"`
	SubtaskIndex int32                   `codec:"si"`
	Parallelism  int32                   `codec:"p"`
	KeyGroup     KeyGroupRange           `codec:"kg"`
	Upstream     []UpstreamChannelInfo   `codec:"up,omitempty"`
	Downstream   []DownstreamChannelInfo `codec:"dn,omitempty"`
}

// KeyGroupRange defines the key-group range assigned to a task.
type KeyGroupRange struct {
	Start int32 `codec:"s"`
	End   int32 `codec:"e"`
}

// UpstreamChannelInfo describes a task's upstream data source.
type UpstreamChannelInfo struct {
	OperatorID   string `codec:"oid"`
	SubtaskIndex int32  `codec:"si"`
	Address      string `codec:"addr"`
}

// DownstreamChannelInfo describes a task's downstream data sink.
type DownstreamChannelInfo struct {
	OperatorID   string `codec:"oid"`
	SubtaskIndex int32  `codec:"si"`
	Address      string `codec:"addr"`
}

// CheckpointRestoreInfo carries state needed to restore from a checkpoint.
type CheckpointRestoreInfo struct {
	CheckpointID uint64        `codec:"cid"`
	Handles      []StateHandle `codec:"h"`
}

// StateHandle references a serialized state artifact.
type StateHandle struct {
	TaskID    string `codec:"tid"`
	Path      string `codec:"p"`
	SizeBytes int64  `codec:"sz"`
}

// JobConfig holds job-level configuration.
type JobConfig struct {
	MaxParallelism   int32           `codec:"mp"`
	RestartStrategy  RestartStrategy `codec:"rs"`
	CheckpointConfig []byte          `codec:"cc,omitempty"`
}

// RestartStrategy configures restart behavior on failure.
type RestartStrategy struct {
	Type              RestartStrategyType `codec:"t"`
	MaxAttempts       int32               `codec:"ma"`
	DelayMs           int64               `codec:"d"`
	MaxDelayMs        int64               `codec:"md,omitempty"`
	BackoffMultiplier float64             `codec:"bm,omitempty"`
}

// TaskDeploymentStatus reports a single task's deployment outcome.
type TaskDeploymentStatus struct {
	TaskID  string     `codec:"tid"`
	Status  TaskStatus `codec:"st"`
	Message string     `codec:"m,omitempty"`
}

// ---------- UpdateTaskStatus RPC ----------

// UpdateTaskStatusRequest is sent from Worker to Coordinator to report a task's status change.
type UpdateTaskStatusRequest struct {
	JobID   string           `codec:"jid"`
	TaskID  string           `codec:"tid"`
	Status  TaskStatus       `codec:"st"`
	EpochID uint64           `codec:"eid"`
	Metrics *TaskMetrics     `codec:"met,omitempty"`
	Failure *TaskFailureInfo `codec:"fi,omitempty"`
}

// UpdateTaskStatusResponse is the Coordinator's reply.
type UpdateTaskStatusResponse struct {
	Accepted   bool                   `codec:"a"`
	Message    string                 `codec:"m,omitempty"`
	Directives []CoordinatorDirective `codec:"dir,omitempty"`
}

// TaskMetrics carries runtime metrics for a task.
type TaskMetrics struct {
	RecordsIn   int64   `codec:"ri"`
	RecordsOut  int64   `codec:"ro"`
	BytesIn     int64   `codec:"bi"`
	BytesOut    int64   `codec:"bo"`
	Latency99   float64 `codec:"l99,omitempty"`
	BacklogSize int64   `codec:"bl,omitempty"`
}

// TaskFailureInfo describes a task failure.
type TaskFailureInfo struct {
	ErrorMessage string `codec:"em"`
	ErrorClass   string `codec:"ec,omitempty"`
	StackTrace   string `codec:"st,omitempty"`
	Timestamp    int64  `codec:"ts"`
}

// CoordinatorDirective carries a directive from Coordinator to Worker.
type CoordinatorDirective struct {
	Type   DirectiveType `codec:"t"`
	TaskID string        `codec:"tid,omitempty"`
	Data   []byte        `codec:"d,omitempty"`
}

// ---------- TriggerCheckpoint RPC ----------

// TriggerCheckpointRequest is sent from Coordinator to Worker to initiate a checkpoint.
type TriggerCheckpointRequest struct {
	JobID        string            `codec:"jid"`
	CheckpointID uint64            `codec:"cid"`
	EpochID      uint64            `codec:"eid"`
	Type         CheckpointType    `codec:"t"`
	Options      CheckpointOptions `codec:"opt"`
	Timestamp    int64             `codec:"ts"`
}

// TriggerCheckpointResponse is the Worker's reply.
type TriggerCheckpointResponse struct {
	Accepted bool                      `codec:"a"`
	Message  string                    `codec:"m,omitempty"`
	Statuses []CheckpointTriggerStatus `codec:"sts,omitempty"`
}

// CheckpointOptions carries checkpoint configuration.
type CheckpointOptions struct {
	TimeoutMs     int64  `codec:"to"`
	Synchronous   bool   `codec:"sync,omitempty"`
	SavepointPath string `codec:"sp,omitempty"`
}

// CheckpointTriggerStatus reports per-task checkpoint trigger outcome.
type CheckpointTriggerStatus struct {
	TaskID  string `codec:"tid"`
	Success bool   `codec:"ok"`
	Message string `codec:"m,omitempty"`
}

// ---------- AcknowledgeCheckpoint RPC ----------

// AcknowledgeCheckpointRequest is sent from Worker to Coordinator when a task completes its checkpoint.
type AcknowledgeCheckpointRequest struct {
	JobID        string                `codec:"jid"`
	TaskID       string                `codec:"tid"`
	CheckpointID uint64                `codec:"cid"`
	EpochID      uint64                `codec:"eid"`
	State        *StateHandle          `codec:"sh,omitempty"`
	Metrics      *AckCheckpointMetrics `codec:"met,omitempty"`
}

// AcknowledgeCheckpointResponse is the Coordinator's reply.
type AcknowledgeCheckpointResponse struct {
	Accepted bool   `codec:"a"`
	Message  string `codec:"m,omitempty"`
}

// AckCheckpointMetrics carries metrics for a completed checkpoint (named to avoid clash with engine.CheckpointMetrics).
type AckCheckpointMetrics struct {
	StateSizeBytes  int64 `codec:"sz"`
	DurationMs      int64 `codec:"dur"`
	SyncDurationMs  int64 `codec:"sd,omitempty"`
	AsyncDurationMs int64 `codec:"ad,omitempty"`
}

// ---------- RequestTaskSlots RPC ----------

// RequestTaskSlotsRequest is sent from Coordinator to Worker to query available slots.
type RequestTaskSlotsRequest struct {
	JobID         string `codec:"jid"`
	RequiredSlots int32  `codec:"rs"`
	MemoryMB      int32  `codec:"mem,omitempty"`
}

// RequestTaskSlotsResponse is the Worker's reply.
type RequestTaskSlotsResponse struct {
	Granted        int32               `codec:"g"`
	AvailableSlots int32               `codec:"as"`
	Resource       *WorkerResourceInfo `codec:"res,omitempty"`
	Message        string              `codec:"m,omitempty"`
}

// WorkerResourceInfo reports the worker's current resource state.
type WorkerResourceInfo struct {
	TotalSlots    int32   `codec:"ts"`
	UsedSlots     int32   `codec:"us"`
	TotalMemoryMB int32   `codec:"tm"`
	UsedMemoryMB  int32   `codec:"um"`
	CPUUsage      float64 `codec:"cpu,omitempty"`
}

// ---------- Heartbeat RPC ----------

// ResourceReport carries worker-level resource utilization.
type ResourceReport struct {
	CPUUsagePercent  float64 `codec:"cpu"`
	MemoryUsedBytes  int64   `codec:"mub"`
	MemoryTotalBytes int64   `codec:"mtb"`
	DiskUsedBytes    int64   `codec:"dub"`
	DiskTotalBytes   int64   `codec:"dtb"`
	GoroutineCount   int     `codec:"gc"`
}

// HeartbeatRequest is sent from Worker to Coordinator as a liveness signal.
type HeartbeatRequest struct {
	WorkerID  string               `codec:"wid"`
	EpochID   uint64               `codec:"eid"`
	Timestamp int64                `codec:"ts,omitempty"`
	Load      *WorkerLoad          `codec:"ld,omitempty"`
	Resources *ResourceReport      `codec:"res,omitempty"`
	Tasks     []RunningTaskSummary `codec:"tsk,omitempty"`
}

// HeartbeatResponse is the Coordinator's reply, which may include commands.
type HeartbeatResponse struct {
	Accepted bool            `codec:"a"`
	EpochID  uint64          `codec:"eid"`
	Commands []WorkerCommand `codec:"cmd,omitempty"`
}

// WorkerLoad reports the worker's current load.
type WorkerLoad struct {
	CPUUsage    float64 `codec:"cpu"`
	MemoryUsage float64 `codec:"mem"`
	ActiveSlots int32   `codec:"as"`
	TotalSlots  int32   `codec:"ts"`
}

// RunningTaskSummary is a brief status of a running task.
type RunningTaskSummary struct {
	TaskID   string       `codec:"tid"`
	JobID    string       `codec:"jid"`
	Status   TaskStatus   `codec:"st"`
	UptimeMs int64        `codec:"up"`
	Metrics  *TaskMetrics `codec:"met,omitempty"`
}

// ---------- RegisterWorker RPC ----------

// RegisterWorkerRequest is sent from Worker to Coordinator to register or re-register.
type RegisterWorkerRequest struct {
	WorkerID         string   `codec:"wid"`
	Address          string   `codec:"addr"`
	TaskSlotsTotal   int      `codec:"tst"`
	HighestSeenEpoch uint64   `codec:"hse"`
	RunningTasks     []string `codec:"rt,omitempty"`
}

// RegisterWorkerResponse is the Coordinator's reply to RegisterWorker.
type RegisterWorkerResponse struct {
	Epoch         uint64   `codec:"eid"`
	TasksToCancel []string `codec:"ttc,omitempty"`
	MissingTasks  []string `codec:"mt,omitempty"`
}

// WorkerCommand carries a command from Coordinator to Worker via heartbeat.
type WorkerCommand struct {
	Type   CommandType `codec:"t"`
	JobID  string      `codec:"jid,omitempty"`
	TaskID string      `codec:"tid,omitempty"`
	Data   []byte      `codec:"d,omitempty"`
}
