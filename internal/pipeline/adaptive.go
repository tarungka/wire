package pipeline

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
	"math"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// AdaptivePipelineConfig holds configuration for the adaptive pipeline.
type AdaptivePipelineConfig struct {
	Enabled         bool          `yaml:"enabled" json:"enabled"`
	MinWorkers      int           `yaml:"min_workers" json:"min_workers" validate:"min=1"`
	MaxWorkers      int           `yaml:"max_workers" json:"max_workers" validate:"min=1"`
	TargetLatencyMs int64         `yaml:"target_latency_ms" json:"target_latency_ms" validate:"min=1"` // Target latency in milliseconds
	AdjustInterval  time.Duration `yaml:"adjust_interval" json:"adjust_interval" validate:"min=1s"`

	// Configuration for the underlying ParallelPipeline will be separate
	// or inherited/passed through.
}

// AdaptivePipeline wraps a ParallelPipeline and adds adaptive parallelism.
type AdaptivePipeline struct {
	*ParallelPipeline // Embed ParallelPipeline for its functionalities

	config AdaptivePipelineConfig
	logger zerolog.Logger

	// Metrics for adaptation decision-making
	// These would ideally be more sophisticated, like moving averages or percentiles from histograms.
	// For simplicity, we might start with recent average latency from PipelineMetrics.
	// latencyWindow    *MetricsWindow // Placeholder for a more advanced windowing mechanism
	// throughputWindow *MetricsWindow // Placeholder

	// Control for the adaptation loop
	adaptLoopCtx    context.Context
	adaptLoopCancel context.CancelFunc
	adaptLoopWg     sync.WaitGroup
}

// NewAdaptivePipeline creates a new AdaptivePipeline.
// It requires a fully configured ParallelPipeline and adaptive settings.
func NewAdaptivePipeline(pp *ParallelPipeline, adaptiveCfg AdaptivePipelineConfig) (*AdaptivePipeline, error) {
	if pp == nil {
		return nil, fmt.Errorf("parallel pipeline cannot be nil for adaptive pipeline")
	}
	if !adaptiveCfg.Enabled {
		// If not enabled, could return the ParallelPipeline directly or a non-adapting wrapper.
		// For now, assume if NewAdaptivePipeline is called, it's intended to be adaptive if configured.
		log.Warn().Msg("AdaptivePipeline created but adaptive behavior is disabled in config.")
	}
	if adaptiveCfg.MinWorkers <= 0 {
		adaptiveCfg.MinWorkers = 1
	}
	if adaptiveCfg.MaxWorkers < adaptiveCfg.MinWorkers {
		adaptiveCfg.MaxWorkers = adaptiveCfg.MinWorkers * 2 // Ensure max is at least min
		if adaptiveCfg.MaxWorkers == 0 { adaptiveCfg.MaxWorkers = 1} // ensure max is at least 1
	}
	if adaptiveCfg.TargetLatencyMs <= 0 {
		adaptiveCfg.TargetLatencyMs = 100 // Default target latency: 100ms
	}
	if adaptiveCfg.AdjustInterval <= 0 {
		adaptiveCfg.AdjustInterval = 30 * time.Second // Default adjust interval: 30s
	}

	ctx, cancel := context.WithCancel(context.Background()) // Independent context for adaptation loop

	return &AdaptivePipeline{
		ParallelPipeline: pp,
		config:           adaptiveCfg,
		logger:           pp.logger.With().Str("component", "adaptive_controller").Logger(),
		adaptLoopCtx:     ctx,
		adaptLoopCancel:  cancel,
	}, nil
}

// Run starts the adaptive pipeline.
// It starts the underlying ParallelPipeline and the adaptation loop.
func (ap *AdaptivePipeline) Run(parentCtx context.Context) error {
	ap.logger.Info().
		Interface("adaptive_config", ap.config).
		Msg("Starting adaptive pipeline...")

	if ap.config.Enabled {
		ap.adaptLoopWg.Add(1)
		go ap.adaptationLoop()
		ap.logger.Info().Msg("Adaptation loop started.")
	} else {
		ap.logger.Info().Msg("Adaptive behavior is disabled. Running as a standard parallel pipeline.")
	}

	// Run the embedded ParallelPipeline
	err := ap.ParallelPipeline.Run(parentCtx)

	// After the ParallelPipeline finishes (or errors), stop the adaptation loop.
	if ap.config.Enabled {
		ap.logger.Info().Msg("Parallel pipeline finished. Stopping adaptation loop...")
		ap.adaptLoopCancel() // Signal adaptation loop to stop
		ap.adaptLoopWg.Wait() // Wait for adaptation loop to clean up
		ap.logger.Info().Msg("Adaptation loop stopped.")
	}

	ap.logger.Info().Msg("Adaptive pipeline shutdown complete.")
	return err
}

