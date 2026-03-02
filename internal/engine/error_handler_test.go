package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// -- Backoff strategy tests --

func TestFixedBackoff(t *testing.T) {
	b := FixedBackoff(100 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if got := b(i); got != 100*time.Millisecond {
			t.Errorf("attempt %d: got %v, want 100ms", i, got)
		}
	}
}

func TestExponentialBackoff(t *testing.T) {
	b := ExponentialBackoff(10*time.Millisecond, 200*time.Millisecond, 2.0)

	expected := []time.Duration{
		10 * time.Millisecond,  // attempt 0: 10ms
		20 * time.Millisecond,  // attempt 1: 10*2
		40 * time.Millisecond,  // attempt 2: 10*2*2
		80 * time.Millisecond,  // attempt 3: 10*2*2*2
		160 * time.Millisecond, // attempt 4: 10*2^4
		200 * time.Millisecond, // attempt 5: capped at 200ms
	}

	for i, want := range expected {
		got := b(i)
		if got != want {
			t.Errorf("attempt %d: got %v, want %v", i, got, want)
		}
	}
}

// -- Error classifier tests --

func TestDefaultClassifier(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"transient", fmt.Errorf("conn reset: %w", ErrTransient), ErrorClassTransient},
		{"fatal", fmt.Errorf("oom: %w", ErrFatal), ErrorClassFatal},
		{"unknown", errors.New("something broke"), ErrorClassPoison},
		{"wrapped transient", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrTransient)), ErrorClassTransient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultClassifier(tt.err)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestClassify_CustomClassifier(t *testing.T) {
	cfg := ErrorHandlerConfig{
		Classifier: func(err error) ErrorClass {
			return ErrorClassFatal // Always fatal.
		},
	}
	got := classify(cfg, errors.New("anything"))
	if got != ErrorClassFatal {
		t.Errorf("got %d, want ErrorClassFatal", got)
	}
}

// -- safeInvoke tests --

func TestSafeInvoke_Success(t *testing.T) {
	err := safeInvoke(func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestSafeInvoke_Error(t *testing.T) {
	expected := errors.New("fail")
	err := safeInvoke(func() error { return expected })
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestSafeInvoke_Panic(t *testing.T) {
	err := safeInvoke(func() error {
		panic("boom")
	})
	if !errors.Is(err, ErrOperatorPanic) {
		t.Fatalf("expected ErrOperatorPanic, got: %v", err)
	}
}

// -- trackingErrorMetrics for testing --

type trackingErrorMetrics struct {
	mu           sync.Mutex
	errors       map[string]int
	retries      map[string]int
	dlqs         map[string]int
	dlqOverflows map[string]int
	drops        map[string]int
}

func newTrackingErrorMetrics() *trackingErrorMetrics {
	return &trackingErrorMetrics{
		errors:       make(map[string]int),
		retries:      make(map[string]int),
		dlqs:         make(map[string]int),
		dlqOverflows: make(map[string]int),
		drops:        make(map[string]int),
	}
}

func (m *trackingErrorMetrics) IncErrorTotal(op string) {
	m.mu.Lock()
	m.errors[op]++
	m.mu.Unlock()
}
func (m *trackingErrorMetrics) IncRetryTotal(op string) {
	m.mu.Lock()
	m.retries[op]++
	m.mu.Unlock()
}
func (m *trackingErrorMetrics) IncDLQTotal(op string) {
	m.mu.Lock()
	m.dlqs[op]++
	m.mu.Unlock()
}
func (m *trackingErrorMetrics) IncDLQOverflowTotal(op string) {
	m.mu.Lock()
	m.dlqOverflows[op]++
	m.mu.Unlock()
}
func (m *trackingErrorMetrics) IncDropTotal(op string) {
	m.mu.Lock()
	m.drops[op]++
	m.mu.Unlock()
}

func (m *trackingErrorMetrics) get(store map[string]int, op string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return store[op]
}

// -- invokeWithRetry tests --

func newTestChainContext(t *testing.T, dlqCh chan<- DLQEvent, metrics *trackingErrorMetrics) *chainContext {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return &chainContext{
		ctx:        ctx,
		dlqCh:      dlqCh,
		errMetrics: metrics,
		log:        testLogger(),
	}
}

func TestInvokeWithRetry_SuccessNoRetry(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{OperatorName: "op"}}

	err := invokeWithRetry(cc, link, Event{}, func() error { return nil })
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if metrics.get(metrics.errors, "op") != 0 {
		t.Error("should not have recorded errors")
	}
}

func TestInvokeWithRetry_TransientRetryThenSuccess(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   3,
	}}

	attempt := 0
	err := invokeWithRetry(cc, link, Event{}, func() error {
		attempt++
		if attempt <= 2 {
			return fmt.Errorf("transient: %w", ErrTransient)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil after retry success, got %v", err)
	}
	if metrics.get(metrics.retries, "op") != 2 {
		t.Errorf("retries: got %d, want 2", metrics.get(metrics.retries, "op"))
	}
}

func TestInvokeWithRetry_TransientExhausted_FailJob(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   2,
		OnExhausted:  FailJob,
	}}

	err := invokeWithRetry(cc, link, Event{}, func() error {
		return fmt.Errorf("transient: %w", ErrTransient)
	})
	if !errors.Is(err, ErrRetriesExhausted) {
		t.Fatalf("expected ErrRetriesExhausted, got: %v", err)
	}
}

