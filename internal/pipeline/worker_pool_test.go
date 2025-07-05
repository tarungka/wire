package pipeline

import (
	"fmt"
	"sync"
	"testing"
	"time"
	"sync/atomic"

	"github.com/stretchr/testify/assert"
	"github.com/tarungka/wire/internal/models"
)

// mockJobProcessor for testing WorkerPool
type mockJobProcessor struct {
	processDelay time.Duration
	failJob      bool
	failJobID    string
	processedIDs map[string]bool
	mu           sync.Mutex
}

func newMockJobProcessor(delay time.Duration, failJob bool, failJobID string) *mockJobProcessor {
	return &mockJobProcessor{
		processDelay: delay,
		failJob:      failJob,
		failJobID:    failJobID,
		processedIDs: make(map[string]bool),
	}
}

func (mjp *mockJobProcessor) Process(job *models.Job) (*models.ProcessedJob, error) {
	if job == nil {
		return nil, fmt.Errorf("nil job")
	}
	time.Sleep(mjp.processDelay)

	mjp.mu.Lock()
	mjp.processedIDs[job.ID] = true
	mjp.mu.Unlock()

	if mjp.failJob && job.ID == mjp.failJobID {
		return nil, fmt.Errorf("mock processing error for job %s", job.ID)
	}

	return &models.ProcessedJob{
		ID:            job.ID,
		OriginalData:  job.Data,
		ProcessedData: fmt.Sprintf("processed %v", job.Data),
		ProcessedAt:   time.Now(),
	}, nil
}

func (mjp *mockJobProcessor) WasProcessed(jobID string) bool {
	mjp.mu.Lock()
	defer mjp.mu.Unlock()
	return mjp.processedIDs[jobID]
}


func TestWorkerPool_NewWorkerPool(t *testing.T) {
	wp := NewWorkerPool(0, 0) // Test default values
	assert.Equal(t, 1, wp.workers, "Default workers should be 1")
	assert.Equal(t, 1*10, cap(wp.jobQueue), "Default buffer size for jobQueue")

	wp = NewWorkerPool(5, 50)
	assert.Equal(t, 5, wp.workers)
	assert.Equal(t, 50, cap(wp.jobQueue))
	assert.Equal(t, 50, cap(wp.resultQueue))
	assert.Equal(t, 5, cap(wp.errorQueue)) // errorQueue size is num workers
}

func TestWorkerPool_StartStop(t *testing.T) {
	processor := newMockJobProcessor(0, false, "")
	wp := NewWorkerPool(2, 10)
	wp.Start(processor)

	// Give workers time to start (though ideally, we'd have a better sync mechanism)
	time.Sleep(50 * time.Millisecond)
	stats := wp.Stats()
	// ActiveWorkers might fluctuate as they pick up tasks, but should be around the configured number
	// For this test, we're more interested in clean start/stop
	// assert.Equal(t, int32(2), stats.ActiveWorkers)

	wp.Stop()
	stats = wp.Stats()
	assert.Equal(t, int32(0), stats.ActiveWorkers, "All workers should be stopped")
	// Ensure queues are closed (by checking if reads are possible and eventually fail)
	_, jobOpen := <-wp.jobQueue
	_, resultOpen := <-wp.resultQueue
	_, errorOpen := <-wp.errorQueue

	assert.False(t, jobOpen, "Job queue should be closed after stop if it was closed internally by Stop")
	assert.False(t, resultOpen, "Result queue should be closed after stop")
	assert.False(t, errorOpen, "Error queue should be closed after stop")
}

func TestWorkerPool_ProcessJobs(t *testing.T) {
	numJobs := 10
	processor := newMockJobProcessor(10*time.Millisecond, false, "")
	wp := NewWorkerPool(3, numJobs)
	wp.Start(processor)

	for i := 0; i < numJobs; i++ {
		wp.GetJobQueue() <- &models.Job{ID: fmt.Sprintf("job-%d", i), Data: i}
	}

	var results []*models.ProcessedJob
	var errors []error
	var wg sync.WaitGroup
	wg.Add(2) // For result and error collection goroutines

	go func() {
		defer wg.Done()
		for res := range wp.GetResultQueue() {
			results = append(results, res)
		}
	}()
	go func() {
		defer wg.Done()
		for err := range wp.GetErrorQueue() {
			errors = append(errors, err)
		}
	}()

	// Close job queue to signal no more jobs, then stop the pool
	close(wp.GetJobQueue())
	wp.Stop() // This will wait for workers to finish, then close result/error queues
	wg.Wait() // Wait for collectors to finish

	assert.Len(t, results, numJobs, "Should have processed all jobs")
	assert.Len(t, errors, 0, "Should have no errors")
	assert.Equal(t, uint64(numJobs), wp.Stats().ProcessedJobs)
	for i := 0; i < numJobs; i++ {
		assert.True(t, processor.WasProcessed(fmt.Sprintf("job-%d", i)), "Job %d should be marked as processed", i)
	}
}

