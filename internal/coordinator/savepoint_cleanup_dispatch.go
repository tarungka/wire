package coordinator

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func (c *Coordinator) dispatchSavepointCleanup(ctx context.Context) {
	c.mu.RLock()
	jobs := make([]string, 0, len(c.jobs))
	for id := range c.jobs {
		jobs = append(jobs, id)
	}
	ready := c.readyLocked()
	c.mu.RUnlock()
	if !ready {
		return
	}
	for _, jobID := range jobs {
		if ctx.Err() != nil {
			return
		}
		var pending []SavepointCleanup
		err := c.store.PrefixScan([]byte(fmt.Sprintf("jobs/%s/savepoint-cleanup/", jobID)), func(_, raw []byte) bool {
			if ctx.Err() != nil {
				return false
			}
			var cleanup SavepointCleanup
			if protocol.DecodeMsgPack(raw, &cleanup) == nil && cleanup.JobID == jobID && cleanup.CompletedAt.IsZero() {
				pending = append(pending, cleanup)
			}
			return true
		})
		if err != nil {
			c.log.Warn().Err(err).Str("job_id", jobID).Msg("cannot read checkpoint cleanup requests")
			continue
		}
		c.mu.Lock()
		if !c.readyLocked() {
			c.mu.Unlock()
			return
		}
		for _, cleanup := range pending {
			for task, address := range cleanup.Replicas {
				if cleanup.Completed[task] || address == "" {
					continue
				}
				for workerID, worker := range c.workers {
					if worker.CheckpointAddress != address || worker.Lost || worker.Removed || worker.LastHeartbeat.IsZero() || time.Since(worker.LastHeartbeat) >= c.config.WorkerTimeout {
						continue
					}
					request := rpc.CheckpointCleanupRequest{WorkerID: workerID, EpochID: c.epoch, JobID: cleanup.JobID, SavepointID: cleanup.SavepointID, TaskID: task, CheckpointID: cleanup.CheckpointID, SnapshotEpoch: cleanup.EpochID}
					raw, err := protocol.EncodeMsgPack(request)
					if err != nil {
						continue
					}
					duplicate := false
					for _, cmd := range c.pendingCmds[workerID] {
						if cmd.Type == rpc.CommandTypeDeleteCheckpoint && bytes.Equal(cmd.Data, raw) {
							duplicate = true
							break
						}
					}
					// Bound retries queued for slow/disconnected workers. Durable requests
					// are rescanned after receipts, reconnects and leadership recovery.
					if !duplicate && len(c.pendingCmds[workerID]) < 64 {
						c.enqueueCommandLocked(workerID, rpc.WorkerCommand{Type: rpc.CommandTypeDeleteCheckpoint, JobID: cleanup.JobID, TaskID: task, EpochID: c.epoch, Data: raw})
					}
					break
				}
			}
		}
		c.mu.Unlock()
	}
}
