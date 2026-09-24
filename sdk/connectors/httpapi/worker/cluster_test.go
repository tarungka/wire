package worker_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/sdk"
	"github.com/tarungka/wire/sdk/connectors/httpapi"
	httpworker "github.com/tarungka/wire/sdk/connectors/httpapi/worker"
)

type singleSource struct{ sent bool }

func (*singleSource) Open(context.Context) error { return nil }
func (*singleSource) Close() error               { return nil }
func (*singleSource) GenerateWatermark() int64   { return 0 }
func (s *singleSource) ReadBatch(context.Context) ([]sdk.Event, error) {
	if s.sent {
		return nil, nil
	}
	s.sent = true
	return []sdk.Event{{Key: []byte("key"), Value: []byte(`{"id":42}`)}}, nil
}

type channelSink struct{ events chan sdk.Event }

func (*channelSink) Open(context.Context) error { return nil }
func (*channelSink) Close() error               { return nil }
func (s *channelSink) Write(ctx context.Context, e sdk.Event) error {
	select {
	case s.events <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestPublicHTTPWorkerDeliveryAndNamedDLQ(t *testing.T) {
	for _, scenario := range []string{"success", "retry", "permanent-dlq"} {
		t.Run(scenario, func(t *testing.T) {
			var attempts atomic.Int32
			var bodiesMu sync.Mutex
			var bodies, ids []string
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				bodiesMu.Lock()
				bodies = append(bodies, string(body))
				ids = append(ids, r.Header.Get("X-Idempotency-Key"))
				bodiesMu.Unlock()
				count := attempts.Add(1)
				if scenario == "permanent-dlq" {
					w.WriteHeader(400)
				} else if scenario == "retry" && count == 1 {
					w.WriteHeader(503)
				} else {
					w.WriteHeader(204)
				}
			}))
			defer target.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			store := coordinator.NewMemoryStore()
			coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "test", WorkerTimeout: 3 * time.Second}, store, nil, zerolog.Nop())
			rpcServer := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
			api := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
			var joined sync.WaitGroup
			start := func(fn func()) { joined.Add(1); go func() { defer joined.Done(); fn() }() }
			defer func() {
				cancel()
				shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				_ = api.Shutdown(shutdown)
				_ = rpcServer.Shutdown(shutdown)
				joined.Wait()
				_ = store.Close()
			}()
			start(func() { _ = coord.Run(ctx) })
			wait := func(ready func() bool) {
				t.Helper()
				for !ready() {
					select {
					case <-ctx.Done():
						t.Fatal("cluster timed out")
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
			wait(coord.IsReady)
			if err := rpcServer.Listen(); err != nil {
				t.Fatal(err)
			}
			if err := api.Listen(); err != nil {
				t.Fatal(err)
			}
			start(func() { _ = rpcServer.Serve(ctx) })
			start(func() { _ = api.Serve() })
			registry := sdk.NewWorkerRegistry()
			httpworker.Register(registry)
			registry.RegisterSource("single", func(context.Context, []byte, sdk.WorkerTaskContext) (sdk.Source, error) { return &singleSource{}, nil })
			dead := make(chan sdk.Event, 2)
			registry.RegisterSink("dead", func(context.Context, []byte, sdk.WorkerTaskContext) (sdk.Sink, error) {
				return &channelSink{events: dead}, nil
			})
			start(func() {
				if err := sdk.RunWorker(ctx, sdk.WorkerConfig{WorkerID: "worker", CoordinatorAddr: rpcServer.Addr(), TaskSlots: 2, HeartbeatInterval: 100 * time.Millisecond, HeartbeatTimeout: 3 * time.Second}, registry); err != nil && ctx.Err() == nil {
					t.Error(err)
				}
			})
			wait(func() bool { return len(coord.ListWorkers()) == 1 })
			cfg, err := httpworker.EncodeSinkConfig(httpapi.SinkConfig{URL: target.URL, AllowInsecure: true, IdempotencyKeyField: "id", MaxAttempts: 2, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			env := sdk.New().SetMode(sdk.Cluster).SetCoordinator("http://" + api.Addr())
			sink := env.AddSourceNamed("source", "single", nil).AddSinkNamed("sink", "http-api", cfg)
			if scenario == "permanent-dlq" {
				sink.WithErrorHandler(sdk.ErrorHandler{OnExhausted: "dlq"}).WithDLQSinkNamed("dead", nil)
			}
			result, err := env.ExecuteWithName(ctx, "http-"+scenario)
			if err != nil || result == nil {
				t.Fatalf("execution: %v", err)
			}
			want := int32(1)
			if scenario == "retry" {
				want = 2
			}
			if attempts.Load() != want {
				t.Fatalf("HTTP attempts=%d want %d", attempts.Load(), want)
			}
			bodiesMu.Lock()
			defer bodiesMu.Unlock()
			if scenario == "retry" && (bodies[0] != bodies[1] || ids[0] == "" || ids[0] != ids[1]) {
				t.Fatal("retry changed request identity")
			}
			if scenario == "permanent-dlq" {
				select {
				case event := <-dead:
					var record struct {
						Original struct{ Key, Value []byte } `json:"original_event"`
						Error    string                      `json:"error"`
					}
					if err := json.Unmarshal(event.Value, &record); err != nil {
						t.Fatal(err)
					}
					if string(record.Original.Key) != "key" || string(record.Original.Value) != `{"id":42}` || record.Error == "" {
						t.Fatalf("DLQ record=%s", event.Value)
					}
				default:
					t.Fatal("permanent delivery failure not routed to DLQ")
				}
			} else if len(dead) != 0 {
				t.Fatal("successful delivery reached DLQ")
			}
		})
	}
}