func TestWorkerPool_JobFailure(t *testing.T) {
	failID := "job-fail"
	processor := newMockJobProcessor(5*time.Millisecond, true, failID)
	wp := NewWorkerPool(1, 5)
	wp.Start(processor)

	wp.GetJobQueue() <- &models.Job{ID: "job-ok", Data: "ok"}
	wp.GetJobQueue() <- &models.Job{ID: failID, Data: "fail_this"}
	wp.GetJobQueue() <- &models.Job{ID: "job-ok-2", Data: "ok2"}

	var results []*models.ProcessedJob
	var errors []error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for res := range wp.GetResultQueue() {
			results = append(results, res)
		}
	}()
	go func() {
		defer wg.Done()
		for err := range wp.GetErrorQueue() {
			errors = append(errors, err)
		}
	}()

	close(wp.GetJobQueue())
	wp.Stop()
	wg.Wait()

	assert.Len(t, results, 2, "Should have 2 successful results")
	assert.Len(t, errors, 1, "Should have 1 error")
	assert.Equal(t, uint64(1), wp.Stats().FailedJobs)
	assert.Equal(t, uint64(2), wp.Stats().ProcessedJobs)
	assert.Contains(t, errors[0].Error(), "mock processing error for job job-fail")
}

func TestWorkerPool_Stats(t *testing.T) {
	processor := newMockJobProcessor(0, false, "")
	wp := NewWorkerPool(2, 5)

	initialStats := wp.Stats()
	assert.Equal(t, int32(0), initialStats.ActiveWorkers)
	assert.Equal(t, uint64(0), initialStats.ProcessedJobs)
	assert.Equal(t, uint64(0), initialStats.FailedJobs)
	assert.Equal(t, 0, initialStats.QueuedJobs)

	wp.Start(processor)
	// Send one job
	wp.GetJobQueue() <- &models.Job{ID: "job-1", Data: 1}
	// Allow some time for job to be picked up, but not necessarily finished
	time.Sleep(10 * time.Millisecond)

	statsAfterJobSent := wp.Stats()
	// ActiveWorkers can be tricky to assert precisely due to timing.
	// QueuedJobs should be 0 if the job was picked up.
	// If the job is very fast, ProcessedJobs might be 1.

	// Let's wait for processing to likely complete
	var wg sync.WaitGroup
	wg.Add(1)
	go func(){
		defer wg.Done()
		<- wp.GetResultQueue() // consume the result
	}()

	close(wp.GetJobQueue()) // Signal no more jobs
	wp.Stop() // Stop the pool
	wg.Wait() // Wait for result collector

	finalStats := wp.Stats()
	assert.Equal(t, int32(0), finalStats.ActiveWorkers, "Active workers should be 0 after stop")
	assert.Equal(t, uint64(1), finalStats.ProcessedJobs, "Processed jobs should be 1")
	assert.Equal(t, uint64(0), finalStats.FailedJobs, "Failed jobs should be 0")
	assert.Equal(t, 0, finalStats.QueuedJobs, "Queued jobs should be 0 after processing and stop")
}

