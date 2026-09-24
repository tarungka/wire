package worker

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// statusRecorder is an in-memory coordinator that records task status reports.
type statusRecorder struct {
	mu      sync.Mutex
	reports []rpc.UpdateTaskStatusRequest
}

func (r *statusRecorder) attempts(taskID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var attempts []string
	for _, report := range r.reports {
		if report.TaskID == taskID {
			attempts = append(attempts, report.AttemptID)
		}
	}
	return attempts
}

func workerWithStatusRecorder(t *testing.T) (*Worker, *statusRecorder) {
	t.Helper()
	a, b := net.Pipe()
	client, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	serverSession, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	recorder := &statusRecorder{}
	server := rpc.NewServer(rpc.DefaultConfig())
	server.Register(rpc.MethodUpdateTaskStatus, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var req rpc.UpdateTaskStatusRequest
		if err := rpc.DecodeRPCPayload(rpc.RPCFrame{Payload: payload}, &req); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
		}
		recorder.mu.Lock()
		recorder.reports = append(recorder.reports, req)
		recorder.mu.Unlock()
		return &rpc.UpdateTaskStatusResponse{Accepted: true}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, serverSession) }()
	t.Cleanup(func() { cancel(); _ = client.Close(); _ = serverSession.Close(); server.Stop(); <-done })
	w := New(Config{}, zerolog.Nop())
	w.client = rpc.NewClient(client, rpc.DefaultConfig())
	return w, recorder
}

func deployCommand(t *testing.T, attemptID string) rpc.WorkerCommand {
	t.Helper()
	// An empty operator chain fails immediately in the executor, which reports
	// Failed under the admitted handle's attempt ID.
	data, err := protocol.EncodeMsgPack(rpc.TaskDescriptor{TaskID: "task", AttemptID: attemptID})
	if err != nil {
		t.Fatal(err)
	}
	return rpc.WorkerCommand{Type: rpc.CommandTypeDeployTask, TaskID: "task", JobID: "job", Data: data}
}

func TestDeploymentOfNewerAttemptWaitsForPreviousTeardown(t *testing.T) {
	w, recorder := workerWithStatusRecorder(t)
	// The previous attempt has reported its terminal status, but runTask has not
	// yet removed its handle.
	previous := &taskHandle{done: make(chan struct{}), cancel: func() {}, jobID: "job", attemptID: "old"}
	w.tasks["task"] = previous

	w.handleDeployTask(deployCommand(t, "old"))
	w.handleDeployTask(deployCommand(t, "new"))
	w.mu.RLock()
	current := w.tasks["task"]
	w.mu.RUnlock()
	if current != previous {
		t.Fatal("deployment replaced a handle that was still tearing down")
	}

	w.mu.Lock()
	delete(w.tasks, "task")
	w.mu.Unlock()
	close(previous.done)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if attempts := recorder.attempts("task"); len(attempts) > 0 {
			if len(attempts) != 1 || attempts[0] != "new" {
				t.Fatalf("status reports for attempts %v, want only the newer attempt", attempts)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("newer attempt was dropped instead of admitted after the previous teardown")
}