func TestInvokeWithRetry_TransientExhausted_RouteToDLQ(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	dlqCh := make(chan DLQEvent, 10)
	cc := newTestChainContext(t, dlqCh, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   1,
		OnExhausted:  RouteToDLQ,
	}}

	event := Event{Key: []byte("k"), Value: []byte("v")}
	err := invokeWithRetry(cc, link, event, func() error {
		return fmt.Errorf("transient: %w", ErrTransient)
	})
	if err != nil {
		t.Fatalf("expected nil (DLQ'd), got %v", err)
	}

	if len(dlqCh) != 1 {
		t.Fatalf("expected 1 DLQ event, got %d", len(dlqCh))
	}
	dlqEvent := <-dlqCh
	if dlqEvent.OperatorName != "op" {
		t.Errorf("DLQ operator: got %q, want %q", dlqEvent.OperatorName, "op")
	}
	if string(dlqEvent.OriginalEvent.Key) != "k" {
		t.Errorf("DLQ event key: got %q, want %q", dlqEvent.OriginalEvent.Key, "k")
	}
	if metrics.get(metrics.dlqs, "op") != 1 {
		t.Errorf("DLQ metrics: got %d, want 1", metrics.get(metrics.dlqs, "op"))
	}
}

func TestInvokeWithRetry_TransientExhausted_DropEvent(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   1,
		OnExhausted:  DropEvent,
	}}

	err := invokeWithRetry(cc, link, Event{}, func() error {
		return fmt.Errorf("transient: %w", ErrTransient)
	})
	if err != nil {
		t.Fatalf("expected nil (dropped), got %v", err)
	}
	if metrics.get(metrics.drops, "op") != 1 {
		t.Errorf("drop metrics: got %d, want 1", metrics.get(metrics.drops, "op"))
	}
}

func TestInvokeWithRetry_PoisonGoesToDLQ(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	dlqCh := make(chan DLQEvent, 10)
	cc := newTestChainContext(t, dlqCh, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   5,
		OnExhausted:  RouteToDLQ,
	}}

	// Poison errors skip retries entirely.
	err := invokeWithRetry(cc, link, Event{}, func() error {
		return errors.New("bad record") // default classifier → Poison
	})
	if err != nil {
		t.Fatalf("expected nil (DLQ'd), got %v", err)
	}
	if metrics.get(metrics.retries, "op") != 0 {
		t.Error("poison should not trigger retries")
	}
	if len(dlqCh) != 1 {
		t.Fatalf("expected 1 DLQ event, got %d", len(dlqCh))
	}
}

func TestInvokeWithRetry_PoisonWithFailJob(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   5,
		OnExhausted:  FailJob, // zero value
	}}

	err := invokeWithRetry(cc, link, Event{}, func() error {
		return errors.New("bad record")
	})
	if err == nil {
		t.Fatal("expected error for FailJob on poison")
	}
}

func TestInvokeWithRetry_FatalSkipsRetry(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   5,
		OnExhausted:  RouteToDLQ,
	}}

	err := invokeWithRetry(cc, link, Event{}, func() error {
		return fmt.Errorf("oom: %w", ErrFatal)
	})
	if !errors.Is(err, ErrFatal) {
		t.Fatalf("expected ErrFatal, got: %v", err)
	}
	if metrics.get(metrics.retries, "op") != 0 {
		t.Error("fatal should not trigger retries")
	}
}

func TestInvokeWithRetry_PanicRoutesDLQ(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	dlqCh := make(chan DLQEvent, 10)
	cc := newTestChainContext(t, dlqCh, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   2,
		OnExhausted:  RouteToDLQ,
	}}

	// ErrOperatorPanic is classified as Poison by default → DLQ.
	err := invokeWithRetry(cc, link, Event{}, func() error {
		panic("boom")
	})
	if err != nil {
		t.Fatalf("expected nil (DLQ'd after panic), got %v", err)
	}
	if len(dlqCh) != 1 {
		t.Fatalf("expected 1 DLQ event, got %d", len(dlqCh))
	}
}

func TestInvokeWithRetry_ContextCancelDuringBackoff(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	ctx, cancel := context.WithCancel(context.Background())
	cc := &chainContext{
		ctx:        ctx,
		errMetrics: metrics,
		log:        testLogger(),
	}
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   10,
		Backoff:      FixedBackoff(10 * time.Second), // Very long backoff.
	}}

	// Cancel after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := invokeWithRetry(cc, link, Event{}, func() error {
		return fmt.Errorf("transient: %w", ErrTransient)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestInvokeWithRetry_ZeroConfig_LegacyBehavior(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{}} // All zero values.

	// Zero config: MaxRetries=0, OnExhausted=FailJob → any error fails.
	err := invokeWithRetry(cc, link, Event{}, func() error {
		return errors.New("any error")
	})
	if err == nil {
		t.Fatal("expected error with zero config")
	}
}