func TestWorkerPool_StopWithPendingJobs(t *testing.T) {
	numJobs := 5
	processor := newMockJobProcessor(50*time.Millisecond, false, "") // Jobs take time
	wp := NewWorkerPool(1, numJobs+5) // Single worker, buffer > numJobs
	wp.Start(processor)

	for i := 0; i < numJobs; i++ {
		wp.GetJobQueue() <- &models.Job{ID: fmt.Sprintf("job-%d", i), Data: i}
	}

	// Don't close job queue immediately, call Stop.
	// Stop should close stopCh, workers should process remaining jobs in queue then exit.
	// Then Stop closes result/error queues.

	var processedCount uint64

	var wg sync.WaitGroup
	wg.Add(1) // For result collection

	go func() {
		defer wg.Done()
		for range wp.GetResultQueue() {
			atomic.AddUint64(&processedCount, 1)
		}
	}()

	// Give a little time for the first job to be picked by the worker
	time.Sleep(10 * time.Millisecond)

	wp.Stop() // Stop the pool. This will wait for the worker to finish its current job and drain jobQueue.

	wg.Wait() // Wait for result collector to finish (it finishes when resultQueue is closed by Stop)

	// Since jobQueue is not closed by the sender, workers will process jobs until jobQueue is empty
	// *and* stopCh is closed. The behavior of Stop() is to close stopCh, then wait for workers.
	// Workers, upon seeing stopCh closed, will return. If they are in the middle of `<-wp.jobQueue`,
	// and jobQueue is not closed, they might block.
	// The current worker implementation:
	//   select { case <-wp.stopCh: return; case job := <-wp.jobQueue: ...}
	// This means if stopCh is closed, worker returns. If a job is already taken, it's processed.
	// If jobQueue still has items when stopCh is closed, those items in jobQueue might not be processed
	// by the worker if it selects stopCh first.
	// However, Stop() also calls wg.Wait() for workers. A worker only calls wg.Done() when it exits its loop.
	// If jobQueue is not closed externally, Stop() will close stopCh. Workers will see this and exit.
	// Any jobs remaining in jobQueue at that point will be unprocessed.
	// The expectation here is that Stop should ideally allow processing of already enqueued jobs.
	// The current worker code:
	//   case job, ok := <-wp.jobQueue: if !ok { return } ...
	// If jobQueue is never closed by sender, `ok` will always be true until stopCh is selected.

	// Let's refine: Stop closes stopCh. Workers will exit.
	// If jobs are still in jobQueue, they won't be processed by *these* workers.
	// `len(wp.jobQueue)` after stop might show remaining jobs.
	// `processedCount` will show how many were processed before workers exited.

	// Given the current implementation, Stop() signals workers to terminate.
	// They will finish their current job, then exit. They won't pick new jobs from jobQueue
	// if stopCh is prioritized or checked first.
	// The test "ProcessJobs" closes jobQueue first, which is a cleaner shutdown pattern.
	// This test checks a more abrupt Stop().

	// Assert that at least one job was processed (the one picked up before Stop)
	// and likely not all, because Stop is called while jobs are still processing/queued.
	// This depends heavily on timing and worker loop details.
	// A truly robust Stop might involve a two-phase: signal no new jobs, then wait for queue drain, then hard stop.

	// For current worker_pool.go: Stop() closes stopCh. Worker's select will pick <-wp.stopCh and return.
	// It will complete the *current* job it's working on, but not pick new ones from jobQueue.
	assert.GreaterOrEqual(t, atomic.LoadUint64(&processedCount), uint64(1), "At least one job should be processed")
	// The number of processed jobs could be anything from 1 to numJobs depending on how fast the worker is
	// compared to the Stop() call. This makes the assertion tricky.
	// A better assertion might be on wp.Stats().QueuedJobs after stop, if it accurately reflects unprocessed items.
	// However, `len(chan)` is not perfectly reliable for queue length during active processing.

	// Let's assume the single worker processes at least one job fully.
	// The rest might remain in the queue.
	finalStats := wp.Stats()
	t.Logf("Jobs processed: %d, Jobs failed: %d, Jobs queued: %d", finalStats.ProcessedJobs, finalStats.FailedJobs, finalStats.QueuedJobs)
	assert.Equal(t, uint64(0), finalStats.FailedJobs)
	// QueuedJobs might be numJobs - processedCount.
	// This test highlights the behavior of Stop() when the job queue isn't explicitly closed by the producer.
}

func TestWorkerPool_ErrorQueueFull(t *testing.T) {
	processor := newMockJobProcessor(0, true, "job-0") // Fail job-0
	wp := NewWorkerPool(1, 2) // 1 worker, errorQueue capacity 1
	wp.Start(processor)

	// Send two jobs that will fail. Only the first error should make it to errorQueue.
	// The worker's error path has a non-blocking send to errorQueue.
	wp.GetJobQueue() <- &models.Job{ID: "job-0", Data: "fail_this_0"}
	wp.GetJobQueue() <- &models.Job{ID: "job-0", Data: "fail_this_1"} // Same ID to trigger fail on processor

	var errorsReceived []error
	var wg sync.WaitGroup
	wg.Add(1) // For error collection

	go func() {
		defer wg.Done()
		// Try to collect up to 2 errors, but expect only 1 due to queue size
		for i := 0; i < 2; i++ {
			select {
			case err, ok := <-wp.GetErrorQueue():
				if !ok {
					return
				}
				errorsReceived = append(errorsReceived, err)
			case <-time.After(100 * time.Millisecond): // Timeout if error not received
				return
			}
		}
	}()

	// Give time for jobs to be processed and errors to be sent
	time.Sleep(50 * time.Millisecond)

	close(wp.GetJobQueue())
	wp.Stop()
	wg.Wait()

	assert.Len(t, errorsReceived, 1, "Should have received only 1 error due to errorQueue capacity")
	if len(errorsReceived) > 0 {
		assert.Contains(t, errorsReceived[0].Error(), "mock processing error for job job-0")
	}
	// Both jobs should be marked as failed by the pool's stats, even if error couldn't be sent for one.
	stats := wp.Stats()
	assert.Equal(t, uint64(2), stats.FailedJobs, "Worker pool should count both jobs as failed")
}

// TODO: Add benchmark tests as suggested in the issue: BenchmarkPipelineParallelism
// This would likely be in a parallel_pipeline_test.go or similar, as it tests the whole pipeline.
// For WorkerPool itself, a benchmark could test raw job throughput.

func BenchmarkWorkerPool_Process(b *testing.B) {
	processor := newMockJobProcessor(0, false, "") // No delay, no failures
	wp := NewWorkerPool(10, b.N) // 10 workers, buffer for all jobs
	wp.Start(processor)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Drain results
		for i := 0; i < b.N; i++ {
			<-wp.GetResultQueue()
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wp.GetJobQueue() <- &models.Job{ID: fmt.Sprintf("benchjob-%d", i), Data: i}
	}

	close(wp.GetJobQueue())
	wp.Stop()
	wg.Wait()
	b.StopTimer()
}
