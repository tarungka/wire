package pipeline

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/models" // Assuming Job, ProcessedJob are here
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

// ParallelPipeline orchestrates data processing using a worker pool.
type ParallelPipeline struct {
	// Configuration
	name        string
	parallelism int // Number of worker goroutines
	bufferSize  int // Channel buffer size

	// Components - Assuming these interfaces are defined elsewhere
	source DataSource
	sink   DataSink
	// transforms []Transform // Assuming Transform interface is defined

	// Runtime state
	workerPool *WorkerPool
	metrics    *PipelineMetrics // Assuming PipelineMetrics is defined
	limiter    *rate.Limiter

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	logger zerolog.Logger
}

// Config for ParallelPipeline, mirroring issue's PipelineConfig
// This might be duplicative if internal/pipeline/config.go also defines a similar struct.
// For now, using the structure from the issue for this specific file.
type ParallelPipelineConfig struct {
	Name        string
	Parallelism int `yaml:"parallelism" json:"parallelism" validate:"min=1,max=1000"`
	BufferSize  int `yaml:"buffer_size" json:"buffer_size" validate:"min=1,max=10000"`

	// Advanced tuning options from the issue
	BatchSize         int           `yaml:"batch_size" json:"batch_size"`
	FlushInterval     time.Duration `yaml:"flush_interval" json:"flush_interval"`
	MaxRetries        int           `yaml:"max_retries" json:"max_retries"`
	RateLimitPerSec   int           `yaml:"rate_limit_per_sec" json:"rate_limit_per_sec"`
	BackpressureLimit int           `yaml:"backpressure_limit" json:"backpressure_limit"`

	// Assuming DataSource, DataSink, and Transforms are configured elsewhere
	// and passed to NewParallelPipeline or set via methods.
	Source     DataSource  // Example: how source might be passed
	Sink       DataSink    // Example: how sink might be passed
	Transforms []Transform // Example: how transforms might be passed
}

// NewParallelPipeline creates a new ParallelPipeline.
func NewParallelPipeline(config ParallelPipelineConfig, source DataSource, sink DataSink, transforms []Transform, pMetrics *PipelineMetrics) *ParallelPipeline {
	if config.Parallelism <= 0 {
		config.Parallelism = runtime.NumCPU()
	}
	if config.BufferSize <= 0 {
		// Sensible default, e.g., based on parallelism
		config.BufferSize = config.Parallelism * 100
	}

	ctx, cancel := context.WithCancel(context.Background()) // Base context for the pipeline

	p := &ParallelPipeline{
		name:        config.Name,
		parallelism: config.Parallelism,
		bufferSize:  config.BufferSize,
		source:      source,
		sink:        sink,
		// transforms:  transforms, // TODO: Integrate transforms
		metrics:     pMetrics, // Use passed-in metrics
		ctx:         ctx,
		cancel:      cancel,
		logger:      log.With().Str("pipeline_name", config.Name).Logger(),
	}

	if p.metrics == nil {
		// Fallback if no metrics collector is provided
		p.metrics = NewPipelineMetrics(config.Name) // Assuming NewPipelineMetrics exists
		p.logger.Warn().Msg("PipelineMetrics not provided, creating a new default one.")
	}

	if config.RateLimitPerSec > 0 {
		p.limiter = rate.NewLimiter(rate.Limit(config.RateLimitPerSec), config.RateLimitPerSec)
	}

	// Initialize worker pool
	p.workerPool = NewWorkerPool(p.parallelism, p.bufferSize)

	return p
}

