package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func (w *Worker) startCheckpointCleanup(parent context.Context) func() {
	ctx, cancel := context.WithCancel(parent)
	queue := make(chan rpc.WorkerCommand, 16)
	done := make(chan struct{})
	w.mu.Lock()
	w.cleanupCommands = queue
	w.mu.Unlock()
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case cmd := <-queue:
				if err := w.deleteCheckpointReplica(ctx, cmd); err != nil {
					w.log.Warn().Err(err).Msg("checkpoint cleanup deferred")
				}
			}
		}
	}()
	return func() { w.mu.Lock(); w.cleanupCommands = nil; w.mu.Unlock(); cancel(); <-done }
}
func (w *Worker) enqueueCheckpointCleanup(cmd rpc.WorkerCommand) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.stopping || cmd.EpochID != w.epoch {
		return
	}
	select {
	case w.cleanupCommands <- cmd:
	default:
	}
}
func (w *Worker) deleteCheckpointReplica(ctx context.Context, cmd rpc.WorkerCommand) error {
	var request rpc.CheckpointCleanupRequest
	if err := protocol.DecodeMsgPack(cmd.Data, &request); err != nil {
		return err
	}
	w.mu.RLock()
	valid := !w.stopping && request.EpochID == w.epoch && cmd.EpochID == w.epoch && request.WorkerID == w.cfg.WorkerID && request.JobID == cmd.JobID && request.TaskID == cmd.TaskID && request.SavepointID != "" && request.CheckpointID != 0
	cfg, client := w.cfg.CheckpointReplica, w.client
	w.mu.RUnlock()
	if !valid || cfg == nil || client == nil {
		return fmt.Errorf("checkpoint cleanup command is not current")
	}
	store, err := engine.NewFileCheckpointStore(cfg.StoreRoot)
	if err != nil {
		return err
	}
	if err := store.Delete(ctx, request.JobID, request.TaskID, request.CheckpointID, request.SnapshotEpoch); err != nil {
		return err
	}
	if _, err := store.CollectArtifacts(ctx, cfg.ArtifactRoot); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var response rpc.AcknowledgeCheckpointResponse
	if err := client.Call(callCtx, rpc.MethodAcknowledgeCheckpointCleanup, request, &response); err != nil {
		return err
	}
	if !response.Accepted {
		return fmt.Errorf("checkpoint cleanup receipt rejected: %s", response.Message)
	}
	return nil
}
