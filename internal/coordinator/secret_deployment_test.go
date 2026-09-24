package coordinator

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestSecretDeploymentRequiresAuthenticatedCapablePeer(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		authenticated, capable, reservations, allowed bool
	}{
		{"secure", true, true, true, true}, {"plaintext", false, true, true, false},
		{"old worker", true, false, true, false}, {"command queue fallback", true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WIRE_DEPLOYMENT_SECRET", "submission-private-token")
			c, store := newTestCoordinator(t)
			a, b := net.Pipe()
			caller, err := yamux.Client(a, nil)
			if err != nil {
				t.Fatal(err)
			}
			receiver, err := yamux.Server(b, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer caller.Close()
			defer receiver.Close()
			submitted := make(chan rpc.SubmitJobRequest, 1)
			server := rpc.NewServer(rpc.DefaultConfig())
			server.Register(rpc.MethodRequestTaskSlots, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
				var req rpc.RequestTaskSlotsRequest
				if err := protocol.DecodeMsgPack(payload, &req); err != nil {
					return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "invalid reservation")
				}
				return &rpc.RequestTaskSlotsResponse{Granted: req.RequiredSlots, ReservationID: req.ReservationID}, nil
			})
			server.Register(rpc.MethodSubmitJob, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
				var req rpc.SubmitJobRequest
				if err := protocol.DecodeMsgPack(payload, &req); err != nil {
					return nil, rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "invalid deployment")
				}
				submitted <- req
				return &rpc.SubmitJobResponse{Accepted: true}, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { defer close(done); server.ServeSession(ctx, receiver) }()
			defer func() { cancel(); server.Stop(); <-done }()
			c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 8, TaskSlotsAvailable: 8, LastHeartbeat: time.Now(), RPCClient: rpc.NewClient(caller, rpc.DefaultConfig()), RPCPeerEpoch: c.epoch, RPCAuthenticated: tc.authenticated, SupportsSecretConfig: tc.capable, SupportsReservations: tc.reservations}
			graph := linearGraph()
			graph.Operators[0].Config = []byte(`{"authorization":"Bearer ${WIRE_DEPLOYMENT_SECRET}"}`)
			encoded, err := protocol.EncodeMsgPack(graph)
			if err != nil {
				t.Fatal(err)
			}
			job, err := c.SubmitJob("secure-job", 1, encoded)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("WIRE_DEPLOYMENT_SECRET", "changed-after-submission")
			owned := c.jobs[job.ID]
			c.scheduleJob(owned)
			if !tc.allowed {
				if owned.Status != JobCreated {
					t.Fatalf("insecure deployment changed status: %v", owned.Status)
				}
				select {
				case <-submitted:
					t.Fatal("sent secrets to an ineligible peer")
				default:
				}
				raw, err := store.Get(JobAssignmentsKey(job.ID))
				if err != nil {
					t.Fatal(err)
				}
				if len(raw) != 0 || len(c.pendingCmds["worker"]) != 0 {
					t.Fatal("published an insecure deployment")
				}
				return
			}
			var req rpc.SubmitJobRequest
			select {
			case req = <-submitted:
			default:
				t.Fatal("secure deployment not dispatched")
			}
			found := false
			for _, task := range req.Tasks {
				for _, op := range task.OperatorChain {
					if op.OperatorID == graph.Operators[0].OperatorID {
						found = true
						if string(op.Config) != `{"authorization":"Bearer submission-private-token"}` {
							t.Fatalf("incorrect runtime configuration: %s", op.Config)
						}
						if len(task.SecretValues) != 1 || task.SecretValues[0] != "submission-private-token" {
							t.Fatal("missing runtime redaction values")
						}
					}
				}
			}
			if !found {
				t.Fatal("missing configured operator")
			}
			for _, key := range [][]byte{JobMetaKey(job.ID), JobConfigKey(job.ID), JobAssignmentsKey(job.ID)} {
				raw, err := store.Get(key)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(raw, []byte("submission-private-token")) || bytes.Contains(raw, []byte("changed-after-submission")) {
					t.Fatal("resolved credential persisted")
				}
				if !bytes.Contains(raw, []byte("${WIRE_DEPLOYMENT_SECRET}")) {
					t.Fatal("persisted metadata lost unresolved reference")
				}
			}
			if len(c.pendingCmds["worker"]) != 0 {
				t.Fatal("secret payload entered fallback queue")
			}
		})
	}
}

func TestSecretPlacementReservesSecureCapacityWithoutOverbooking(t *testing.T) {
	c, _ := newTestCoordinator(t)
	peer := rpc.NewClient(nil, rpc.DefaultConfig())
	c.workers["secure"] = &WorkerMeta{ID: "secure", TaskSlotsAvailable: 1, LastHeartbeat: time.Now(), RPCClient: peer, RPCPeerEpoch: c.epoch, RPCAuthenticated: true, SupportsSecretConfig: true, SupportsReservations: true}
	c.workers["plain"] = &WorkerMeta{ID: "plain", TaskSlotsAvailable: 3, LastHeartbeat: time.Now()}
	tasks := []rpc.TaskDescriptor{{TaskID: "ordinary1"}, {TaskID: "secret"}, {TaskID: "ordinary2"}, {TaskID: "ordinary3"}}
	assignments, err := c.assignTasks(tasks, map[string]bool{"secret": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments["secure"]) != 1 || assignments["secure"][0].TaskID != "secret" || len(assignments["plain"]) != 3 {
		t.Fatalf("incorrect constrained placement: %+v", assignments)
	}
	if _, err := c.assignTasks(tasks, map[string]bool{"secret": true, "ordinary1": true}); err == nil {
		t.Fatal("overbooked secure capacity")
	}
}
