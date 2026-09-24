package sdk

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type miniReplaySource struct {
	position atomic.Int32
	release  <-chan struct{}
	restored *atomic.Int32
}

func (*miniReplaySource) Open(context.Context) error { return nil }
func (*miniReplaySource) Close() error               { return nil }
func (*miniReplaySource) GenerateWatermark() int64   { return 0 }
func (s *miniReplaySource) Checkpoint(uint64) ([]byte, error) {
	return binary.BigEndian.AppendUint32(nil, uint32(s.position.Load())), nil
}
func (s *miniReplaySource) RestoreOffset(_ context.Context, data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("bad offset")
	}
	offset := binary.BigEndian.Uint32(data)
	if offset > 64 || offset%32 != 0 {
		return fmt.Errorf("bad offset")
	}
	s.position.Store(int32(offset))
	s.restored.Add(1)
	return nil
}
func (s *miniReplaySource) ReadBatch(ctx context.Context) ([]Event, error) {
	position := s.position.Load()
	ready := position == 0
	if position == 32 {
		select {
		case <-s.release:
			ready = true
		default:
		}
	}
	if ready {
		events := make([]Event, 32)
		for i := range events {
			events[i] = Event{Key: []byte(fmt.Sprintf("key-%d", i)), Value: []byte("record"), EventTime: 1}
		}
		s.position.Store(position + 32)
		return events, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return []Event{}, nil
	}
}

func TestMiniClusterBackendRecoveryAfterWorkerLoss(t *testing.T) {
	for _, kind := range []string{"hashmap", "pebble"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()
			cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 8, NumWorkers: 3})
			defer cluster.Shutdown()
			env := cluster.GetExecutionEnvironment().SetParallelism(3).SetRestartStrategy(FixedDelay(3, 0))
			if kind == "pebble" {
				env.SetStateBackend(NewPebbleStateBackend(t.TempDir()))
			}
			release := make(chan struct{})
			var restored atomic.Int32
			sink := &collectSink{}
			env.AddSourceFactory("source", func(InstanceContext) (Source, error) {
				return &miniReplaySource{release: release, restored: &restored}, nil
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
				err := <-done
				if t.Failed() {
					t.Logf("execution: %v", err)
				}
			}()
			lifecycleWait(t, ctx, func() bool { return len(cluster.Jobs()) == 1 && len(sink.Events()) == 32 })
			job := cluster.Jobs()[0]
			path := job.CoordinatorURL + "/api/v1/jobs/" + job.JobID
			request := miniClusterRequester(t, ctx)
			lifecycleWait(t, ctx, func() bool { return request("GET", path, "", 200)["status"] == "RUNNING" })
			detail := request("GET", path, "", 200)
			victim := "mini-worker-2"
			ownsState := false
			for _, raw := range detail["tasks"].([]any) {
				task := raw.(map[string]any)
				if task["worker_id"] == victim && strings.Contains(task["task_id"].(string), "/state/") {
					ownsState = true
				}
			}
			if !ownsState {
				t.Fatal("fault target does not own managed state")
			}
			// The lexically last worker is not selected as another worker's sole
			// replica by the current placement policy. Its archives survive elsewhere.
			saved := request("POST", path+"/savepoints", "", 202)["id"].(string)
			lifecycleWait(t, ctx, func() bool { return request("GET", path+"/savepoints/"+saved, "", 200)["status"] == "COMPLETED" })
			if err := cluster.StopWorker(ctx, job.JobID, victim); err != nil {
				t.Fatal(err)
			}
			lifecycleWait(t, ctx, func() bool {
				current := request("GET", path, "", 200)
				return restored.Load() > 0 && current["status"] == "RUNNING" && current["restart_count"].(float64) > 0
			})
			for _, raw := range request("GET", path, "", 200)["tasks"].([]any) {
				if raw.(map[string]any)["worker_id"] == victim {
					t.Fatal("recovery reused stopped worker")
				}
			}
			close(release)
			lifecycleWait(t, ctx, func() bool { return len(sink.Events()) >= 64 })
			counts := make(map[string][]string)
			for _, e := range sink.Events() {
				counts[string(e.Key)] = append(counts[string(e.Key)], string(e.Value))
			}
			if len(counts) != 32 {
				t.Fatalf("recovered key count %d", len(counts))
			}
			for key, values := range counts {
				if len(values) != 2 || values[0] != "1" || values[1] != "2" {
					t.Fatalf("lost or replayed state for %s: %v", key, values)
				}
			}
			replacement := request("POST", path+"/savepoints", "", 202)["id"].(string)
			lifecycleWait(t, ctx, func() bool { return request("GET", path+"/savepoints/"+replacement, "", 200)["status"] == "COMPLETED" })
			request("DELETE", path+"/savepoints/"+saved, "", 204)
		})
	}
}
