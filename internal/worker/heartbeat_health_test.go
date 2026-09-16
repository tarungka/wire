package worker

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shirou/gopsutil/v4/cpu"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestWorkerContactDeadlineIncludesFailedDials(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	w := New(Config{WorkerID: "worker", CoordinatorAddr: address, TaskSlots: 1, HeartbeatInterval: 10 * time.Millisecond, HeartbeatTimeout: 100 * time.Millisecond}, zerolog.Nop())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	err = w.Run(ctx)
	if !errors.Is(err, ErrCoordinatorContactLost) {
		t.Fatalf("worker did not request supervisor restart: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond || elapsed > time.Second {
		t.Fatalf("contact deadline elapsed=%s", elapsed)
	}
	w.mu.RLock()
	stopping := w.stopping
	w.mu.RUnlock()
	if !stopping {
		t.Fatal("expired worker still admits tasks")
	}
}

func TestHeartbeatCarriesAttemptStatusAndTaskMetrics(t *testing.T) {
	var running atomic.Bool
	source := &lifecycleSource{remaining: 3, running: &running}
	registry, desc := lifecyclePipeline(source, &lifecycleMap{}, &lifecycleSink{})
	w := NewWithRegistry(Config{WorkerID: "worker", TaskSlots: 2}, registry, zerolog.Nop())
	w.epoch = 5
	desc.TaskID = "task"
	desc.AttemptID = "attempt"
	desc.EpochID = 5
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.installTaskLocked("job", "task", desc, cancel)
	h := w.tasks["task"]
	err := w.executor.run(engine.WithTaskStatistics(ctx, h.statistics), "job", "task", desc, zerolog.Nop(), func() { running.Store(true) })
	if err != nil {
		t.Fatal(err)
	}
	h.status = rpc.TaskStatusFinished
	report := w.buildHeartbeatRequest()
	if report.Load.ActiveSlots != 1 || report.Load.TotalSlots != 2 || len(report.Tasks) != 1 {
		t.Fatalf("inventory: %+v", report)
	}
	task := report.Tasks[0]
	if task.AttemptID != "attempt" || task.EpochID != 5 || task.Status != rpc.TaskStatusFinished || task.Metrics.RecordsIn != 3 || task.Metrics.RecordsOut != 3 || task.Metrics.BytesIn == 0 || task.Metrics.BytesOut == 0 {
		t.Fatalf("task report: %+v metrics:%+v", task, task.Metrics)
	}
}

func TestResourceReportSamplesHostAndMarksUnavailable(t *testing.T) {
	sampler := resourceSampler{}
	first := sampler.sample(context.Background(), t.TempDir())
	if first.SampledAt == 0 || first.GoroutineCount < 1 || first.MemoryTotalBytes <= 0 || first.DiskTotalBytes <= 0 {
		t.Fatalf("incomplete host report: %+v", first)
	}
	second := sampler.sample(context.Background(), "/definitely-not-a-wire-volume")
	missing := false
	for _, field := range second.Unavailable {
		if field == "disk" {
			missing = true
		}
	}
	if !missing {
		t.Fatal("missing volume reported as measured")
	}
	if usage := cpuUsage(cpu.TimesStat{User: 10, Idle: 10}, cpu.TimesStat{User: 15, Idle: 25}); usage != 25 {
		t.Fatalf("CPU delta=%v", usage)
	}
}
