package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

// Config holds worker configuration.
type Config struct {
	CheckpointReplica *CheckpointReplicaConfig
	TaskSlot          *engine.TaskSlotConfig // Nil selects engine defaults.
	WorkerID          string
	CoordinatorAddr   string
	ListenAddr        string
	TaskSlots         int
}

// taskHandle tracks a running task so it can be cancelled on demand or
// on worker shutdown.
type taskHandle struct {
	done       chan struct{}
	attemptID  string
	cancel     context.CancelFunc
	jobID      string
	epoch      uint64
	checkpoint *taskCheckpointRuntime
}

// Worker connects to a coordinator, registers, and runs a heartbeat loop.
// Deployed tasks are resolved against the Worker's Registry and executed
// via taskExecutor.
type Worker struct {
	cfg          Config
	reg          *Registry
	executor     *taskExecutor
	client       *rpc.Client
	session      *transport.Session
	data         *transport.Mux
	epoch        uint64
	mu           sync.RWMutex
	stopping     bool
	closeReplica func()
	tasks        map[string]*taskHandle // taskID -> handle
	log          zerolog.Logger
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
	executor := newTaskExecutor(reg)
	if cfg.TaskSlot != nil {
		copied := *cfg.TaskSlot
		executor.taskConfig = &copied
	}
	return &Worker{
		cfg:      cfg,
		reg:      reg,
		executor: executor,
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
	var checkpointAddress string
	if w.cfg.CheckpointReplica != nil {
		replicaConfig := *w.cfg.CheckpointReplica
		if replicaConfig.AuthorizeFetch == nil {
			replicaConfig.AuthorizeFetch = w.authorizeCheckpointFetch
		}
		if replicaConfig.Authorize == nil {
			replicaConfig.Authorize = w.authorizeCheckpointReplica
		}
		addr, closeReplica, err := startCheckpointReplicaService(ctx, replicaConfig)
		if err != nil {
			return fmt.Errorf("worker: checkpoint replica listener: %w", err)
		}
		defer closeReplica()
		w.mu.Lock()
		if w.stopping {
			w.mu.Unlock()
			return fmt.Errorf("worker is shutting down")
		}
		w.closeReplica = closeReplica
		w.mu.Unlock()
		checkpointAddress = addr
		w.log.Info().Str("addr", addr).Msg("checkpoint replica listener started")
	}
	dataConfig := transport.DefaultConfig()
	dataConfig.TaskRegistrationTimeout = 5 * time.Second
	dataConfig.NodeID = workerID
	dataConfig.ListenAddr = w.cfg.ListenAddr
	if dataConfig.ListenAddr == "" {
		dataConfig.ListenAddr = "127.0.0.1:0"
	}
	data := transport.NewMux(dataConfig)
	if err := data.Listen(ctx); err != nil {
		return fmt.Errorf("worker: data listener: %w", err)
	}
	defer data.Close()
	w.mu.Lock()
	w.data = data
	w.executor.data = data
	w.cfg.ListenAddr = data.ListenAddr()
	w.mu.Unlock()
	defer w.Shutdown(context.Background())

	for ctx.Err() == nil {
		w.mu.RLock()
		stopping := w.stopping
		w.mu.RUnlock()
		if stopping {
			return nil
		}
		err := w.runCoordinatorSession(ctx, workerID, checkpointAddress)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errReconnectTaskJoin) {
			return err
		}
		w.log.Warn().Err(err).Msg("coordinator session ended; reconnecting")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil
}

var errReconnectTaskJoin = errors.New("old tasks did not stop before coordinator reconnect")

func (w *Worker) runCoordinatorSession(ctx context.Context, workerID, checkpointAddress string) (retErr error) {
	ctx, stopSession := context.WithCancel(ctx)
	defer stopSession()
	w.log.Info().
		Str("worker_id", workerID).
		Str("coordinator", w.cfg.CoordinatorAddr).
		Int("task_slots", w.cfg.TaskSlots).
		Msg("connecting to coordinator")

	// 1. Establish transport session.
	tcfg := transport.DefaultConfig()
	session, err := transport.NewClientSessionContext(ctx, w.cfg.CoordinatorAddr, tcfg)
	if err != nil {
		return fmt.Errorf("worker: connect to coordinator: %w", err)
	}
	w.mu.Lock()
	w.session = session
	w.mu.Unlock()
	defer func() {
		_ = session.Close()
		if err := w.joinTasksForReconnect(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()

	// 2. Create RPC client.
	rpcCfg := rpc.DefaultConfig()
	w.mu.Lock()
	w.client = rpc.NewClient(session.YamuxSession(), rpcCfg)
	w.mu.Unlock()

	// 3. Register with coordinator.
	w.mu.RLock()
	highestEpoch := w.epoch
	w.mu.RUnlock()
	regReq := &rpc.RegisterWorkerRequest{
		HighestSeenEpoch:  highestEpoch,
		CheckpointAddress: checkpointAddress,
		WorkerID:          workerID,
		Address:           w.cfg.ListenAddr,
		TaskSlotsTotal:    w.cfg.TaskSlots,
	}
	regResp, err := w.client.RegisterWorker(ctx, regReq)
	if err != nil {
		_ = session.Close()
		return fmt.Errorf("worker: register: %w", err)
	}

	if regResp.Epoch < highestEpoch {
		return fmt.Errorf("coordinator registration returned stale epoch %d", regResp.Epoch)
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
	watchCtx, stopWatch := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		w.runWatchCommands(watchCtx)
	}()
	// Stop command admission and join the reader before deferred task shutdown
	// closes shared transports. No pushed deployment can race after this join.
	defer func() {
		stopWatch()
		<-watchDone
	}()

	// 5. Start heartbeat loop.
	heartbeat := rpc.NewHeartbeatSender(
		w.client,
		rpcCfg,
		w.buildHeartbeatRequest,
		w.handleCommands,
		rpc.WithNewEpochCallback(func(epoch uint64) {
			w.log.Warn().Uint64("epoch", epoch).Msg("coordinator epoch changed; stopping old tasks")
			w.cancelTasksOnContactLoss()
			stopSession()
		}),
		rpc.WithContactLostCallback(func() {
			w.log.Error().Msg("lost contact with coordinator")
			w.cancelTasksOnContactLoss()
			stopSession()
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
func (w *Worker) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var tasks []<-chan struct{}
	w.mu.Lock()
	w.stopping = true
	for taskID, h := range w.tasks {
		if h.done != nil {
			tasks = append(tasks, h.done)
		}
		w.log.Info().Str("task_id", taskID).Msg("canceling task")
		h.cancel()
	}
	w.mu.Unlock()

	w.mu.RLock()
	data, session, closeReplica := w.data, w.session, w.closeReplica
	w.mu.RUnlock()
	if closeReplica != nil {
		closeReplica()
	}
	var err error
	if data != nil {
		err = data.Close()
	}
	if session != nil {
		err = errors.Join(err, session.Close())
	}
	for _, done := range tasks {
		select {
		case <-done:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
	return err
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
	stopping := w.stopping
	w.mu.RUnlock()
	if exists || stopping {
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
	// Heartbeat and push delivery may both deploy the same task. Admission
	// must be atomic with duplicate detection and worker shutdown.
	if _, exists := w.tasks[cmd.TaskID]; exists || w.stopping {
		w.mu.Unlock()
		cancel()
		return
	}
	if desc.EpochID != 0 && desc.EpochID != w.epoch {
		w.mu.Unlock()
		cancel()
		w.log.Warn().Uint64("epoch", desc.EpochID).Str("task_id", cmd.TaskID).Msg("ignoring deployment from a different coordinator epoch")
		return
	}
	handle := &taskHandle{done: make(chan struct{}), cancel: cancel, jobID: cmd.JobID, epoch: desc.EpochID, attemptID: desc.AttemptID}
	if desc.CheckpointReplicaAddress != "" || desc.RestoreCheckpoint != nil || desc.RestoreRescale != nil {
		handle.checkpoint = &taskCheckpointRuntime{triggers: make(chan engine.CheckpointTrigger, 1), decisions: make(chan engine.ControlMsg, 16)}
		for _, operator := range desc.OperatorChain {
			if operator.Type == rpc.OperatorTypeSource {
				handle.checkpoint.source = true
			}
		}
	}
	w.tasks[cmd.TaskID] = handle
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

// runTask reports Running after initialization, drives the executor, and reports
// Finished/Failed on exit. Always removes the task from w.tasks when done.
func (w *Worker) runTask(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor, log zerolog.Logger) {
	w.mu.RLock()
	handle := w.tasks[taskID]
	w.mu.RUnlock()
	defer func() {
		if handle != nil && handle.done != nil {
			close(handle.done)
		}
	}()
	defer func() {
		w.mu.Lock()
		delete(w.tasks, taskID)
		w.mu.Unlock()
	}()

	checkpoint, cleanup, err := w.prepareTaskCheckpoint(ctx, jobID, taskID, desc)
	if err != nil {
		w.reportTaskFailed(jobID, taskID, err)
		return
	}
	defer cleanup()
	err = w.executor.run(ctx, jobID, taskID, desc, log, func() {
		w.reportTaskStatus(jobID, taskID, rpc.TaskStatusRunning, nil)
	}, checkpoint)

	switch {
	case ctx.Err() != nil:
		log.Info().Msg("task canceled")
		w.reportTaskStatus(jobID, taskID, rpc.TaskStatusCanceled, nil)
	case err == nil:
		log.Info().Msg("task finished")
		w.reportTaskStatus(jobID, taskID, rpc.TaskStatusFinished, nil)
	default:
		log.Error().Err(err).Msg("task failed")
		w.reportTaskFailed(jobID, taskID, err)
	}
}

// reportTaskStatus sends an UpdateTaskStatus RPC with no failure info.
func (w *Worker) reportTaskStatus(jobID, taskID string, status rpc.TaskStatus, failure *rpc.TaskFailureInfo) {
	w.mu.RLock()
	epoch := w.epoch
	attemptID := ""
	if handle := w.tasks[taskID]; handle != nil && handle.jobID == jobID {
		epoch = handle.epoch
		attemptID = handle.attemptID
	}
	w.mu.RUnlock()

	req := &rpc.UpdateTaskStatusRequest{
		AttemptID: attemptID,
		WorkerID:  w.cfg.WorkerID,
		JobID:     jobID,
		TaskID:    taskID,
		Status:    status,
		EpochID:   epoch,
		Failure:   failure,
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
	var panicErr *engine.OperatorPanicError
	var stack string
	if errors.As(err, &panicErr) {
		stack = panicErr.Stack
	}
	w.reportTaskStatus(jobID, taskID, rpc.TaskStatusFailed, &rpc.TaskFailureInfo{
		ErrorMessage: err.Error(),
		StackTrace:   stack,
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
			if h, ok := w.tasks[cmd.TaskID]; ok && h.jobID == cmd.JobID && h.epoch == cmd.EpochID && h.attemptID == cmd.AttemptID {
				h.cancel()
				// Don't delete here — runTask's defer cleans up.
			}
			w.mu.Unlock()
		case rpc.CommandTypeTakeSnapshot, rpc.CommandTypeCommitCheckpoint, rpc.CommandTypeAbortCheckpoint:
			w.handleCheckpointCommand(cmd)
		default:
			w.log.Warn().Uint8("type", uint8(cmd.Type)).Msg("unknown command type")
		}
	}
}

// cancelTasksOnContactLoss stops old executions when coordinator authority can
// no longer be confirmed. Recovery must deploy a new attempt explicitly.
func (w *Worker) cancelTasksOnContactLoss() {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, handle := range w.tasks {
		handle.cancel()
	}
}

func (w *Worker) joinTasksForReconnect() error {
	w.mu.RLock()
	var tasks []<-chan struct{}
	for _, handle := range w.tasks {
		handle.cancel()
		if handle.done != nil {
			tasks = append(tasks, handle.done)
		}
	}
	w.mu.RUnlock()
	drain := engine.DefaultDrainTimeout
	if w.executor.taskConfig != nil && w.executor.taskConfig.DrainTimeout > 0 {
		drain = w.executor.taskConfig.DrainTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), drain+5*time.Second)
	defer cancel()
	for _, done := range tasks {
		select {
		case <-done:
		case <-ctx.Done():
			return errReconnectTaskJoin
		}
	}
	return nil
}