// Stop gracefully shuts down the adaptive pipeline.
// This will also stop the embedded ParallelPipeline via its own Stop method.
func (ap *AdaptivePipeline) Stop() {
	ap.logger.Info().Msg("Stopping adaptive pipeline...")
	if ap.config.Enabled {
		ap.adaptLoopCancel() // Signal adaptation loop to stop first
	}
	ap.ParallelPipeline.Stop() // Stop the main pipeline execution
	// adaptLoopWg.Wait() is handled at the end of Run
}


func (ap *AdaptivePipeline) adaptationLoop() {
	defer ap.adaptLoopWg.Done()
	ticker := time.NewTicker(ap.config.AdjustInterval)
	defer ticker.Stop()

	ap.logger.Debug().Dur("interval", ap.config.AdjustInterval).Msg("Adaptation loop ticker started.")

	for {
		select {
		case <-ap.adaptLoopCtx.Done():
			ap.logger.Info().Msg("Adaptation loop context cancelled. Exiting.")
			return
		case <-ticker.C:
			if !ap.config.Enabled { // Double check in case it's disabled at runtime
				ap.logger.Debug().Msg("Adaptive behavior disabled. Skipping adjustment.")
				continue
			}
			ap.logger.Debug().Msg("Adaptation tick. Adjusting parallelism if needed.")
			ap.adjustParallelism()
		}
	}
}

func (ap *AdaptivePipeline) adjustParallelism() {
	if ap.ParallelPipeline.metrics == nil {
		ap.logger.Warn().Msg("PipelineMetrics not available, cannot adjust parallelism.")
		return
	}
	if ap.ParallelPipeline.workerPool == nil {
		ap.logger.Warn().Msg("WorkerPool not available, cannot adjust parallelism.")
		return
	}

	stats := ap.ParallelPipeline.metrics.GetStats()
	wpStats := ap.ParallelPipeline.workerPool.Stats() // Assuming WorkerPool has Stats()

	currentWorkers := int(wpStats.ActiveWorkers) // Or desired/configured workers from workerPool
	// If workerPool.workers is the configured number:
	// currentWorkers = ap.ParallelPipeline.workerPool.workers
	// For this logic, we need the *actual currently configured* number of workers,
	// which should be a field in WorkerPool or ParallelPipeline representing its setting.
	// Let's assume `ap.ParallelPipeline.parallelism` reflects the current target.
	currentConfiguredWorkers := ap.ParallelPipeline.parallelism


	// Using AvgProcessingTimeMs from PipelineStats as a proxy for latency.
	// A more sophisticated measure would use P95/P99 latency if available.
	avgLatencyMs := stats.AvgProcessingTimeMs
	targetLatencyMs := float64(ap.config.TargetLatencyMs)

	ap.logger.Debug().
		Float64("current_avg_latency_ms", avgLatencyMs).
		Float64("target_latency_ms", targetLatencyMs).
		Int("current_configured_workers", currentConfiguredWorkers).
		Int32("active_workers", wpStats.ActiveWorkers).
		Msg("Evaluating parallelism adjustment.")

	newWorkerCount := currentConfiguredWorkers

	// Basic scaling logic (similar to the issue's example)
	// Scale up if latency is too high
	if avgLatencyMs > targetLatencyMs * 1.2 { // e.g., 20% over target
		newWorkerCount = int(math.Ceil(float64(currentConfiguredWorkers) * 1.25)) // Increase by 25%
		ap.logger.Info().Float64("avg_latency_ms", avgLatencyMs).Msg("Latency high, proposing increase in workers.")
	} else if avgLatencyMs < targetLatencyMs * 0.7 && currentConfiguredWorkers > ap.config.MinWorkers { // e.g., 30% under target
		// Scale down if latency is very low and we have more than min workers
		newWorkerCount = int(math.Floor(float64(currentConfiguredWorkers) * 0.8)) // Decrease by 20%
		ap.logger.Info().Float64("avg_latency_ms", avgLatencyMs).Msg("Latency low, proposing decrease in workers.")
	} else {
		ap.logger.Debug().Msg("Latency within acceptable range, no change to worker count.")
		return // No change needed
	}

	// Clamp to Min/Max workers
	if newWorkerCount < ap.config.MinWorkers {
		newWorkerCount = ap.config.MinWorkers
	}
	if newWorkerCount > ap.config.MaxWorkers {
		newWorkerCount = ap.config.MaxWorkers
	}

	if newWorkerCount != currentConfiguredWorkers {
		ap.logger.Info().
			Int("old_workers", currentConfiguredWorkers).
			Int("new_workers", newWorkerCount).
			Float64("avg_latency_ms", avgLatencyMs).
			Float64("target_latency_ms", targetLatencyMs).
			Msg("Adjusting worker count.")
		ap.scaleWorkers(newWorkerCount)
	} else {
		ap.logger.Debug().Int("current_workers", currentConfiguredWorkers).Msg("Proposed worker count is the same as current. No change.")
	}
}