// Run starts the pipeline processing.
func (p *ParallelPipeline) Run(parentCtx context.Context) error {
	p.logger.Info().Msg("Starting parallel pipeline...")

	// Link pipeline context with parent context
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel() // Ensure cancellation propagates

	// Connect to source and sink
	if err := p.source.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to source: %w", err)
	}
	defer p.source.Disconnect()

	if err := p.sink.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to sink: %w", err)
	}
	defer p.sink.Disconnect()

	// Create an errgroup for managing goroutines
	g, gCtx := errgroup.WithContext(ctx)

	// Start the worker pool
	jobProcessor := p.createJobProcessor()
	p.workerPool.Start(jobProcessor) // jobProcessor needs to be defined

	// Goroutine for reading from source and sending to worker pool
	g.Go(func() error {
		defer close(p.workerPool.GetJobQueue()) // Close job queue when source is done
		var sourceWg sync.WaitGroup
		// Initial data - assuming LoadInitialData returns a channel of *models.Job
		initialDataChan, err := p.source.LoadInitialData(gCtx, &sourceWg)
		if err != nil {
			p.logger.Error().Err(err).Msg("Failed to load initial data from source")
			return fmt.Errorf("source initial data error: %w", err)
		}
		// Stream data
		dataChan, err := p.source.Read(gCtx, &sourceWg)
		if err != nil {
			p.logger.Error().Err(err).Msg("Failed to read data from source")
			return fmt.Errorf("source read error: %w", err)
		}

		// Process initial data
		for job := range initialDataChan {
			select {
			case p.workerPool.GetJobQueue() <- job:
				p.metrics.IncrementJobsSubmitted()
			case <-gCtx.Done():
				p.logger.Info().Msg("Context cancelled, stopping source reading (initial data)")
				return gCtx.Err()
			}
		}

		// Process stream data
		for job := range dataChan {
			select {
			case p.workerPool.GetJobQueue() <- job:
				p.metrics.IncrementJobsSubmitted()
			case <-gCtx.Done():
				p.logger.Info().Msg("Context cancelled, stopping source reading (stream data)")
				return gCtx.Err()
			}
		}
		p.logger.Info().Msg("Source reading complete.")
		return nil
	})

	// Goroutine for collecting results from worker pool and sending to sink
	g.Go(func() error {
		// This assumes sink.Write takes a channel of *models.ProcessedJob
		// This part needs to align with how sink.Write is implemented.
		// For now, let's assume we collect all results and then write, or write one by one.
		// The issue's `writeSink` implies a more complex sink writing logic (e.g., batching).
		// We'll simplify for now and pass the result channel directly if the sink supports it,
		// or iterate and write.

		// The existing DataSink interface's Write method is:
		// Write(ctx context.Context, wg *sync.WaitGroup, data ...<-chan *models.Job) error
		// This is not directly compatible with a single channel of *models.ProcessedJob.
		// This part needs significant refactoring of DataSink or a new sink interface.

		// Temporary: Adapt ProcessedJob to Job for the existing sink interface
		// This is a HACK and needs proper design.
		processedJobAdaptorChan := make(chan *models.Job, p.bufferSize)
		var sinkWg sync.WaitGroup // wg for the sink's Write method
		sinkWg.Add(1) // The sink's Write method will call Done

		go func() {
			defer close(processedJobAdaptorChan)
			defer sinkWg.Done() // Call Done when this goroutine finishes
			for processedJob := range p.workerPool.GetResultQueue() {
				if processedJob == nil {
					continue
				}
				// HACK: Convert ProcessedJob back to Job or a compatible format.
				// This assumes ProcessedJob contains enough info to reconstruct a Job
				// or that the sink can handle ProcessedJob if we change the interface.
				// For now, let's assume ProcessedJob has OriginalData.
				job := &models.Job{
					ID:   processedJob.ID,
					Data: processedJob.ProcessedData, // Or OriginalData, depending on sink needs
					// Other fields might be needed
				}
				select {
				case processedJobAdaptorChan <- job:
					p.metrics.IncrementResultsCollected()
				case <-gCtx.Done():
					p.logger.Info().Msg("Context cancelled, stopping result collection for sink.")
					return
				}
			}
			p.logger.Info().Msg("Result collection for sink complete.")
		}()

		// Call the existing sink.Write method.
		// The sink's Write method expects one or more input channels.
		// We are giving it one adapted channel.
		// The wg passed to sink.Write is for its internal goroutines if any.
		err := p.sink.Write(gCtx, &sinkWg, processedJobAdaptorChan)
		if err != nil {
			p.logger.Error().Err(err).Msg("Error writing to sink")
			return fmt.Errorf("sink write error: %w", err)
		}
		p.logger.Info().Msg("Sink writing process initiated.")
		sinkWg.Wait() // Wait for the sink writing to actually complete
		p.logger.Info().Msg("Sink writing complete.")
		return nil
	})

	// Goroutine for handling errors from the worker pool
	g.Go(func() error {
		for err := range p.workerPool.GetErrorQueue() {
			p.logger.Error().Err(err).Msg("Error from worker pool")
			p.metrics.IncrementFailedJobs() // Or a more specific error metric
			// Decide if an error from a worker should stop the entire pipeline.
			// For now, just log it. If pipeline needs to stop, cancel gCtx.
			// Example: if err is critical { cancel() }
		}
		p.logger.Info().Msg("Error handling goroutine finished.")
		return nil
	})

	// Metrics reporting goroutine (optional, from issue)
	g.Go(func() error {
		metricsTicker := time.NewTicker(30 * time.Second) // Example: report every 30s
		defer metricsTicker.Stop()
		for {
			select {
			case <-metricsTicker.C:
				p.reportMetrics()
			case <-gCtx.Done():
				p.logger.Info().Msg("Metrics reporting stopped.")
				return nil
			}
		}
	})

	// Wait for all goroutines in the errgroup to complete
	err := g.Wait()

	// Stop the worker pool after all pipeline goroutines are done or an error occurs
	p.logger.Info().Msg("Waiting for worker pool to stop...")
	p.workerPool.Stop()
	p.logger.Info().Msg("Parallel pipeline shutdown complete.")

	if err != nil && err != context.Canceled {
		p.logger.Error().Err(err).Msg("Parallel pipeline completed with error")
		return err
	}

	p.logger.Info().Msg("Parallel pipeline completed successfully.")
	return nil
}

