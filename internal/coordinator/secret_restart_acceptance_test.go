package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
	"github.com/tarungka/wire/sdk/connectors/memory"
)

// A source that keeps the initial attempt running until its worker is stopped.
// The second deployment uses the same persisted graph but a finite source.
type secretRestartSource struct{ engine.SourceOperator }

func (s secretRestartSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSecretRecoveryAcrossCoordinatorAndWorkerRestart(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "new environment snapshot"
		if missing {
			name = "missing credential fails before deployment"
		}
		t.Run(name, func(t *testing.T) {
			const variable = "WIRE_RESTART_ACCEPTANCE_SECRET"
			const firstSecret = "first-private-restart-credential"
			const secondSecret = "second-private-restart-credential"
			t.Setenv(variable, firstSecret)
			root := t.TempDir()
			serverTLS, clientTLS := rpcTestTLS(t)
			received := make(chan string, 4)
			sinkID := t.Name()
			defer memory.Reset(sinkID)
			wait := func(description string, condition func() bool) {
				t.Helper()
				deadline := time.Now().Add(12 * time.Second)
				for !condition() {
					if time.Now().After(deadline) {
						t.Fatal(description)
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			start := func(initial bool) (*Coordinator, *PebbleStore, func()) {
				t.Helper()
				store, err := NewPebbleStore(filepath.Join(root, "metadata"))
				if err != nil {
					t.Fatal(err)
				}
				c := New(CoordinatorConfig{NodeID: "coordinator", WorkerTimeout: time.Second, HeartbeatInterval: 100 * time.Millisecond}, store, nil, zerolog.Nop())
				ctx, cancel := context.WithCancel(context.Background())
				coordinatorDone := make(chan error, 1)
				go func() { coordinatorDone <- c.Run(ctx) }()
				srv := NewTransportServer(c, "127.0.0.1:0", zerolog.Nop(), serverTLS)
				if err := srv.Listen(); err != nil {
					cancel()
					<-coordinatorDone
					_ = store.Close()
					t.Fatal(err)
				}
				serverDone := make(chan error, 1)
				go func() { serverDone <- srv.Serve(ctx) }()
				registry := worker.NewRegistry()
				sourceConfig := encode(t, memory.SourceConfig{Events: [][]byte{[]byte("recovered-record")}})
				registry.RegisterSource("secret-source", func(ctx context.Context, raw []byte, tc worker.TaskContext) (engine.SourceOperator, error) {
					var cfg struct {
						Token string `json:"token"`
					}
					if err := json.Unmarshal(raw, &cfg); err != nil {
						return nil, fmt.Errorf("invalid credential config")
					}
					select {
					case received <- cfg.Token:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					source, err := memory.SourceFactory()(ctx, sourceConfig, tc)
					if err != nil {
						return nil, err
					}
					if initial {
						return secretRestartSource{source}, nil
					}
					return source, nil
				})
				registry.RegisterSink("memory-sink", memory.SinkFactory())
				w := worker.NewWithRegistry(worker.Config{WorkerID: "worker", TaskSlots: 1, CoordinatorAddr: srv.Addr(), RPCTLSConfig: clientTLS, EpochPath: filepath.Join(root, "worker-epoch"), HeartbeatInterval: 100 * time.Millisecond, HeartbeatTimeout: time.Second}, registry, zerolog.Nop())
				workerDone := make(chan error, 1)
				go func() { workerDone <- w.Run(ctx) }()
				var once sync.Once
				stop := func() {
					once.Do(func() {
						cancel()
						_ = srv.Shutdown(context.Background())
						for _, done := range []<-chan error{coordinatorDone, serverDone, workerDone} {
							select {
							case <-done:
							case <-time.After(5 * time.Second):
								t.Error("restart cleanup did not join runtime")
							}
						}
						if err := store.Close(); err != nil {
							t.Error(err)
						}
					})
				}
				t.Cleanup(stop)
				wait("coordinator not ready", c.IsReady)
				wait("worker did not authenticate and register", func() bool {
					c.mu.RLock()
					defer c.mu.RUnlock()
					w := c.workers["worker"]
					return w != nil && w.RPCAuthenticated && w.SupportsSecretConfig && c.cmdStreams["worker"] != nil
				})
				return c, store, stop
			}
			checkStore := func(store *PebbleStore) {
				t.Helper()
				if err := store.PrefixScan(nil, func(key, raw []byte) bool {
					if bytes.Contains(raw, []byte(firstSecret)) || bytes.Contains(raw, []byte(secondSecret)) {
						t.Errorf("credential persisted at %q", key)
					}
					return true
				}); err != nil {
					t.Fatal(err)
				}
			}
			c, store, stop := start(true)
			graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{
				{OperatorID: "source", Type: rpc.OperatorTypeSource, Parallelism: 1, ClassName: "secret-source", Config: []byte(`{"token":"${WIRE_RESTART_ACCEPTANCE_SECRET}"}`)},
				{OperatorID: "sink", Type: rpc.OperatorTypeSink, Parallelism: 1, ClassName: "memory-sink", Config: encode(t, memory.SinkConfig{SinkID: sinkID})},
			}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyForward}}}
			job, err := c.SubmitJob("restart-secret", 1, encode(t, graph))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case value := <-received:
				if value != firstSecret {
					t.Fatal("initial credential not resolved")
				}
			case <-time.After(8 * time.Second):
				t.Fatal("initial factory was not called")
			}
			wait("initial job not running", func() bool { c.mu.RLock(); defer c.mu.RUnlock(); return c.jobs[job.ID].Status == JobRunning })
			checkStore(store)
			c.mu.RLock()
			oldEpoch := c.epoch
			c.mu.RUnlock()
			stop()
			if missing {
				if err := os.Unsetenv(variable); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv(variable, secondSecret)
			}
			recovered, reopened, _ := start(false)
			recovered.mu.RLock()
			newEpoch := recovered.epoch
			recovered.mu.RUnlock()
			if newEpoch <= oldEpoch {
				t.Fatal("coordinator did not recover a new epoch")
			}
			want := JobFinished
			if missing {
				want = JobFailed
			}
			wait("recovered job did not reach expected terminal status", func() bool {
				recovered.mu.RLock()
				defer recovered.mu.RUnlock()
				return recovered.jobs[job.ID] != nil && recovered.jobs[job.ID].Status == want
			})
			if missing {
				select {
				case <-received:
					t.Fatal("missing credential reached worker factory")
				default:
				}
			} else {
				select {
				case value := <-received:
					if value != secondSecret {
						t.Fatal("recovery reused old credential")
					}
				default:
					t.Fatal("no recovered deployment")
				}
				if records := memory.Collected(sinkID); len(records) != 1 || string(records[0].Value) != "recovered-record" {
					t.Fatalf("recovered output: %v", records)
				}
			}
			checkStore(reopened)
		})
	}
}
