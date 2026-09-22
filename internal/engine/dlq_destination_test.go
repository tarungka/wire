package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type failingLifecycleDLQ struct {
	openFailure, closeFailure bool
	panicFailure              bool
	opened, closed, writes    int
}

func (s *failingLifecycleDLQ) Open(context.Context) error {
	s.opened++
	if s.openFailure {
		if s.panicFailure {
			panic("open failure")
		}
		return errors.New("open failure")
	}
	return nil
}
func (s *failingLifecycleDLQ) Close() error {
	s.closed++
	if s.closeFailure {
		if s.panicFailure {
			panic("close failure")
		}
		return errors.New("close failure")
	}
	return nil
}
func (s *failingLifecycleDLQ) Write(context.Context, Event) error { s.writes++; return nil }

func TestDLQLifecycleFailureIsolation(t *testing.T) {
	for _, panics := range []bool{false, true} {
		for _, openFailure := range []bool{false, true} {
			sink := &failingLifecycleDLQ{openFailure: openFailure, closeFailure: true, panicFailure: panics}
			destination := OpenDLQDestination(context.Background(), sink, testLogger())
			metrics := newTrackingErrorMetrics()
			cc := newTestChainContext(t, nil, metrics)
			err := invokeWithRetry(cc, ChainLink{Config: ErrorHandlerConfig{OperatorName: "parse", OnExhausted: RouteToDLQ, DLQWriter: destination.Write}}, Event{}, func() error { return errors.New("bad record") })
			if err != nil {
				t.Fatal(err)
			}
			destination.Close()
			if sink.opened != 1 || sink.closed != 1 {
				t.Fatalf("wrong lifecycle: %+v", sink)
			}
			if openFailure {
				if sink.writes != 0 || metrics.drops["parse"] != 1 || metrics.dlqs["parse"] != 0 {
					t.Fatal("failed open counted as delivery")
				}
			} else if sink.writes != 1 || metrics.dlqs["parse"] != 1 || metrics.drops["parse"] != 0 {
				t.Fatal("successful write not counted")
			}
		}
	}
}

func TestCancellationDuringDLQWrite(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	ctx, cancel := context.WithTimeout(cc.ctx, 3*time.Second)
	defer cancel()
	cc.ctx = ctx
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- invokeWithRetry(cc, ChainLink{Config: ErrorHandlerConfig{OperatorName: "parse", OnExhausted: RouteToDLQ, DLQWriter: func(writeCtx context.Context, _ DLQEvent) error {
			close(started)
			<-writeCtx.Done()
			return writeCtx.Err()
		}}}, Event{}, func() error { return errors.New("bad record") })
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if metrics.drops["parse"] != 0 || metrics.dlqs["parse"] != 0 {
		t.Fatal("cancellation counted as drop/delivery")
	}
}

func TestCancellationBeforeDLQDecision(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handleExhausted(ctx, ErrorHandlerConfig{OnExhausted: DropEvent}, Event{}, errors.New("bad"), 0, nil, metrics, testLogger()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMissingDLQLogsAndCountsDrop(t *testing.T) {
	var output bytes.Buffer
	metrics := newTrackingErrorMetrics()
	err := handleExhausted(context.Background(), ErrorHandlerConfig{OperatorName: "parse", OnExhausted: RouteToDLQ}, Event{}, errors.New("poison"), 0, nil, metrics, zerolog.New(&output))
	if err != nil || metrics.drops["parse"] != 1 {
		t.Fatalf("err=%v drops=%d", err, metrics.drops["parse"])
	}
	if !strings.Contains(output.String(), `"level":"error"`) || !strings.Contains(output.String(), "DLQ not configured") {
		t.Fatalf("missing diagnostic: %s", output.String())
	}
}
