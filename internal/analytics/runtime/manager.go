package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tarungka/wire/internal/analytics"
	"github.com/tarungka/wire/internal/analytics/operators"
	"github.com/tarungka/wire/internal/analytics/planner"
)

// CreateOperator is a helper to create an operator from the registry.
func CreateOperator(typeName string, config map[string]interface{}) (analytics.Operator, error) {
	return operators.Create(typeName, config)
}

// JobManager coordinates the execution of analytical jobs.
type JobManager struct {
	ctx           context.Context
	planner       *planner.Planner
	nodeID        string
	WorkerManager *WorkerManager

	mu   sync.RWMutex
	jobs map[string]*ActiveJob
}

type ActiveJob struct {
	ID           string
	PhysicalPlan *planner.PhysicalPlan
}

// NewJobManager creates a new JobManager.
func NewJobManager(ctx context.Context, nodeID string, clusterNodes []string, wm *WorkerManager) *JobManager {
	return &JobManager{
		ctx:           ctx,
		nodeID:        nodeID,
		planner:       planner.NewPlanner(clusterNodes),
		WorkerManager: wm,
		jobs:          make(map[string]*ActiveJob),
	}
}

// SubmitJob submits a new job to the cluster.
func (jm *JobManager) SubmitJob(jobID string, logicalPlan *planner.LogicalPlan) error {
	physicalPlan, err := jm.planner.Plan(logicalPlan)
	if err != nil {
		return fmt.Errorf("failed to plan physical execution: %w", err)
	}

	jm.mu.Lock()
	jm.jobs[jobID] = &ActiveJob{
		ID:           jobID,
		PhysicalPlan: physicalPlan,
	}
	jm.mu.Unlock()

	// Deploy tasks
	for _, task := range physicalPlan.Tasks {
		if task.NodeID == jm.nodeID {
			// Local deployment
			lNode := logicalPlan.Nodes[task.OperatorID]
			if err := jm.deployLocalTask(task, lNode.Operator); err != nil {
				return err
			}
		} else {
			// TODO: Remote deployment via cluster.Client
		}
	}

	return nil
}

func (jm *JobManager) deployLocalTask(task *planner.Task, op analytics.Operator) error {
	if jm.WorkerManager == nil {
		return fmt.Errorf("WorkerManager not set")
	}
	return jm.WorkerManager.DeployTask(task, op)
}

// WorkerManager manages task execution on a single node.
type WorkerManager struct {
	ctx    context.Context
	nodeID string

	mu    sync.RWMutex
	tasks map[string]*TaskExecutor
}

type TaskExecutor struct {
	TaskID   string
	Operator analytics.Operator
	Input    *BoundedStream
	Output   *BoundedStream
	cancel   context.CancelFunc
}

// NewWorkerManager creates a new WorkerManager.
func NewWorkerManager(ctx context.Context, nodeID string) *WorkerManager {
	return &WorkerManager{
		ctx:    ctx,
		nodeID: nodeID,
		tasks:  make(map[string]*TaskExecutor),
	}
}

// DeployTask deploys a task to this worker.
func (wm *WorkerManager) DeployTask(task *planner.Task, op analytics.Operator) error {
	ctx, cancel := context.WithCancel(wm.ctx)

	executor := &TaskExecutor{
		TaskID:   task.ID,
		Operator: op,
		// In a real system, these would be configured based on task requirements
		Input:  NewBoundedStream(1024, 800, 200),
		Output: NewBoundedStream(1024, 800, 200),
		cancel: cancel,
	}

	wm.mu.Lock()
	wm.tasks[task.ID] = executor
	wm.mu.Unlock()

	go executor.Run(ctx)

	return nil
}

func (e *TaskExecutor) Run(ctx context.Context) {
	// 1. Open operator
	// 2. Start consuming from input stream
	// 3. Process records and emit to output stream
	// 4. Handle context cancellation

	defer e.Operator.Close()

	// Create context for operator
	opCtx := &DefaultOperatorContext{
		Context: ctx,
		id:      e.TaskID,
	}

	if err := e.Operator.Open(opCtx); err != nil {
		fmt.Printf("failed to open operator %s: %v\n", e.TaskID, err)
		return
	}

	for record := range e.Input.Consume() {
		if record.IsBarrier() {
			// Handle checkpoint barrier
			if err := e.handleBarrier(record); err != nil {
				fmt.Printf("error handling barrier in task %s: %v\n", e.TaskID, err)
			}
			// Forward barrier to output
			if err := e.Output.Emit(record); err != nil {
				fmt.Printf("error emitting barrier in task %s: %v\n", e.TaskID, err)
			}
		} else {
			if err := e.Operator.ProcessElement(record, e.Output); err != nil {
				fmt.Printf("error processing element in task %s: %v\n", e.TaskID, err)
			}
		}
		e.Input.Acknowledge()

		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func (e *TaskExecutor) handleBarrier(record *analytics.Record) error {
	fmt.Printf("[Task %s] Received barrier for checkpoint %d\n", e.TaskID, record.CheckpointID)
	// TODO: Trigger state snapshot and persist to BadgerDB
	// Once state is persisted, we can notify the JobManager via Raft
	return nil
}

type DefaultOperatorContext struct {
	context.Context
	id string
}

func (c *DefaultOperatorContext) OperatorID() string { return c.id }
func (c *DefaultOperatorContext) GetState(name string) analytics.StateStore {
	// TODO: implement BadgerDB-backed state store
	return nil
}
func (c *DefaultOperatorContext) SetTimer(timestamp time.Time, callback func(time.Time)) {
	// TODO: implement timer service
}
