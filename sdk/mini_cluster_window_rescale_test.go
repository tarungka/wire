package sdk

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type observedWindowCount struct {
	CountAggregator
	seen *atomic.Int32
}

func (a observedWindowCount) Add(acc []byte, e Event) []byte {
	a.seen.Add(1)
	return a.CountAggregator.Add(acc, e)
}

func TestMiniClusterWindowRescale(t *testing.T) {
	for _, kind := range []string{"hashmap", "pebble"} {
		for _, window := range []WindowAssigner{TumblingWindow(10 * time.Millisecond), SlidingWindow(10*time.Millisecond, 5*time.Millisecond), SessionWindow(10 * time.Millisecond)} {
			for _, sizes := range [][2]int{{4, 8}, {8, 4}, {4, 3}} {
				t.Run(fmt.Sprintf("%s/%s/%d-to-%d", kind, window.Type(), sizes[0], sizes[1]), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
					defer cancel()
					cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 8, NumWorkers: 3})
					defer cluster.Shutdown()
					env := cluster.GetExecutionEnvironment().SetParallelism(sizes[0])
					if kind == "pebble" {
						env.SetStateBackend(NewPebbleStateBackend(t.TempDir()))
					}
					first, second, advance := make(chan struct{}), make(chan struct{}), make(chan struct{})
					close(first)
					var attempts, seen atomic.Int32
					sink := &collectSink{}
					env.AddSourceFactory("source", func(InstanceContext) (Source, error) {
						gate := first
						var mark <-chan struct{}
						if attempts.Add(1) > 1 {
							gate = second
							mark = advance
						}
						return &miniRescaleSource{gate: gate, advance: mark}, nil
					}).SetParallelism(1).SetWatermarkStrategy(BoundedOutOfOrderness(0)).
						KeyByWithName("key", func(e Event) ([]byte, error) { return e.Key, nil }).Window(window).Aggregate(observedWindowCount{seen: &seen}).Name("state").
						AddSinkFactory("sink", func(InstanceContext) (Sink, error) { return sink, nil })
					done := make(chan error, 1)
					go func() { _, err := env.Execute(ctx); done <- err }()
					defer func() {
						cancel()
						<-done
						if len(cluster.Jobs()) != 0 {
							t.Error("control endpoint retained")
						}
					}()
					windowsPerKey := 1
					if window.Type() == "sliding" {
						windowsPerKey = 2
					}
					expected := 32 * windowsPerKey
					lifecycleWait(t, ctx, func() bool { return len(cluster.Jobs()) == 1 && seen.Load() == int32(expected) })
					if len(sink.Events()) != 0 {
						t.Fatal("windows fired before savepoint")
					}
					job := cluster.Jobs()[0]
					path := job.CoordinatorURL + "/api/v1/jobs/" + job.JobID
					request := miniClusterRequester(t, ctx)
					lifecycleWait(t, ctx, func() bool { return request("GET", path, "", 200)["status"] == "RUNNING" })
					savepoint := request("POST", path+"/savepoints", "", 202)["id"].(string)
					lifecycleWait(t, ctx, func() bool { return request("GET", path+"/savepoints/"+savepoint, "", 200)["status"] == "COMPLETED" })
					request("POST", path+"/rescale", fmt.Sprintf(`{"savepoint_id":%q,"operators":{"state":%d,"sink":%d}}`, savepoint, sizes[1], sizes[1]), 202)
					lifecycleWait(t, ctx, func() bool { return attempts.Load() > 1 && request("GET", path, "", 200)["status"] == "RUNNING" })
					close(second)
					lifecycleWait(t, ctx, func() bool { return seen.Load() == int32(expected*2) })
					close(advance)
					lifecycleWait(t, ctx, func() bool { return len(sink.Events()) >= expected })
					counts := make(map[string]int)
					for _, event := range sink.Events() {
						if len(event.Value) != 8 || binary.BigEndian.Uint64(event.Value) != 2 {
							t.Fatalf("lost window accumulator %s: %x", event.Key, event.Value)
						}
						counts[string(event.Key)]++
					}
					if len(counts) != 32 {
						t.Fatalf("restored keys %d", len(counts))
					}
					for key, count := range counts {
						if count != windowsPerKey {
							t.Fatalf("duplicate/missing windows for %s: %d", key, count)
						}
					}
					replacement := request("POST", path+"/savepoints", "", 202)["id"].(string)
					lifecycleWait(t, ctx, func() bool { return request("GET", path+"/savepoints/"+replacement, "", 200)["status"] == "COMPLETED" })
					request("DELETE", path+"/savepoints/"+savepoint, "", 204)
				})
			}
		}
	}
}
