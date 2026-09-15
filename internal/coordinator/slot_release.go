package coordinator

import "slices"

// releaseTaskSlotLocked returns a terminal task's slot to its worker instead of
// waiting for the next heartbeat. Heartbeats remain authoritative and overwrite
// this count. Releasing only while the task is still listed makes duplicate
// reports unable to add capacity. A late report may arrive after a heartbeat
// already returned the slot, so a release never raises availability above the
// slots not held by tasks this coordinator still has assigned. Callers hold c.mu.
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
	// Never lower availability here: stale entries for tasks whose reports were
	// lost can undercount free slots until the next heartbeat corrects them.
	unassigned := max(0, w.TaskSlotsTotal-len(w.RunningTasks))
	w.TaskSlotsAvailable = max(w.TaskSlotsAvailable, min(w.TaskSlotsAvailable+1, unassigned))
	return true
}
