package engine

import (
	"context"
	"sync/atomic"
	"time"
)

// TaskStatistics counts logical records consumed and emitted by the task chain.
// Bytes include key and value payloads, excluding protocol framing. Snapshots
// are safe during execution; each counter is cumulative for one task attempt.
type TaskStatistics struct {
	recordsIn, recordsOut, bytesIn, bytesOut atomic.Int64
	backpressureNs                           atomic.Int64
}
type TaskStatisticsSnapshot struct {
	RecordsIn, RecordsOut, BytesIn, BytesOut, BackpressureMs int64
}
type taskStatisticsKey struct{}

func WithTaskStatistics(ctx context.Context, stats *TaskStatistics) context.Context {
	return context.WithValue(ctx, taskStatisticsKey{}, stats)
}
func taskStatistics(ctx context.Context) *TaskStatistics {
	s, _ := ctx.Value(taskStatisticsKey{}).(*TaskStatistics)
	return s
}
func (s *TaskStatistics) Snapshot() TaskStatisticsSnapshot {
	if s == nil {
		return TaskStatisticsSnapshot{}
	}
	return TaskStatisticsSnapshot{s.recordsIn.Load(), s.recordsOut.Load(), s.bytesIn.Load(), s.bytesOut.Load(), s.backpressureNs.Load() / int64(time.Millisecond)}
}
func recordTaskInput(ctx context.Context, e Event) {
	if s := taskStatistics(ctx); s != nil {
		s.recordsIn.Add(1)
		s.bytesIn.Add(int64(len(e.Key) + len(e.Value)))
	}
}
func recordTaskOutput(ctx context.Context, e Event) {
	if s := taskStatistics(ctx); s != nil {
		s.recordsOut.Add(1)
		s.bytesOut.Add(int64(len(e.Key) + len(e.Value)))
	}
}
func recordTaskBackpressure(ctx context.Context, d time.Duration) {
	if s := taskStatistics(ctx); s != nil {
		s.backpressureNs.Add(int64(d))
	}
}
