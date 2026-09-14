package coordinator

import (
	"sync"
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestCommandStreamReplacementConcurrentWithEnqueue(t *testing.T) {
	c, _ := newTestCoordinator(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			c.EnqueueCommand("worker", rpc.WorkerCommand{Type: rpc.CommandTypeTakeSnapshot})
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			_, cleanup := c.RegisterCommandStream("worker")
			cleanup()
		}
	}()
	wg.Wait()
}
