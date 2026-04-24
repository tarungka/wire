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

// Worker connects to a coordinator, registers, and runs a heartbeat loop.
type Worker struct {
	cfg     Config
	client  *rpc.Client
	session *transport.Session
	epoch   uint64
	mu      sync.RWMutex
	tasks   map[string]context.CancelFunc // taskID -> cancel
	log     zerolog.Logger
}

// New creates a new Worker.
func New(cfg Config, log zerolog.Logger) *Worker {
	return &Worker{
		cfg:   cfg,
		tasks: make(map[string]context.CancelFunc),
		log:   log.With().Str("component", "worker").Logger(),
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

	// 4. Start heartbeat loop.
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

// Shutdown cancels running tasks and closes the transport session.
func (w *Worker) Shutdown(_ context.Context) error {
	w.mu.Lock()
	for taskID, cancel := range w.tasks {
		w.log.Info().Str("task_id", taskID).Msg("canceling task")
		cancel()
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
		return
	}

	// Create task context.
	taskCtx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.tasks[cmd.TaskID] = cancel
	w.mu.Unlock()

	w.log.Info().
		Str("task_id", cmd.TaskID).
		Str("job_id", cmd.JobID).
		Str("operator_id", desc.OperatorID).
		Int32("subtask_index", desc.SubtaskIndex).
		Msg("deployed task")

	// Send UpdateTaskStatus(RUNNING) to coordinator.
	go func() {
		defer func() {
			// When the task context is canceled, the task is done.
			<-taskCtx.Done()
		}()

		w.mu.RLock()
		epoch := w.epoch
		w.mu.RUnlock()

		statusReq := &rpc.UpdateTaskStatusRequest{
			JobID:   cmd.JobID,
			TaskID:  cmd.TaskID,
			Status:  rpc.TaskStatusRunning,
			EpochID: epoch,
		}
		if _, err := w.client.UpdateTaskStatus(context.Background(), statusReq); err != nil {
			w.log.Error().Err(err).Str("task_id", cmd.TaskID).Msg("failed to send UpdateTaskStatus")
		}
	}()
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
			if cancel, ok := w.tasks[cmd.TaskID]; ok {
				cancel()
				delete(w.tasks, cmd.TaskID)
			}
			w.mu.Unlock()
		case rpc.CommandTypeTakeSnapshot:
			w.log.Info().Str("task_id", cmd.TaskID).Msg("received TakeSnapshot command (stub)")
		default:
			w.log.Warn().Uint8("type", uint8(cmd.Type)).Msg("unknown command type")
		}
	}
}
