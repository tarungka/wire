package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestExponentialBackoffBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		initial, max time.Duration
		multiplier   float64
		attempt      int
		want         time.Duration
	}{
		{"zero initial", 0, time.Second, 2, 1, 0},
		{"zero cap", time.Millisecond, 0, 2, 1, 0},
		{"negative attempt", time.Millisecond, time.Second, 2, -1, time.Millisecond},
		{"NaN multiplier", time.Millisecond, time.Second, math.NaN(), 3, time.Millisecond},
		{"small multiplier", time.Millisecond, time.Second, 0.5, 3, time.Millisecond},
		{"overflow", time.Second, time.Hour, 2, 10000, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExponentialBackoff(tc.initial, tc.max, tc.multiplier)(tc.attempt); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRetryCancellationBoundaries(t *testing.T) {
	for _, where := range []string{"before invocation", "classification", "backoff"} {
		t.Run(where, func(t *testing.T) {
			metrics := newTrackingErrorMetrics()
			cc := newTestChainContext(t, nil, metrics)
			ctx, cancel := context.WithCancel(cc.ctx)
			defer cancel()
			cc.ctx = ctx
			calls := 0
			cfg := ErrorHandlerConfig{MaxRetries: 2, OnExhausted: DropEvent, OperatorName: "op"}
			switch where {
			case "before invocation":
				cancel()
			case "classification":
				cfg.Classifier = func(error) ErrorClass { cancel(); return ErrorClassTransient }
			case "backoff":
				cfg.Backoff = func(int) time.Duration { cancel(); return 0 }
			}
			err := invokeWithRetry(cc, ChainLink{Config: cfg}, Event{}, func() error { calls++; return ErrTransient })
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			want := 1
			if where == "before invocation" {
				want = 0
			}
			if calls != want || metrics.retries["op"] != 0 || metrics.drops["op"] != 0 {
				t.Fatalf("calls=%d retries=%d drops=%d", calls, metrics.retries["op"], metrics.drops["op"])
			}
		})
	}
}

func TestRetryBecomesPoisonAndDeliversDLQ(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	calls, delivered := 0, 0
	cfg := ErrorHandlerConfig{MaxRetries: 5, OnExhausted: RouteToDLQ, OperatorName: "op", DLQWriter: func(_ context.Context, event DLQEvent) error {
		delivered++
		if event.RetryCount != 1 || event.Error != "bad record" || string(event.OriginalEvent.Value) != "original" {
			t.Fatalf("wrong DLQ: %+v", event)
		}
		return nil
	}}
	err := invokeWithRetry(cc, ChainLink{Config: cfg}, Event{Value: []byte("original")}, func() error {
		calls++
		if calls == 1 {
			return ErrTransient
		}
		return errors.New("bad record")
	})
	if err != nil || calls != 2 || delivered != 1 || metrics.retries["op"] != 1 || metrics.dlqs["op"] != 1 || metrics.drops["op"] != 0 {
		t.Fatalf("err=%v calls=%d DLQ=%d metrics=%+v", err, calls, delivered, metrics)
	}
}

func TestRetryFinishesBeforeCheckpointBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input := make(chan Event, 1)
	controls := make(chan ControlMsg, 1)
	output := make(chan OutputMsg, 3)
	aligner := NewBarrierAligner(1, 100)
	waiting, release := make(chan struct{}), make(chan struct{})
	cfg := ErrorHandlerConfig{MaxRetries: 1, Backoff: func(int) time.Duration {
		close(waiting)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return 0
	}}
	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, []Operator{&partialErrorMap{succeed: true}}, input, controls, output, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, []ErrorHandlerConfig{cfg}, nil, NoopErrorMetrics())
	}()
	input <- Event{Value: []byte("original")}
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	aligner.OnBarrier(0, 42, 7)
	controls <- ControlMsg{Type: CtrlBarrierReceived, InputIndex: 0, CheckpointID: 42, EpochID: 7}
	close(input)
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if len(output) < 2 {
		t.Fatalf("only %d output messages", len(output))
	}
	first, second := <-output, <-output
	if first.Type != OutputData || string(first.Event.Value) != "success" || second.Type != OutputBarrier || second.Barrier.CheckpointID != 42 {
		t.Fatalf("barrier overtook retry: first=%+v second=%+v", first, second)
	}
}

func TestStandardErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want ErrorClass
	}{
		{syscall.ENOSPC, ErrorClassFatal}, {syscall.ENOMEM, ErrorClassFatal},
		{syscall.ECONNRESET, ErrorClassTransient}, {syscall.ECONNREFUSED, ErrorClassTransient}, {syscall.EPIPE, ErrorClassTransient},
		{&net.DNSError{IsTimeout: true}, ErrorClassTransient},
		{&net.DNSError{Err: "name not found"}, ErrorClassPoison},
	} {
		if got := defaultClassifier(fmt.Errorf("wrapped: %w", tc.err)); got != tc.want {
			t.Fatalf("%v: got %v want %v", tc.err, got, tc.want)
		}
	}
	for _, fatal := range []error{ErrFatal, syscall.ENOSPC, syscall.ENOMEM} {
		if got := classify(ErrorHandlerConfig{Classifier: func(error) ErrorClass { return ErrorClassPoison }}, fatal); got != ErrorClassFatal {
			t.Fatalf("fatal error downgraded: %v", fatal)
		}
	}
	if got := classify(ErrorHandlerConfig{Classifier: func(error) ErrorClass { return ErrorClass(255) }}, errors.New("unknown")); got != ErrorClassFatal {
		t.Fatal("invalid classification did not fail closed")
	}
}
