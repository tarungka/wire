package pipeline

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/models" // Assuming Job is defined here
)

// JobProcessor defines the interface for processing a job.
// This needs to be defined based on how jobs are processed.
// For now, let's assume it's a function or an interface method.
type JobProcessor interface {
	Process(job *models.Job) (*models.ProcessedJob, error) // Adjusted to models.Job and models.ProcessedJob
}

// WorkerPool manages a pool of workers to process jobs in parallel.
type WorkerPool struct {
	workers     int
	jobQueue    chan *models.Job // Changed to models.Job
	resultQueue chan *models.ProcessedJob // Changed to models.ProcessedJob
	errorQueue  chan error

	// Metrics
	activeWorkers int32
	processedJobs uint64
	failedJobs    uint64

	// Control
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewWorkerPool creates a new WorkerPool.
func NewWorkerPool(workers int, bufferSize int) *WorkerPool {
	if workers <= 0 {
		workers = 1 // Ensure at least one worker
	}
	if bufferSize <= 0 {
		bufferSize = workers * 10 // Default buffer size
	}
	return &WorkerPool{
		workers:     workers,
		jobQueue:    make(chan *models.Job, bufferSize),
		resultQueue: make(chan *models.ProcessedJob, bufferSize),
		errorQueue:  make(chan error, workers), // Error queue size can be same as workers
		stopCh:      make(chan struct{}),
	}
}

// Start initializes and starts the worker goroutines.
func (wp *WorkerPool) Start(processor JobProcessor) {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.worker(i, processor)
	}
}

// worker is the main function for each worker goroutine.
func (wp *WorkerPool) worker(id int, processor JobProcessor) {
	defer wp.wg.Done()
	atomic.AddInt32(&wp.activeWorkers, 1)
	defer atomic.AddInt32(&wp.activeWorkers, -1)

	logger := log.With().Int("worker_id", id).Logger()
	logger.Debug().Msg("Worker started")

	for {
		select {
		case <-wp.stopCh:
			logger.Debug().Msg("Worker stopping")
			return

		case job, ok := <-wp.jobQueue:
			if !ok {
				logger.Debug().Msg("Job queue closed, worker stopping")
				return
			}

			startTime := time.Now()
			// Process job
			result, err := processor.Process(job) // This needs a concrete Job type
			processingTime := time.Since(startTime)

			if err != nil {
				atomic.AddUint64(&wp.failedJobs, 1)
				jobID := "unknown" // Default job ID
				if job != nil {    // Check if job is not nil before accessing ID
					jobID = job.ID // Assuming Job has an ID field
				}
				logger.Error().
					Err(err).
					Str("job_id", jobID).
					Dur("duration_ms", processingTime).
					Msg("Job processing failed")

				// Non-blocking send to errorQueue
				select {
				case wp.errorQueue <- err:
				case <-wp.stopCh: // If stopCh is closed, don't block on errorQueue
					logger.Warn().Msg("Worker stopping, could not send error to errorQueue")
					return
				default: // If errorQueue is full, log and drop error to prevent blocking
					logger.Warn().Err(err).Msg("Error queue full, dropping job processing error")
				}
				continue // Continue to next job
			}

			atomic.AddUint64(&wp.processedJobs, 1)
			logger.Debug().
				Str("job_id", job.ID). // Assuming Job has an ID field
				Dur("duration_ms", processingTime).
				Msg("Job processed successfully")

			// Send result to resultQueue
			select {
			case wp.resultQueue <- result:
			case <-wp.stopCh:
				logger.Debug().Msg("Worker stopping, result not sent")
				return
			}
		}
	}
}

// Stop signals all workers to stop and waits for them to finish.
func (wp *WorkerPool) Stop() {
	logger := log.Logger // Use a package-level logger or pass one in
	logger.Info().Msg("Stopping worker pool...")
	close(wp.stopCh) // Signal workers to stop

	// Wait for all worker goroutines to complete
	wp.wg.Wait()

	// Close channels only after all goroutines that write to them have finished
	// Ensure jobQueue is drained or handled if Stop is called while jobs are queued.
	// Closing resultQueue and errorQueue here assumes they are no longer being written to.
	close(wp.resultQueue)
	close(wp.errorQueue)
	logger.Info().Msg("Worker pool stopped.")
}

// WorkerPoolStats holds statistics for the worker pool.
type WorkerPoolStats struct {
	ActiveWorkers int32
	ProcessedJobs uint64
	FailedJobs    uint64
	QueuedJobs    int // Length of the job queue
}

// Stats returns the current statistics of the worker pool.
func (wp *WorkerPool) Stats() WorkerPoolStats {
	return WorkerPoolStats{
		ActiveWorkers: atomic.LoadInt32(&wp.activeWorkers),
		ProcessedJobs: atomic.LoadUint64(&wp.processedJobs),
		FailedJobs:    atomic.LoadUint64(&wp.failedJobs),
		QueuedJobs:    len(wp.jobQueue), // Note: len(chan) is an approximation
	}
}

// GetJobQueue returns the job queue channel for sending jobs to the pool.
func (wp *WorkerPool) GetJobQueue() chan<- *models.Job {
	return wp.jobQueue
}

// GetResultQueue returns the result queue channel for receiving processed jobs.
func (wp *WorkerPool) GetResultQueue() <-chan *models.ProcessedJob {
	return wp.resultQueue
}

// GetErrorQueue returns the error queue channel for receiving errors.
func (wp *WorkerPool) GetErrorQueue() <-chan error {
	return wp.errorQueue
}
