package coordinator

import "fmt"

// Key prefixes for the coordinator metadata keyspace.
const (
	JobsPrefix    = "jobs/"
	WorkersPrefix = "workers/"
	ClusterPrefix = "cluster/"
)

// JobMetaKey returns the PebbleDB key for a job's metadata.
func JobMetaKey(jobID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/meta", jobID)
}

// JobGraphKey returns the PebbleDB key for a job's execution graph.
func JobGraphKey(jobID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/graph", jobID)
}

// JobAssignmentsKey returns the PebbleDB key for a job's task-to-worker assignments.
func JobAssignmentsKey(jobID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/assignments", jobID)
}

// CheckpointKey returns the PebbleDB key for a specific checkpoint of a job.
func CheckpointKey(jobID string, cpID uint64) []byte {
	return fmt.Appendf(nil, "jobs/%s/checkpoints/%016x", jobID, cpID)
}

// LatestCheckpointKey returns the PebbleDB key for the latest checkpoint pointer of a job.
func LatestCheckpointKey(jobID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/checkpoints/latest", jobID)
}

// WorkerMetaKey returns the PebbleDB key for a worker's metadata.
func WorkerMetaKey(workerID string) []byte {
	return fmt.Appendf(nil, "workers/%s/meta", workerID)
}

// WorkerTasksKey returns the PebbleDB key for a worker's running task list.
func WorkerTasksKey(workerID string) []byte {
	return fmt.Appendf(nil, "workers/%s/tasks", workerID)
}

// ClusterConfigKey returns the PebbleDB key for the cluster configuration.
func ClusterConfigKey() []byte {
	return []byte("cluster/config")
}

// ClusterEpochKey returns the PebbleDB key for the current cluster epoch.
func ClusterEpochKey() []byte {
	return []byte("cluster/epoch")
}

// TaskStatusKey returns the PebbleDB key for a task's status.
func TaskStatusKey(taskID string) []byte {
	return fmt.Appendf(nil, "tasks/%s/status", taskID)
}

// ClusterLeaderKey returns the PebbleDB key for the current leader info.
func ClusterLeaderKey() []byte {
	return []byte("cluster/leader")
}

// SavepointKey returns the PebbleDB key for a specific savepoint of a job.
func SavepointKey(jobID, spID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/savepoints/%s", jobID, spID)
}

// SavepointsPrefix returns the PebbleDB key prefix for all savepoints of a job.
func SavepointsPrefix(jobID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/savepoints/", jobID)
}

// JobConfigKey returns the PebbleDB key for a job's raw configuration.
func JobConfigKey(jobID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/config", jobID)
}
