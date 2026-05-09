package worker

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

// Config holds worker configuration.
type Config struct {
	WorkerID        string
	CoordinatorAddr string
	ListenAddr      string
	TaskSlots       int
}

// taskHandle tracks a running task so it can be cancelled on demand or
// on worker shutdown.
type taskHandle struct {
	cancel context.CancelFunc
}

// Worker connects to a coordinator, registers, and runs a heartbeat loop.
// Deployed tasks are resolved against the Worker's Registry and executed
// via taskExecutor.
type Worker struct {
	cfg      Config
	reg      *Registry
	executor *taskExecutor
	client   *rpc.Client
	session  *transport.Session
	epoch    uint64
	mu       sync.RWMutex
	tasks    map[string]*taskHandle // taskID -> handle
	log      zerolog.Logger
}

// New creates a new Worker using the package-level default registry. User
// code should call worker.RegisterSource/RegisterMap/RegisterSink before
// constructing the Worker.
func New(cfg Config, log zerolog.Logger) *Worker {
	return NewWithRegistry(cfg, defaultRegistry, log)
}

// NewWithRegistry creates a new Worker with an explicit registry. Useful for
// tests that need isolation from the package-level default registry.
func NewWithRegistry(cfg Config, reg *Registry, log zerolog.Logger) *Worker {
	if reg == nil {
		reg = defaultRegistry
	}
	return &Worker{
		cfg:      cfg,
		reg:      reg,
		executor: newTaskExecutor(reg),
		tasks:    make(map[string]*taskHandle),
		log:      log.With().Str("component", "worker").Logger(),
	}
}

// Run connects to the coordinator, registers, and starts the heartbeat loop.
// It blocks until ctx is canceled or an unrecoverable error occurs.
func (w *Worker) Run(ctx context.Context) error {
	// Resolve worker ID.
	workerID := w.cfg.WorkerID
	if workerID == "" {
		workerID, _ = os.Hostname()
		if workerID == "" {
			workerID = "wire-worker-1"
		}
	}
	w.cfg.WorkerID = workerID

	w.log.Info().
		Str("worker_id", workerID).
		Str("coordinator", w.cfg.CoordinatorAddr).
		Int("task_slots", w.cfg.TaskSlots).
		Msg("connecting to coordinator")

	// 1. Establish transport session.
	tcfg := transport.DefaultConfig()
	session, err := transport.NewClientSession(w.cfg.CoordinatorAddr, tcfg)
	if err != nil {
		return fmt.Errorf("worker: connect to coordinator: %w", err)
	}
	w.session = session

	// 2. Create RPC client.
	rpcCfg := rpc.DefaultConfig()
	w.client = rpc.NewClient(session.YamuxSession(), rpcCfg)

	// 3. Register with coordinator.
	regReq := &rpc.RegisterWorkerRequest{
		WorkerID:       workerID,
		Address:        w.cfg.ListenAddr,
		TaskSlotsTotal: w.cfg.TaskSlots,
	}
	regResp, err := w.client.RegisterWorker(ctx, regReq)
	if err != nil {
		_ = session.Close()
		return fmt.Errorf("worker: register: %w", err)
	}

	w.mu.Lock()
	w.epoch = regResp.Epoch
	w.mu.Unlock()

	w.log.Info().
		Uint64("epoch", regResp.Epoch).
		Int("tasks_to_cancel", len(regResp.TasksToCancel)).
		Int("missing_tasks", len(regResp.MissingTasks)).
		Msg("registered with coordinator")

	// Handle reconciliation response.
	for _, taskID := range regResp.TasksToCancel {
		w.log.Warn().Str("task_id", taskID).Msg("coordinator requested task cancellation (orphaned)")
	}

	// 4. Open the WatchCommands push stream BEFORE starting the heartbeat
	// loop. The coordinator will push deploy/cancel commands directly on
	// this stream — orders of magnitude faster than the heartbeat-tick
	// dispatch model. Heartbeats still run for liveness and as a fallback
	// when the stream is unavailable.
	go w.runWatchCommands(ctx)

	// 5. Start heartbeat loop.
	heartbeat := rpc.NewHeartbeatSender(
		w.client,
		rpcCfg,
		w.buildHeartbeatRequest,
		w.handleCommands,
		rpc.WithContactLostCallback(func() {
			w.log.Error().Msg("lost contact with coordinator")
		}),
	)

	w.log.Info().Msg("heartbeat loop started")
	heartbeat.Run(ctx)

	return nil
}

