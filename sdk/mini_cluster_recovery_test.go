package sdk

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// The first attempt fails only after a second checkpoint starts. Since the
// coordinator permits one active checkpoint, the first is durably completed.
type miniRecoverySource struct {
	first     bool
	offset    byte
	snapshots atomic.Int32
	restored  *atomic.Bool
}

func (*miniRecoverySource) Open(context.Context) error { return nil }
func (*miniRecoverySource) Close() error               { return nil }
func (*miniRecoverySource) GenerateWatermark() int64   { return 0 }
func (s *miniRecoverySource) ReadBatch(ctx context.Context) ([]Event, error) {
	if s.offset == 0 {
		s.offset = 1
		return []Event{{Key: []byte("k"), Value: []byte("one"), EventTime: 1}}, nil
	}
	if s.first {
		if s.snapshots.Load() >= 2 {
			return nil, errors.New("injected source failure after completed checkpoint")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
			return []Event{}, nil
		}
	}
	if !s.restored.Load() {
		return nil, errors.New("replacement source was not restored")
	}
	if s.offset == 1 {
		s.offset = 2
		return []Event{{Key: []byte("k"), Value: []byte("two"), EventTime: 2}}, nil
	}
	return nil, nil
}
func (s *miniRecoverySource) Checkpoint(uint64) ([]byte, error) {
	s.snapshots.Add(1)
	return []byte{s.offset}, nil
}
func (s *miniRecoverySource) RestoreOffset(ctx context.Context, state []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(state) != 1 || state[0] != 1 {
		return fmt.Errorf("invalid restored offset %v", state)
	}
	s.offset = state[0]
	s.restored.Store(true)
	return nil
}

func TestMiniClusterRestoresOffsetsAndManagedState(t *testing.T) {
	mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 2})
	defer mc.Shutdown()
	env := mc.GetExecutionEnvironment().SetCheckpointInterval(50 * time.Millisecond).SetRestartStrategy(FixedDelay(2, 0)).SetStateBackend(NewHashMapStateBackend(0))
	var instances atomic.Int32
	var restored atomic.Bool
	sink, side := &collectSink{}, &collectSink{}
	tag := NewOutputTag("timer-audit")
	stream := env.AddSourceFactory("source", func(InstanceContext) (Source, error) {
		return &miniRecoverySource{first: instances.Add(1) == 1, restored: &restored}, nil
	}).SetParallelism(1).KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).ProcessWithTimers(func(c ProcessContext, e Event) ([]Event, error) {
		n, err := c.GetState("count").ValueInt64()
		if err != nil {
			return nil, err
		}
		if err := c.GetState("count").SetInt64(n + 1); err != nil {
			return nil, err
		}
		if n == 0 {
			c.RegisterEventTimeTimer(10)
		}
		e.Value = []byte(fmt.Sprint(n + 1))
		return []Event{e}, nil
	}, func(c ProcessContext, ts int64) ([]Event, error) {
		n, err := c.GetState("count").ValueInt64()
		if err != nil {
			return nil, err
		}
		e := Event{Key: c.Key(), Value: []byte(fmt.Sprintf("timer%d", n)), EventTime: ts}
		c.EmitToSideOutput(tag, e)
		return []Event{e}, nil
	}).WithSideOutputs(tag)
	stream.AddSink(sink)
	stream.GetSideOutput(tag).AddSink(side)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err := env.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if !restored.Load() || instances.Load() != 2 {
		t.Fatalf("recovery missing: restored=%t instances=%d", restored.Load(), instances.Load())
	}
	events := sink.Events()
	if len(events) != 3 || string(events[0].Value) != "1" || string(events[1].Value) != "2" || string(events[2].Value) != "timer2" {
		t.Fatalf("state/offset replay wrong: %+v", events)
	}
	if audit := side.Events(); len(audit) != 1 || string(audit[0].Value) != "timer2" {
		t.Fatalf("restored timer side output: %+v", audit)
	}
}

func TestMiniClusterShutdownJoinsExecution(t *testing.T) {
	mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
	env := mc.GetExecutionEnvironment()
	opened := make(chan struct{})
	env.AddSource(&miniBlockingSource{opened: opened}).AddSink(&collectSink{})
	done := make(chan error, 1)
	go func() { _, err := env.Execute(t.Context()); done <- err }()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("source never opened")
	}
	if err := mc.Shutdown(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("execution result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown returned before execution joined")
	}
	if err := mc.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

type miniBlockingSource struct{ opened chan struct{} }

func (s *miniBlockingSource) Open(context.Context) error { close(s.opened); return nil }
func (*miniBlockingSource) Close() error                 { return nil }
func (*miniBlockingSource) GenerateWatermark() int64     { return 0 }
func (*miniBlockingSource) ReadBatch(ctx context.Context) ([]Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
