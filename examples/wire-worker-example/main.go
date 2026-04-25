// wire-worker-example is a runnable demo of the operator-registry pattern.
//
// It registers the in-tree memory-source and memory-sink connectors plus a
// trivial "upper" map operator, then runs as a Wire worker that connects to
// a coordinator. Pair it with the submit-uppercase-job example (or any
// SDK program in Cluster mode) to see end-to-end execution.
//
// Usage:
//
//	go run ./examples/wire-worker-example \
//	    --coordinator-addr 127.0.0.1:4002 \
//	    --task-slots 4
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/worker"
	"github.com/tarungka/wire/sdk/connectors/memory"
)

func main() {
	var (
		coordinatorAddr = flag.String("coordinator-addr", "127.0.0.1:4002", "coordinator wire-protocol address")
		workerID        = flag.String("worker-id", "", "worker ID (defaults to hostname)")
		listenAddr      = flag.String("listen", "127.0.0.1:0", "data-plane listen address (Phase 2+)")
		taskSlots       = flag.Int("task-slots", 4, "number of concurrent task slots")
	)
	flag.Parse()

	// Use the same logger configuration as the wire CLI so both processes
	// produce identical output. We don't open a file — logger.GetLogger
	// handles a nil log file gracefully (writes only to the console).
	log := logger.GetLogger("worker-example")

	// Register the operators this worker can run. In a real deployment,
	// users add their own factories alongside (or instead of) these.
	worker.RegisterSource("memory-source", memory.SourceFactory())
	worker.RegisterSink("memory-sink", memory.SinkFactory())
	worker.RegisterMap("upper", upperMapFactory())

	w := worker.New(worker.Config{
		WorkerID:        *workerID,
		CoordinatorAddr: *coordinatorAddr,
		ListenAddr:      *listenAddr,
		TaskSlots:       *taskSlots,
	}, log)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Info().Str("coordinator", *coordinatorAddr).Int("slots", *taskSlots).Msg("starting wire-worker-example")
	if err := w.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "worker exited: %v\n", err)
		os.Exit(1)
	}
}

// upperMapFactory returns a MapFactory whose operator uppercases each event's value.
func upperMapFactory() worker.MapFactory {
	return func(_ context.Context, _ []byte, _ worker.TaskContext) (engine.MapOperator, error) {
		return &upperMap{}, nil
	}
}

type upperMap struct{}

func (*upperMap) Open(_ context.Context) error        { return nil }
func (*upperMap) Close() error                        { return nil }
func (*upperMap) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (*upperMap) Map(_ context.Context, e engine.Event) (engine.Event, error) {
	e.Value = []byte(strings.ToUpper(string(e.Value)))
	return e, nil
}

var _ engine.MapOperator = (*upperMap)(nil)