// runWatchCommands maintains a long-lived RPC stream to the coordinator
// that delivers worker commands as soon as they're enqueued (instead of
// waiting for the next heartbeat tick to pull them). It reconnects with
// a small backoff if the stream errors so transient session blips don't
// permanently stall command dispatch.
func (w *Worker) runWatchCommands(ctx context.Context) {
	const baseBackoff = 200 * time.Millisecond
	const maxBackoff = 5 * time.Second
	backoff := baseBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		w.mu.RLock()
		epoch := w.epoch
		w.mu.RUnlock()

		req := &rpc.WatchCommandsRequest{
			WorkerID: w.workerID(),
			EpochID:  epoch,
		}
		frames, cancel, err := w.client.CallStream(ctx, rpc.MethodWatchCommands, req)
		if err != nil {
			w.log.Warn().Err(err).Msg("WatchCommands open failed, retrying")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		w.log.Info().Msg("WatchCommands stream open")
		backoff = baseBackoff

	streamLoop:
		for {
			select {
			case <-ctx.Done():
				cancel()
				return
			case sf, ok := <-frames:
				if !ok {
					// Stream closed cleanly — reconnect.
					break streamLoop
				}
				if sf.Err != nil {
					w.log.Warn().Err(sf.Err).Msg("WatchCommands stream error, reconnecting")
					break streamLoop
				}
				var cmd rpc.WorkerCommand
				if err := protocol.DecodeMsgPack(sf.Frame.Payload, &cmd); err != nil {
					w.log.Error().Err(err).Msg("decode pushed WorkerCommand")
					continue
				}
				// Reuse the same handler the heartbeat path uses so
				// either delivery channel is interchangeable.
				w.handleCommands([]rpc.WorkerCommand{cmd})
			}
		}
		cancel()
	}
}

// workerID returns the resolved worker ID — the explicit cfg.WorkerID
// when set, otherwise the hostname (matching the registration path).
func (w *Worker) workerID() string {
	if w.cfg.WorkerID != "" {
		return w.cfg.WorkerID
	}
	hn, _ := os.Hostname()
	return hn
}

// Shutdown cancels running tasks and closes the transport session.
func (w *Worker) Shutdown(_ context.Context) error {
	w.mu.Lock()
	for taskID, h := range w.tasks {
		w.log.Info().Str("task_id", taskID).Msg("canceling task")
		h.cancel()
	}
	w.mu.Unlock()

	if w.session != nil {
		return w.session.Close()
	}
	return nil
}

// buildHeartbeatRequest constructs a HeartbeatRequest from the worker's current state.
func (w *Worker) buildHeartbeatRequest() *rpc.HeartbeatRequest {
	w.mu.RLock()
	activeSlots := int32(len(w.tasks))
	epoch := w.epoch
	w.mu.RUnlock()

	return &rpc.HeartbeatRequest{
		WorkerID:  w.cfg.WorkerID,
		EpochID:   epoch,
		Timestamp: time.Now().UnixMilli(),
		Load: &rpc.WorkerLoad{
			ActiveSlots: activeSlots,
			TotalSlots:  int32(w.cfg.TaskSlots),
		},
	}
}