func TestInvokeWithRetry_BackoffWithExponential(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   3,
		Backoff:      ExponentialBackoff(1*time.Millisecond, 100*time.Millisecond, 2.0),
		OnExhausted:  DropEvent,
	}}

	start := time.Now()
	err := invokeWithRetry(cc, link, Event{}, func() error {
		return fmt.Errorf("transient: %w", ErrTransient)
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected nil (dropped), got %v", err)
	}
	// Should have slept 1ms + 2ms + 4ms = 7ms minimum.
	if elapsed < 5*time.Millisecond {
		t.Errorf("expected at least 5ms of backoff, got %v", elapsed)
	}
}

func TestInvokeWithRetry_TransientBecomeFatalDuringRetry(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	link := ChainLink{Config: ErrorHandlerConfig{
		OperatorName: "op",
		MaxRetries:   5,
		OnExhausted:  RouteToDLQ,
	}}

	attempt := 0
	err := invokeWithRetry(cc, link, Event{}, func() error {
		attempt++
		if attempt == 1 {
			return fmt.Errorf("transient: %w", ErrTransient)
		}
		return fmt.Errorf("now fatal: %w", ErrFatal)
	})
	if !errors.Is(err, ErrFatal) {
		t.Fatalf("expected ErrFatal after reclassification, got: %v", err)
	}
}

// -- buildChainLinks tests --

func TestBuildChainLinks_MatchesOperators(t *testing.T) {
	ops := []Operator{&noopMap{}, &noopMap{}, &noopMap{}}
	configs := []ErrorHandlerConfig{
		{OperatorName: "a"},
		{OperatorName: "b"},
	}

	links := buildChainLinks(ops, configs)
	if len(links) != 3 {
		t.Fatalf("got %d links, want 3", len(links))
	}
	if links[0].Config.OperatorName != "a" {
		t.Errorf("link[0] name: got %q, want %q", links[0].Config.OperatorName, "a")
	}
	if links[1].Config.OperatorName != "b" {
		t.Errorf("link[1] name: got %q, want %q", links[1].Config.OperatorName, "b")
	}
	// Link 2 should have zero-value config.
	if links[2].Config.OperatorName != "" {
		t.Errorf("link[2] name: got %q, want empty", links[2].Config.OperatorName)
	}
	if links[2].Config.MaxRetries != 0 {
		t.Errorf("link[2] maxRetries: got %d, want 0", links[2].Config.MaxRetries)
	}
}

func TestBuildChainLinks_NilConfigs(t *testing.T) {
	ops := []Operator{&noopMap{}}
	links := buildChainLinks(ops, nil)
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	if links[0].Config.MaxRetries != 0 {
		t.Error("expected zero-value config for nil configs")
	}
}

// -- handleExhausted tests --

func TestHandleExhausted_DLQChannelFull(t *testing.T) {
	// DLQ channel is full — should not block, just log and count overflow.
	dlqCh := make(chan DLQEvent) // unbuffered
	metrics := newTrackingErrorMetrics()

	err := handleExhausted(
		ErrorHandlerConfig{OperatorName: "op", OnExhausted: RouteToDLQ},
		Event{},
		errors.New("fail"),
		1,
		dlqCh,
		metrics,
		testLogger(),
	)
	if err != nil {
		t.Fatalf("expected nil even with full DLQ, got %v", err)
	}
	if metrics.get(metrics.dlqs, "op") != 0 {
		t.Error("DLQ metric should NOT be incremented when channel is full")
	}
	if metrics.get(metrics.dlqOverflows, "op") != 1 {
		t.Error("DLQ overflow metric should be incremented when channel is full")
	}
}

// T3: DLQ overflow — verify metrics accuracy vs actual enqueue count.
func TestHandleExhausted_DLQOverflow_MetricsAccuracy(t *testing.T) {
	// Buffered DLQ channel with capacity 2.
	dlqCh := make(chan DLQEvent, 2)
	metrics := newTrackingErrorMetrics()
	cfg := ErrorHandlerConfig{OperatorName: "op", OnExhausted: RouteToDLQ}

	// Send 5 events to the DLQ — only 2 should enqueue, 3 should overflow.
	for i := 0; i < 5; i++ {
		err := handleExhausted(cfg, Event{Key: []byte{byte(i)}}, errors.New("fail"), 0, dlqCh, metrics, testLogger())
		if err != nil {
			t.Fatalf("event %d: expected nil, got %v", i, err)
		}
	}

	// Verify: DLQ metric matches actual channel contents.
	actualEnqueued := len(dlqCh)
	if actualEnqueued != 2 {
		t.Fatalf("expected 2 events in DLQ channel, got %d", actualEnqueued)
	}
	if metrics.get(metrics.dlqs, "op") != 2 {
		t.Errorf("DLQ metric: got %d, want 2 (must match actual enqueue count)", metrics.get(metrics.dlqs, "op"))
	}
	if metrics.get(metrics.dlqOverflows, "op") != 3 {
		t.Errorf("DLQ overflow metric: got %d, want 3", metrics.get(metrics.dlqOverflows, "op"))
	}
}
