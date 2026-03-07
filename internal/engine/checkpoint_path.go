package engine

import "fmt"

// CheckpointPath returns the metadata.json path for a checkpoint.
func CheckpointPath(jobID string, checkpointID int64) string {
	return fmt.Sprintf("jobs/%s/checkpoints/chk-%d/metadata.json", jobID, checkpointID)
}

// SavepointPath returns the metadata.json path for a savepoint.
func SavepointPath(jobID string, savepointID int64) string {
	return fmt.Sprintf("jobs/%s/savepoints/sp-%d/metadata.json", jobID, savepointID)
}

// CheckpointDir returns the directory path for a checkpoint.
func CheckpointDir(jobID string, checkpointID int64) string {
	return fmt.Sprintf("jobs/%s/checkpoints/chk-%d/", jobID, checkpointID)
}

// SavepointDir returns the directory path for a savepoint.
func SavepointDir(jobID string, savepointID int64) string {
	return fmt.Sprintf("jobs/%s/savepoints/sp-%d/", jobID, savepointID)
}

// TaskStatePath returns the state directory path for a given subtask index
// within a checkpoint or savepoint directory.
func TaskStatePath(subtaskIndex int) string {
	return fmt.Sprintf("task-%d-state/", subtaskIndex)
}