// handleDeployTask processes a DeployTask command from the coordinator.
// It decodes the TaskDescriptor, instantiates the operator chain via the
// registry, and drives execution in a background goroutine. Task status
// transitions are reported back to the coordinator via UpdateTaskStatus.
func (w *Worker) handleDeployTask(cmd rpc.WorkerCommand) {
	// Idempotent: ignore if task already exists.
	w.mu.RLock()
	_, exists := w.tasks[cmd.TaskID]
	w.mu.RUnlock()
	if exists {
		w.log.Debug().Str("task_id", cmd.TaskID).Msg("ignoring duplicate DeployTask")
		return
	}

	// Decode task descriptor.
	var desc rpc.TaskDescriptor
	if err := protocol.DecodeMsgPack(cmd.Data, &desc); err != nil {
		w.log.Error().Err(err).Str("task_id", cmd.TaskID).Msg("failed to decode task descriptor")
		w.reportTaskFailed(cmd.JobID, cmd.TaskID, fmt.Errorf("decode descriptor: %w", err))
		return
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.tasks[cmd.TaskID] = &taskHandle{cancel: cancel}
	w.mu.Unlock()

	taskLog := w.log.With().
		Str("task_id", cmd.TaskID).
		Str("job_id", cmd.JobID).
		Str("operator_id", desc.OperatorID).
		Int32("subtask_index", desc.SubtaskIndex).
		Logger()

	taskLog.Info().Int("chain_len", len(desc.OperatorChain)).Msg("deployed task")

	go w.runTask(taskCtx, cmd.JobID, cmd.TaskID, desc, taskLog)
}

// runTask reports Running on entry, drives the executor, and reports
// Finished/Failed on exit. Always removes the task from w.tasks when done.
func (w *Worker) runTask(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor, log zerolog.Logger) {
	defer func() {
		w.mu.Lock()
		delete(w.tasks, taskID)
		w.mu.Unlock()
	}()

	w.reportTaskStatus(jobID, taskID, rpc.TaskStatusRunning, nil)

	err := w.executor.run(ctx, jobID, taskID, desc, log)

	switch {
	case err == nil:
		log.Info().Msg("task finished")
		w.reportTaskStatus(jobID, taskID, rpc.TaskStatusFinished, nil)
	case ctx.Err() != nil:
		log.Info().Msg("task canceled")
		w.reportTaskStatus(jobID, taskID, rpc.TaskStatusCanceled, nil)
	default:
		log.Error().Err(err).Msg("task failed")
		w.reportTaskFailed(jobID, taskID, err)
	}
}

// reportTaskStatus sends an UpdateTaskStatus RPC with no failure info.
func (w *Worker) reportTaskStatus(jobID, taskID string, status rpc.TaskStatus, failure *rpc.TaskFailureInfo) {
	w.mu.RLock()
	epoch := w.epoch
	w.mu.RUnlock()

	req := &rpc.UpdateTaskStatusRequest{
		JobID:   jobID,
		TaskID:  taskID,
		Status:  status,
		EpochID: epoch,
		Failure: failure,
	}
	if _, err := w.client.UpdateTaskStatus(context.Background(), req); err != nil {
		w.log.Error().Err(err).
			Str("task_id", taskID).
			Str("status", status.String()).
			Msg("failed to send UpdateTaskStatus")
	}
}

// reportTaskFailed sends an UpdateTaskStatus RPC with status=Failed and the
// error message populated in the failure info.
func (w *Worker) reportTaskFailed(jobID, taskID string, err error) {
	w.reportTaskStatus(jobID, taskID, rpc.TaskStatusFailed, &rpc.TaskFailureInfo{
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	})
}

// handleCommands dispatches coordinator commands received via heartbeat responses.
func (w *Worker) handleCommands(cmds []rpc.WorkerCommand) {
	for _, cmd := range cmds {
		switch cmd.Type {
		case rpc.CommandTypeDeployTask:
			w.handleDeployTask(cmd)
		case rpc.CommandTypeCancelTask:
			w.log.Info().Str("task_id", cmd.TaskID).Msg("received CancelTask command")
			w.mu.Lock()
			if h, ok := w.tasks[cmd.TaskID]; ok {
				h.cancel()
				// Don't delete here — runTask's defer cleans up.
			}
			w.mu.Unlock()
		case rpc.CommandTypeTakeSnapshot:
			w.log.Info().Str("task_id", cmd.TaskID).Msg("received TakeSnapshot command (stub — Phase 4)")
		default:
			w.log.Warn().Uint8("type", uint8(cmd.Type)).Msg("unknown command type")
		}
	}
}
