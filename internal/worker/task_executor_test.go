package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type lifecycleProbe struct {
	opened     atomic.Int32
	closed     atomic.Int32
	openErr    error
	openPanic  bool
	closePanic bool
}

func (p *lifecycleProbe) Open(context.Context) error {
	if p.openPanic {
		panic("open panic")
	}
	if p.openErr != nil {
		return p.openErr
	}
	if p.opened.Add(1) != 1 {
		return fmt.Errorf("opened twice")
	}
	return nil
}
func (p *lifecycleProbe) Close() error {
	p.closed.Add(1)
	if p.closePanic {
		panic("close panic")
	}
	return nil
}
func (*lifecycleProbe) Checkpoint(uint64) ([]byte, error) { return nil, nil }

type lifecycleSource struct {
	lifecycleProbe
	remaining int
	running   *atomic.Bool
	readPanic bool
	readErr   error
	block     bool
}

func (s *lifecycleSource) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	if s.opened.Load() != 1 || !s.running.Load() {
		return nil, fmt.Errorf("read before initialization/RUNNING")
	}
	if s.readPanic {
		panic("read panic")
	}
	if s.readErr != nil {
		return nil, s.readErr
	}
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.remaining == 0 {
		return nil, nil
	}
	s.remaining--
	return []engine.Event{{Value: []byte("record")}}, nil
}
func (*lifecycleSource) GenerateWatermark() int64 { return 0 }

type lifecycleMap struct {
	lifecycleProbe
	processPanic bool
}

func (m *lifecycleMap) Map(_ context.Context, e engine.Event) (engine.Event, error) {
	if m.processPanic {
		panic("map panic")
	}
	return e, nil
}

type lifecycleSink struct {
	lifecycleProbe
	count atomic.Int32
}

func (s *lifecycleSink) Write(_ context.Context, _ engine.Event) error { s.count.Add(1); return nil }

func lifecyclePipeline(source *lifecycleSource, m *lifecycleMap, sink *lifecycleSink) (*Registry, rpc.TaskDescriptor) {
	r := NewRegistry()
	r.RegisterSource("source", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) { return source, nil })
	r.RegisterMap("map", func(context.Context, []byte, TaskContext) (engine.MapOperator, error) { return m, nil })
	r.RegisterSink("sink", func(context.Context, []byte, TaskContext) (engine.SinkOperator, error) { return sink, nil })
	return r, rpc.TaskDescriptor{TaskID: "task", Parallelism: 1, OperatorChain: []rpc.OperatorDescriptor{
		{OperatorID: "source", ClassName: "source", Type: rpc.OperatorTypeSource},
		{OperatorID: "map", ClassName: "map", Type: rpc.OperatorTypeMap},
		{OperatorID: "sink", ClassName: "sink", Type: rpc.OperatorTypeSink},
	}}
}

