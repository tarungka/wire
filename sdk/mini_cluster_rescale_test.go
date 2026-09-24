package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type miniRescaleSource struct {
	gate    <-chan struct{}
	emitted bool
}

func (*miniRescaleSource) Open(context.Context) error { return nil }
func (*miniRescaleSource) Close() error               { return nil }
func (*miniRescaleSource) GenerateWatermark() int64   { return 0 }
func (s *miniRescaleSource) ReadBatch(ctx context.Context) ([]Event, error) {
	if !s.emitted {
		select {
		case <-s.gate:
			s.emitted = true
			events := make([]Event, 32)
			for i := range events {
				events[i] = Event{Key: []byte(fmt.Sprintf("key-%d", i)), Value: []byte("record")}
			}
			return events, nil
		default:
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return []Event{}, nil
	}
}

func TestMiniClusterManagedProcessRescale(t *testing.T) {
	for _, kind := range []string{"hashmap", "pebble"} {
		for _, sizes := range [][2]int{{4, 8}, {8, 4}, {4, 3}} {
			t.Run(fmt.Sprintf("%s/%d-to-%d", kind, sizes[0], sizes[1]), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
				defer cancel()
				cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 8, NumWorkers: 3})
				defer cluster.Shutdown()
				env := cluster.GetExecutionEnvironment().SetParallelism(sizes[0])
				if kind == "pebble" {
					env.SetStateBackend(NewPebbleStateBackend(t.TempDir()))
				}
				first, second := make(chan struct{}), make(chan struct{})
				close(first)
				var attempts atomic.Int32
				sink := &collectSink{}
				env.AddSourceFactory("source", func(InstanceContext) (Source, error) {
					gate := first
					if attempts.Add(1) > 1 {
						gate = second
					}
					return &miniRescaleSource{gate: gate}, nil
				}).SetParallelism(1).
					KeyByWithName("key", func(e Event) ([]byte, error) { return e.Key, nil }).ProcessWithName("state", func(c ProcessContext, e Event) ([]Event, error) {
					state := c.GetValueState("count")
					n, err := state.ValueInt64()
					if err != nil {
						return nil, err
					}
					if err := state.SetInt64(n + 1); err != nil {
						return nil, err
					}
					e.Value = []byte(fmt.Sprint(n + 1))
					return []Event{e}, nil
				}).AddSinkFactory("sink", func(InstanceContext) (Sink, error) { return sink, nil })
				done := make(chan error, 1)
				go func() { _, err := env.Execute(ctx); done <- err }()
				defer func() {
					cancel()
					<-done
					if len(cluster.Jobs()) != 0 {
						t.Error("completed execution retained MiniCluster control endpoint")
					}
				}()
				lifecycleWait(t, ctx, func() bool { return len(cluster.Jobs()) == 1 && len(sink.Events()) == 32 })
				job := cluster.Jobs()[0]
				path := job.CoordinatorURL + "/api/v1/jobs/" + job.JobID
				request := func(method, url, body string, status int) map[string]any {
					t.Helper()
					req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Content-Type", "application/json")
					response, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					data, err := io.ReadAll(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != status {
						t.Fatalf("%s %s: %d %s", method, url, response.StatusCode, data)
					}
					result := make(map[string]any)
					if len(data) > 0 {
						if err := json.Unmarshal(data, &result); err != nil {
							t.Fatal(err)
						}
					}
					return result
				}
				lifecycleWait(t, ctx, func() bool { return request("GET", path, "", 200)["status"] == "RUNNING" })
				savepoint := request("POST", path+"/savepoints", "", 202)["id"].(string)
				lifecycleWait(t, ctx, func() bool { return request("GET", path+"/savepoints/"+savepoint, "", 200)["status"] == "COMPLETED" })
				request("POST", path+"/rescale", fmt.Sprintf(`{"savepoint_id":%q,"operators":{"state":%d,"sink":%d}}`, savepoint, sizes[1], sizes[1]), 202)
				lifecycleWait(t, ctx, func() bool { return attempts.Load() > 1 && request("GET", path, "", 200)["status"] == "RUNNING" })
				close(second)
				lifecycleWait(t, ctx, func() bool { return len(sink.Events()) >= 64 })
				counts := make(map[string][]string)
				for _, event := range sink.Events() {
					counts[string(event.Key)] = append(counts[string(event.Key)], string(event.Value))
				}
				if len(counts) != 32 {
					t.Fatalf("key count %d", len(counts))
				}
				for key, values := range counts {
					if len(values) != 2 || values[0] != "1" || values[1] != "2" {
						t.Fatalf("rescale lost/replayed %s: %v", key, values)
					}
				}
				replacement := request("POST", path+"/savepoints", "", 202)["id"].(string)
				lifecycleWait(t, ctx, func() bool { return request("GET", path+"/savepoints/"+replacement, "", 200)["status"] == "COMPLETED" })
				request("DELETE", path+"/savepoints/"+savepoint, "", 204)
			})
		}
	}
}
