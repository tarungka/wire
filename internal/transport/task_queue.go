package transport

import (
	"context"
	"fmt"
	"sync"
)

// taskQueue belongs to one registration generation. Removing a task closes its
// generation so late streams cannot enter a restarted task with the same ID.
type taskQueue struct {
	rejectOverflow bool
	mu             sync.Mutex
	streams        chan *FrameStream
	space          chan struct{}
	done           chan struct{}
	closed         bool
}

func newTaskQueue() *taskQueue {
	return &taskQueue{streams: make(chan *FrameStream, 64), space: make(chan struct{}, 1), done: make(chan struct{})}
}

func (q *taskQueue) enqueue(ctx context.Context, stream *FrameStream) error {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return fmt.Errorf("transport: task unregistered")
		}
		select {
		case q.streams <- stream:
			q.mu.Unlock()
			return nil
		default:
			q.mu.Unlock()
			if q.rejectOverflow {
				return fmt.Errorf("transport: task input queue full")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-q.done:
			return fmt.Errorf("transport: task unregistered")
		case <-q.space:
		}
	}
}

func (q *taskQueue) accept(ctx, muxCtx context.Context) (*FrameStream, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-muxCtx.Done():
		return nil, fmt.Errorf("transport: mux closed")
	case <-q.done:
		return nil, fmt.Errorf("transport: task unregistered")
	case stream := <-q.streams:
		q.mu.Lock()
		closed := q.closed
		q.mu.Unlock()
		select {
		case q.space <- struct{}{}:
		default:
		}
		if closed {
			_ = stream.Close()
			return nil, fmt.Errorf("transport: task unregistered")
		}
		return stream, nil
	}
}

func (q *taskQueue) close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	close(q.done)
	var pending []*FrameStream
	for {
		select {
		case stream := <-q.streams:
			pending = append(pending, stream)
		default:
			q.mu.Unlock()
			for _, stream := range pending {
				_ = stream.Close()
			}
			return
		}
	}
}

// UnregisterTask stops accepting streams for a finished or canceled task and
// closes streams still waiting for it. Streams already accepted are owned by
// the task and must be closed by its runtime.
func (m *Mux) UnregisterTask(taskID string) {
	m.mu.Lock()
	queue := m.tasks[taskID]
	delete(m.tasks, taskID)
	m.mu.Unlock()
	if queue != nil {
		queue.close()
	}
}

// IsTaskRegistered reports whether a task currently accepts incoming streams.
func (m *Mux) IsTaskRegistered(taskID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tasks[taskID] != nil
}
