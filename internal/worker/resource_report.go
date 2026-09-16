package worker

import (
	"context"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/tarungka/wire/internal/rpc"
)

// resourceSampler runs off the heartbeat path: slow OS/filesystem calls must
// not delay liveness. Reports describe the host visible to this process, not
// container quotas. Missing measurements are explicitly marked unavailable.
type resourceSampler struct{ previous *cpu.TimesStat }

func (s *resourceSampler) sample(ctx context.Context, path string) *rpc.ResourceReport {
	r := &rpc.ResourceReport{SampledAt: time.Now().UnixMilli(), GoroutineCount: runtime.NumGoroutine()}
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil || len(times) != 1 {
		r.Unavailable = append(r.Unavailable, "cpu")
	} else {
		if s.previous == nil {
			r.Unavailable = append(r.Unavailable, "cpu")
		} else {
			r.CPUUsagePercent = cpuUsage(*s.previous, times[0])
		}
		s.previous = &times[0]
	}
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		r.Unavailable = append(r.Unavailable, "memory")
	} else {
		r.MemoryUsedBytes = int64(memory.Used)
		r.MemoryTotalBytes = int64(memory.Total)
	}
	volume, err := disk.UsageWithContext(ctx, path)
	if err != nil {
		r.Unavailable = append(r.Unavailable, "disk")
	} else {
		r.DiskUsedBytes = int64(volume.Used)
		r.DiskTotalBytes = int64(volume.Total)
	}
	return r
}
func cpuUsage(previous, current cpu.TimesStat) float64 {
	total := func(s cpu.TimesStat) float64 {
		return s.User + s.System + s.Nice + s.Idle + s.Iowait + s.Irq + s.Softirq + s.Steal
	}
	elapsed := total(current) - total(previous)
	idle := current.Idle + current.Iowait - previous.Idle - previous.Iowait
	if elapsed <= 0 {
		return 0
	}
	return max(0, min(100, 100*(elapsed-idle)/elapsed))
}
func (w *Worker) runResourceSampler(ctx context.Context) {
	path := "."
	if w.cfg.CheckpointReplica != nil && w.cfg.CheckpointReplica.StoreRoot != "" {
		path = w.cfg.CheckpointReplica.StoreRoot
	}
	sampler := resourceSampler{}
	interval := w.cfg.HeartbeatInterval
	if interval <= 0 {
		interval = rpc.DefaultHeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		sampleCtx, cancel := context.WithTimeout(ctx, interval)
		report := sampler.sample(sampleCtx, path)
		cancel()
		w.mu.Lock()
		w.resources = report
		w.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