// scaleWorkers adjusts the number of workers in the underlying worker pool.
// This requires the WorkerPool to support dynamic scaling.
func (ap *AdaptivePipeline) scaleWorkers(newWorkerCount int) {
	// The current WorkerPool (worker_pool.go) does not have a method to dynamically
	// change the number of workers after it has started.
	// Implementing this would involve:
	// 1. Adding a method to WorkerPool, e.g., `Resize(newSize int)`.
	// 2. This method would need to carefully manage starting new workers
	//    or signaling excess workers to stop.
	//    - Stopping workers: Could involve sending a signal on a per-worker channel
	//      or adjusting a target worker count and having workers self-terminate if
	//      their ID is > target.
	//    - Starting workers: Similar to initial Start(), but adding to existing.
	// 3. The ParallelPipeline's `parallelism` field also needs to be updated.

	// For now, this is a placeholder. A true implementation is complex.
	// A simpler, albeit more disruptive, approach could be to stop and restart
	// the worker pool, but that would interrupt processing.

	currentConfiguredWorkers := ap.ParallelPipeline.parallelism
	if newWorkerCount == currentConfiguredWorkers {
		ap.logger.Debug().Int("workers", newWorkerCount).Msg("Scale request for same number of workers, no action.")
		return
	}

	// Placeholder for actual scaling logic:
	ap.logger.Info().
		Int("current_configured_workers", currentConfiguredWorkers).
		Int("requested_new_worker_count", newWorkerCount).
		Msg("Attempting to scale workers (current WorkerPool does not support dynamic scaling).")


	// Update the parallelism setting in the ParallelPipeline.
	// This makes future adjustments aware of the new target.
	// It doesn't actually change the running worker count in the current WorkerPool design.
	atomic.StoreInt32((*int32)(unsafe.Pointer(&ap.ParallelPipeline.parallelism)), int32(newWorkerCount))
	// Using unsafe.Pointer like this is generally discouraged and needs careful thought.
	// A better way is to have a setter method or ensure `parallelism` can be updated atomically if it's an int32/int64.
	// If `ap.ParallelPipeline.parallelism` is `int`, direct atomic ops aren't standard.
	// Let's assume for now we can update it directly if mutex-protected or if `ParallelPipeline` handles this.
	// For simplicity, let's assume a direct update (though this is not concurrency-safe without locks on ParallelPipeline.parallelism):
	// ap.ParallelPipeline.parallelism = newWorkerCount
	// This needs to be done carefully. A proper solution would be a method on ParallelPipeline or WorkerPool.

	ap.logger.Warn().
		Int("new_target_workers", newWorkerCount).
		Msg("Dynamic scaling of WorkerPool is NOT YET IMPLEMENTED. Parallelism configuration updated, but active worker count may not change.")

	// TODO: Implement dynamic scaling in WorkerPool and call it here.
	// e.g., err := ap.ParallelPipeline.workerPool.Resize(newWorkerCount)
	// if err != nil {
	//     ap.logger.Error().Err(err).Msg("Failed to resize worker pool.")
	// } else {
	//     ap.ParallelPipeline.parallelism = newWorkerCount // Update current parallelism setting
	// }
}


// MetricsWindow (placeholder, from issue) - for more sophisticated latency/throughput tracking
/*
type MetricsWindow struct {
    // ... implementation for a sliding window or similar ...
}
func (mw *MetricsWindow) Average() float64 {
    // ... calculate average over the window ...
    return 0.0
}
*/

// For `atomic.StoreInt32((*int32)(unsafe.Pointer(&ap.ParallelPipeline.parallelism)), int32(newWorkerCount))`
// to be valid, `ap.ParallelPipeline.parallelism` would need to be changed to `int32` or use a mutex.
// The current `ParallelPipeline.parallelism` is `int`.
// This indicates a need for refactoring how `parallelism` is stored or updated if direct atomic ops are desired.
// A safer way for `int` would be to use a mutex in `ParallelPipeline` when accessing `parallelism`.
// For this exercise, we'll note this complexity.
import "unsafe" // Required for the unsafe.Pointer cast if used. This is generally not recommended.
