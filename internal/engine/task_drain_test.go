package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

type drainNotifyingMap struct {
	noopMap
	count    int
	produced chan struct{}
}

func (m *drainNotifyingMap) Map(_ context.Context, event Event) (Event, error) {
	m.count++
	if m.count == 2 {
		close(m.produced)
	}
	return event, nil
}

func TestTaskSlotCancellationDrainsProducedOutput(t *testing.T) {
	testTaskOutputDrain(t, true)
}
func TestTaskSlotCancellationBoundsBlockedOutput(t *testing.T) {
	testTaskOutputDrain(t, false)
}
func testTaskOutputDrain(t *testing.T, receive bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op := &drainNotifyingMap{produced: make(chan struct{})}
	input, output, slot := newTestPipeline(t, []Operator{op}, nil)
	slot.Config.DrainTimeout = 50 * time.Millisecond
	if receive {
		slot.Config.DrainTimeout = time.Second
	}
	done := make(chan error, 1)
	go func() { done <- slot.Run(ctx) }()
	value := make([]byte, 2*1024*1024)
	if err := input.WriteMessage(&protocol.DataRecordMsg{Value: value}); err != nil {
		t.Fatal(err)
	}
	if err := input.WriteMessage(&protocol.DataRecordMsg{Value: []byte("next")}); err != nil {
		t.Fatal(err)
	}
	// Entering the second Map proves the chain enqueued the first output.
	select {
	case <-op.produced:
	case <-time.After(time.Second):
		t.Fatal("first output was not produced")
	}
	cancel()
	if receive {
		msg, err := output.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if len(msg.(*protocol.DataRecordMsg).Value) != len(value) {
			t.Fatal("produced record truncated")
		}
	}

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("task did not join after draining")
	}
}
