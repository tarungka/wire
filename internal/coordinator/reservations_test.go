package coordinator

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestReservedCapacitySurvivesHeartbeatAccounting(t *testing.T) {
	c, _ := newTestCoordinator(t)
	peer := rpc.NewClient(nil, rpc.DefaultConfig())
	c.workers["worker"] = &WorkerMeta{ID: "worker", TaskSlotsTotal: 2, TaskSlotsAvailable: 0, LastHeartbeat: time.Now()}
	assignments := map[string][]rpc.TaskDescriptor{"worker": {{TaskID: "one"}, {TaskID: "two"}}}
	if c.assignmentsLiveLocked(assignments, time.Now()) {
		t.Fatal("unreserved overbooking")
	}
	if !c.assignmentsLiveLocked(assignments, time.Now(), map[string]*rpc.Client{"worker": peer}) {
		t.Fatal("reservation counted twice after heartbeat")
	}
}

func TestTerminalTaskRetryCannotResurrectAttempt(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1, LastHeartbeat: time.Now()}
	job := slotReleaseJob(t, "job")
	c.jobs[job.ID] = job
	c.scheduleJob(job)
	task, attempt := assignedTask(t, store, job.ID)
	sendTaskStatus(t, c, job.ID, task, attempt, rpc.TaskStatusFailed)
	sendTaskStatus(t, c, job.ID, task, attempt, rpc.TaskStatusRunning)
	if c.taskStatuses[task] != rpc.TaskStatusFailed {
		t.Fatal("late retry resurrected task")
	}
}

func TestRPCIdentityMatchesVerifiedCertificate(t *testing.T) {
	ctx := context.WithValue(context.Background(), workerCertificateKey{}, "worker")
	for _, method := range []rpc.MethodID{rpc.MethodRegisterWorker, rpc.MethodHeartbeat, rpc.MethodUpdateTaskStatus, rpc.MethodAcknowledgeCheckpoint, rpc.MethodWatchCommands, rpc.MethodAuthorizeCheckpointReplica, rpc.MethodAuthorizeCheckpointFetch, rpc.MethodAcknowledgeCheckpointCleanup} {
		for _, name := range []string{"worker", "other", ""} {
			payload := encode(t, struct {
				WorkerID        string `codec:"wid"`
				ReplicaWorkerID string `codec:"rwid"`
			}{name, name})
			if err := checkWorkerIdentity(ctx, method, payload); (err == nil) != (name == "worker") {
				t.Fatalf("identity=%q method=%v error=%v", name, method, err)
			}
		}
	}
}

func TestReservationFailureDoesNotPublishDeployment(t *testing.T) {
	c, store := newTestCoordinator(t)
	a, b := net.Pipe()
	caller, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(rpc.DefaultConfig())
	server.Register(rpc.MethodRequestTaskSlots, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var req rpc.RequestTaskSlotsRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, err.Error())
		}
		if req.Release {
			return &rpc.RequestTaskSlotsResponse{}, nil
		}
		return nil, rpc.NewRPCError(rpc.ErrCodeInsufficientSlots, "worker full")
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, peer) }()
	defer func() { cancel(); _ = caller.Close(); server.Stop(); <-done }()
	c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1, LastHeartbeat: time.Now(), SupportsReservations: true, RPCPeerEpoch: c.epoch, RPCClient: rpc.NewClient(caller, rpc.DefaultConfig())}
	job := slotReleaseJob(t, "job")
	c.jobs[job.ID] = job
	c.scheduleJob(job)
	if job.Status != JobCreated {
		t.Fatalf("refused placement changed job to %v", job.Status)
	}
	raw, err := store.Get(JobAssignmentsKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatal("unreserved deployment was persisted")
	}
	if len(c.workers["worker"].RunningTasks) != 0 {
		t.Fatal("unreserved task was assigned")
	}
}
