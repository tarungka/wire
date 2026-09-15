package coordinator

import "slices"

// releaseTaskSlotLocked returns a terminal task's slot to its worker instead of
// waiting for the next heartbeat. Heartbeats remain authoritative and overwrite
// this count. Releasing only while the task is still listed makes duplicate or
// late terminal reports unable to add capacity. Callers hold c.mu.
func (c *Coordinator) releaseTaskSlotLocked(workerID, taskID string) bool {
	w := c.workers[workerID]
	if w == nil {
		return false
	}
	i := slices.Index(w.RunningTasks, taskID)
	if i < 0 {
		return false
	}
	// Copy instead of deleting in place: API responses share this backing array
	// after the lock is released.
	w.RunningTasks = slices.Concat(w.RunningTasks[:i], w.RunningTasks[i+1:])
	if w.TaskSlotsAvailable < w.TaskSlotsTotal {
		w.TaskSlotsAvailable++
	}
	return true
}