// Stop gracefully shuts down the pipeline.
func (p *ParallelPipeline) Stop() {
	p.logger.Info().Msg("Stopping parallel pipeline...")
	p.cancel() // Signal all pipeline operations to stop
	// Worker pool is stopped at the end of Run()
}

// createJobProcessor creates a JobProcessor for the worker pool.
// This is where transforms would be applied.
func (p *ParallelPipeline) createJobProcessor() JobProcessor {
	// This should ideally use p.transforms
	return &transformProcessor{
		transforms: p.transforms, // Assuming p has transforms
		metrics:    p.metrics,
		limiter:    p.limiter,
		logger:     p.logger.With().Str("component", "transform_processor").Logger(),
	}
}

// reportMetrics logs current pipeline metrics.
func (p *ParallelPipeline) reportMetrics() {
	if p.metrics != nil {
		stats := p.metrics.GetStats() // Assuming GetStats returns a structured object or map
		wpStats := p.workerPool.Stats()
		p.logger.Info().
			Interface("pipeline_stats", stats).
			Interface("worker_pool_stats", wpStats).
			Msg("Current pipeline metrics")
	}
}

// transformProcessor implements the JobProcessor interface.
// It applies a series of transformations to a job.
type transformProcessor struct {
	transforms []Transform      // Assuming Transform interface is defined
	metrics    *PipelineMetrics // For recording transform-specific metrics
	limiter    *rate.Limiter
	logger     zerolog.Logger
}

// Process applies transformations to the job.
func (tp *transformProcessor) Process(job *models.Job) (*models.ProcessedJob, error) {
	if job == nil {
		return nil, fmt.Errorf("cannot process nil job")
	}
	startTime := time.Now()

	// Apply rate limiting if configured
	if tp.limiter != nil {
		// Using context.Background() for limiter, or pass pipeline context if appropriate
		if err := tp.limiter.Wait(context.Background()); err != nil {
			return nil, fmt.Errorf("rate limiter error: %w", err)
		}
	}

	currentData := job.Data // Assuming job.Data is the payload

	// Apply transforms in sequence
	for i, transform := range tp.transforms {
		// Ensure transform is not nil
		if transform == nil {
			tp.logger.Warn().Int("transform_index", i).Msg("Encountered nil transform, skipping.")
			continue
		}

		transformedData, err := transform.Apply(currentData) // Apply needs to be defined in Transform interface
		if err != nil {
			if tp.metrics != nil {
				tp.metrics.IncrementTransformErrors(i) // Assuming such a method exists
			}
			tp.logger.Error().Err(err).Int("transform_index", i).Str("job_id", job.ID).Msg("Transform failed")
			return nil, fmt.Errorf("transform %d (%s) failed for job %s: %w", i, transform.Name(), job.ID, err) // Assuming Transform has Name()
		}
		currentData = transformedData
	}

	if tp.metrics != nil {
		tp.metrics.RecordProcessingTime(time.Since(startTime))
		// tp.metrics.IncrementProcessedJobs() // This is usually done by the worker pool itself
	}

	processedJob := &models.ProcessedJob{
		ID:            job.ID,
		OriginalData:  job.Data,       // Or a deep copy if needed
		ProcessedData: currentData,
		ProcessedAt:   time.Now(),
		// Add other relevant fields, e.g., status, errors if handled partially
	}

	tp.logger.Debug().Str("job_id", job.ID).Dur("processing_time_ms", time.Since(startTime)).Msg("Job processed by transformProcessor")
	return processedJob, nil
}

// Transform interface (basic example, needs to be defined properly)
type Transform interface {
	Name() string // Name of the transform
	Apply(data interface{}) (interface{}, error)
}

// Ensure interfaces (DataSource, DataSink, Transform, PipelineMetrics) are defined
// or imported correctly for this file to compile.
// DataSource and DataSink are from existing code.
// Transform and PipelineMetrics are new based on the issue.

// Example: If models.Job and models.ProcessedJob are not defined, they would look like:
/*
package models

import "time"

type Job struct {
    ID   string
    Data interface{}
    // other fields
}

type ProcessedJob struct {
    ID            string
    OriginalData  interface{}
    ProcessedData interface{}
    ProcessedAt   time.Time
    Error         error // If error occurred during processing
    // other fields
}
*/
