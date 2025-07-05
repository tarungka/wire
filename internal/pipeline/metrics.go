package pipeline

import (
	"sync"
	"sync/atomic"
	"time"

	// Assuming a histogram implementation might be used.
	// For simplicity, we might start with basic counters and averages.
	// "github.com/some/histogram" // Placeholder for a histogram library
)

// PipelineMetrics holds various metrics for a pipeline.
type PipelineMetrics struct {
	name string
	mu   sync.RWMutex // To protect access to metrics, especially maps or slices if used

	// Counters (using atomic for safe concurrent updates)
	jobsSubmitted    uint64 // Jobs successfully read from source and sent to worker pool
	resultsCollected uint64 // ProcessedJobs successfully collected from worker pool before sending to sink
	processedJobs    uint64 // Jobs successfully processed by workers
	failedJobs       uint64 // Jobs that failed processing in workers
	transformErrors  map[int]uint64 // Errors per transform stage (index as key)

	// Timings
	// For processing times, a histogram is ideal.
	// For a simpler start, we can track sum and count to calculate average.
	totalProcessingTimeMs int64 // Sum of processing times in milliseconds
	processingTimeCount   uint64 // Count of processing time records

	// TODO: Add more detailed metrics as per the issue if needed, like:
	// batchSizes       *Histogram
	// queueDepths      *Histogram
	// activeWorkers    int32 (This is better tracked in WorkerPoolStats)
	// currentQueueSize int32 (This is better tracked in WorkerPoolStats)
	// throughputRate *Rate (Requires a rate calculation mechanism)
	// errorRate      *Rate (Requires a rate calculation mechanism)
}

// NewPipelineMetrics creates a new PipelineMetrics collector.
func NewPipelineMetrics(name string) *PipelineMetrics {
	return &PipelineMetrics{
		name:            name,
		transformErrors: make(map[int]uint64),
		// Initialize histograms or rate calculators here if used
	}
}

// IncrementJobsSubmitted atomically increments the count of jobs submitted to the pipeline.
func (m *PipelineMetrics) IncrementJobsSubmitted() {
	atomic.AddUint64(&m.jobsSubmitted, 1)
}

// IncrementResultsCollected atomically increments the count of results collected from processors.
func (m *PipelineMetrics) IncrementResultsCollected() {
	atomic.AddUint64(&m.resultsCollected, 1)
}

// IncrementProcessedJobs atomically increments the count of successfully processed jobs.
// This is typically called by the worker or transformProcessor upon successful completion.
func (m *PipelineMetrics) IncrementProcessedJobs() {
	atomic.AddUint64(&m.processedJobs, 1)
}

// IncrementFailedJobs atomically increments the count of failed jobs.
// This is typically called by the worker or error handler when a job processing fails.
func (m *PipelineMetrics) IncrementFailedJobs() {
	atomic.AddUint64(&m.failedJobs, 1)
}

// IncrementTransformErrors increments the error count for a specific transform stage.
func (m *PipelineMetrics) IncrementTransformErrors(transformIndex int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transformErrors[transformIndex]++
}

// RecordProcessingTime records the duration of a single job processing event.
func (m *PipelineMetrics) RecordProcessingTime(d time.Duration) {
	atomic.AddInt64(&m.totalProcessingTimeMs, d.Milliseconds())
	atomic.AddUint64(&m.processingTimeCount, 1)
	// If using a histogram: m.processingTimes.Record(d.Microseconds())
}

// PipelineStats represents a snapshot of the current pipeline metrics.
type PipelineStats struct {
	Name                  string
	JobsSubmitted         uint64
	ResultsCollected      uint64
	ProcessedJobs         uint64
	FailedJobs            uint64
	TransformErrors       map[int]uint64 // Consider a deep copy if this map is large or modified after GetStats
	AvgProcessingTimeMs   float64
	// TODO: Add P95/P99 processing times if using histograms
	// TODO: Add ThroughputPerSec and ErrorRate if rate calculators are implemented
}

// GetStats returns a snapshot of the current pipeline statistics.
func (m *PipelineMetrics) GetStats() PipelineStats {
	m.mu.RLock() // Lock for reading transformErrors map
	defer m.mu.RUnlock()

	totalTimeMs := atomic.LoadInt64(&m.totalProcessingTimeMs)
	timeCount := atomic.LoadUint64(&m.processingTimeCount)

	var avgProcessingTimeMs float64
	if timeCount > 0 {
		avgProcessingTimeMs = float64(totalTimeMs) / float64(timeCount)
	}

	// Deep copy transformErrors to avoid race conditions if the caller modifies the map
	transformErrorsCopy := make(map[int]uint64, len(m.transformErrors))
	for k, v := range m.transformErrors {
		transformErrorsCopy[k] = v
	}

	return PipelineStats{
		Name:                  m.name,
		JobsSubmitted:         atomic.LoadUint64(&m.jobsSubmitted),
		ResultsCollected:      atomic.LoadUint64(&m.resultsCollected),
		ProcessedJobs:         atomic.LoadUint64(&m.processedJobs),
		FailedJobs:            atomic.LoadUint64(&m.failedJobs),
		TransformErrors:       transformErrorsCopy,
		AvgProcessingTimeMs:   avgProcessingTimeMs,
	}
}

// TODO: Implement more advanced metrics features from the issue:
// - Histograms for processing times, batch sizes, queue depths.
// - Rate calculators for throughput and error rates.
// These would require choosing and integrating a library or implementing them.
// For now, this provides a basic set of counters and average processing time.

// Example of how a simple rate might be tracked (conceptual)
/*
type Rate struct {
	mu sync.Mutex
	lastTime time.Time
	lastCount uint64
	currentRate float64
}

func NewRate() *Rate { return &Rate{lastTime: time.Now()} }

func (r *Rate) Mark(count uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(r.lastTime).Seconds()
	if elapsed < 1 && count == r.lastCount { // Avoid too frequent updates or no change
		return
	}
	if elapsed > 0 {
		r.currentRate = float64(count-r.lastCount) / elapsed
	}
	r.lastTime = now
	r.lastCount = count
}

func (r *Rate) Rate() float64 {
	r.mu.Lock() // Should be RLock if Mark is also locked
	defer r.mu.Unlock()
	// Potentially recalculate if Mark hasn't been called recently for an updated rate
	return r.currentRate
}
*/

// Placeholder for Histogram - actual implementation would use a library
/*
type Histogram struct {
	// internal state for histogram
}
func NewHistogram() *Histogram { return &Histogram{} }
func (h *Histogram) Record(value int64) {}
func (h *Histogram) Mean() float64 { return 0 }
func (h *Histogram) Percentile(p float64) float64 { return 0 }
*/