func TestTaskExecutor_InitializesOnceAndDrainsTerminalOutput(t *testing.T) {
	var running atomic.Bool
	n := engine.DefaultOutputBufferSize * 3
	source := &lifecycleSource{remaining: n, running: &running}
	m, sink := &lifecycleMap{}, &lifecycleSink{}
	r, desc := lifecyclePipeline(source, m, sink)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := newTaskExecutor(r).run(ctx, "job", "task", desc, zerolog.Nop(), func() {
		for _, p := range []*lifecycleProbe{&source.lifecycleProbe, &m.lifecycleProbe, &sink.lifecycleProbe} {
			if p.opened.Load() != 1 {
				t.Error("RUNNING before all operators opened")
			}
		}
		running.Store(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("terminal chain stalled")
	}
	if sink.count.Load() != int32(n) {
		t.Fatalf("got %d records, want %d", sink.count.Load(), n)
	}
	for _, p := range []*lifecycleProbe{&source.lifecycleProbe, &m.lifecycleProbe, &sink.lifecycleProbe} {
		if p.opened.Load() != 1 || p.closed.Load() != 1 {
			t.Errorf("open=%d close=%d", p.opened.Load(), p.closed.Load())
		}
	}
}

func TestTaskExecutor_OpenFailureUnwindsWithoutRunning(t *testing.T) {
	for _, panicOpen := range []bool{false, true} {
		t.Run(fmt.Sprintf("panic=%v", panicOpen), func(t *testing.T) {
			var running atomic.Bool
			source := &lifecycleSource{running: &running}
			m, sink := &lifecycleMap{}, &lifecycleSink{}
			m.closePanic = true // A broken cleanup must not prevent source cleanup.
			sink.openPanic = panicOpen
			if !panicOpen {
				sink.openErr = errors.New("cannot open sink")
			}
			r, desc := lifecyclePipeline(source, m, sink)
			err := newTaskExecutor(r).run(context.Background(), "job", "task", desc, zerolog.Nop(), func() { running.Store(true) })
			if err == nil || running.Load() {
				t.Fatalf("err=%v running=%v", err, running.Load())
			}
			if source.closed.Load() != 1 || m.closed.Load() != 1 || sink.closed.Load() != 0 {
				t.Fatal("partial initialization did not unwind")
			}
			if panicOpen && !errors.Is(err, engine.ErrOperatorPanic) {
				t.Fatalf("lost panic: %v", err)
			}
		})
	}
}

// Capture status reports over real RPC framing, rather than substituting a
// callback for the worker's lifecycle. The sessions use an in-memory pipe.
func workerWithStatusCapture(t *testing.T, r *Registry, running *atomic.Bool) (*Worker, chan rpc.UpdateTaskStatusRequest) {
	t.Helper()
	a, b := net.Pipe()
	yc, err := yamux.Client(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	ys, err := yamux.Server(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	statuses := make(chan rpc.UpdateTaskStatusRequest, 8)
	server := rpc.NewServer(rpc.DefaultConfig())
	server.Register(rpc.MethodUpdateTaskStatus, func(_ context.Context, _ uint64, payload []byte) (any, *rpc.RPCError) {
		var req rpc.UpdateTaskStatusRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
		}
		if req.Status == rpc.TaskStatusRunning {
			running.Store(true)
		}
		statuses <- req
		return &rpc.UpdateTaskStatusResponse{}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); server.ServeSession(ctx, ys) }()
	t.Cleanup(func() { cancel(); _ = yc.Close(); _ = ys.Close(); server.Stop(); <-done })
	w := NewWithRegistry(Config{}, r, zerolog.Nop())
	w.client = rpc.NewClient(yc, rpc.DefaultConfig())
	return w, statuses
}

func TestWorker_TaskLifecycleStatuses(t *testing.T) {
	cases := []struct {
		name         string
		want         []rpc.TaskStatus
		panicFailure bool
	}{
		{"success", []rpc.TaskStatus{rpc.TaskStatusRunning, rpc.TaskStatusFinished}, false},
		{"open_error", []rpc.TaskStatus{rpc.TaskStatusFailed}, false},
		{"factory_panic", []rpc.TaskStatus{rpc.TaskStatusFailed}, true},
		{"source_panic", []rpc.TaskStatus{rpc.TaskStatusRunning, rpc.TaskStatusFailed}, true},
		{"map_panic", []rpc.TaskStatus{rpc.TaskStatusRunning, rpc.TaskStatusFailed}, true},
		{"source_error", []rpc.TaskStatus{rpc.TaskStatusRunning, rpc.TaskStatusFailed}, false},
		{"cancel", []rpc.TaskStatus{rpc.TaskStatusRunning, rpc.TaskStatusCanceled}, false},
		{"unknown_operator", []rpc.TaskStatus{rpc.TaskStatusFailed}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var running atomic.Bool
			source := &lifecycleSource{remaining: 2, running: &running}
			m, sink := &lifecycleMap{}, &lifecycleSink{}
			r, desc := lifecyclePipeline(source, m, sink)
			switch tc.name {
			case "open_error":
				sink.openErr = errors.New("open failed")
			case "factory_panic":
				r.RegisterSource("panic", func(context.Context, []byte, TaskContext) (engine.SourceOperator, error) { panic("factory panic") })
				desc.OperatorChain[0].ClassName = "panic"
			case "source_panic":
				source.readPanic = true
			case "map_panic":
				m.processPanic = true
			case "source_error":
				source.readErr = errors.New("source failed")
			case "unknown_operator":
				desc.OperatorChain[1].ClassName = "missing"
			case "cancel":
				source.block = true
			}
			w, reports := workerWithStatusCapture(t, r, &running)
			watchCtx, watchCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer watchCancel()
			ctx, cancel := context.WithCancel(watchCtx)
			defer cancel()
			done := make(chan struct{})
			w.tasks["task"] = &taskHandle{cancel: cancel}
			go func() { defer close(done); w.runTask(ctx, "job", "task", desc, zerolog.Nop()) }()
			var got []rpc.TaskStatus
			for range tc.want {
				select {
				case req := <-reports:
					got = append(got, req.Status)
					if tc.name == "cancel" && req.Status == rpc.TaskStatusRunning {
						w.handleCommands([]rpc.WorkerCommand{{Type: rpc.CommandTypeCancelTask, TaskID: "task"}})
					}
					if tc.panicFailure && req.Status == rpc.TaskStatusFailed {
						if req.Failure == nil || !strings.Contains(req.Failure.StackTrace, "goroutine") {
							t.Fatalf("missing panic stack: %+v", req.Failure)
						}
					}
				case <-watchCtx.Done():
					t.Fatal("missing lifecycle status")
				}
			}
			// CancelTask cancels ctx intentionally; use an independent completion bound.
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("task did not exit")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("statuses %v, want %v", got, tc.want)
			}
			if len(w.tasks) != 0 {
				t.Fatal("task handle leaked")
			}
			if source.opened.Load() == 1 && source.closed.Load() != 1 {
				t.Fatal("source not closed")
			}
		})
	}
}
