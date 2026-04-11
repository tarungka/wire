package engine

import (
	"context"
	"testing"
)

func BenchmarkOperatorChain_MultiStage(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	inputCh := make(chan Event, 1024)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 4096)
	aligner := NewBarrierAligner(1, 100)

	// Map -> FlatMap(2x) -> Map: exercises pool swap logic across 3 operators.
	ops := []Operator{&noopMap{}, &doublerFlatMap{}, &noopMap{}}

	go func() {
		for range outputCh {
		}
	}()
	go func() {
		for i := 0; i < b.N; i++ {
			inputCh <- Event{Value: []byte("bench"), EventTime: int64(i)}
		}
		close(inputCh)
	}()

	b.ResetTimer()
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	close(outputCh)
}

func BenchmarkBarrierAligner_DrainAll_Large(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		ba := NewBarrierAligner(4, 4096)
		for inp := 0; inp < 4; inp++ {
			ba.OnBarrier(inp, uint64(i+1), 1)
			for j := 0; j < 1000; j++ {
				_ = ba.BufferEvent(ctx, inp, Event{Value: []byte("x")})
			}
		}
		ba.DrainAll(uint64(i + 1))
		ba.Reset(uint64(i + 1))
	}
}

func BenchmarkOperatorChain_MapPassthrough(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	inputCh := make(chan Event, 1024)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 1024)
	aligner := NewBarrierAligner(1, 100)

	ops := []Operator{&noopMap{}}

	// Drain output in background.
	go func() {
		for range outputCh {
		}
	}()

	go func() {
		for i := 0; i < b.N; i++ {
			inputCh <- Event{Value: []byte("bench"), EventTime: int64(i)}
		}
		close(inputCh)
	}()

	b.ResetTimer()
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	close(outputCh)
}

func BenchmarkOperatorChain_FlatMap(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	inputCh := make(chan Event, 1024)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 2048)
	aligner := NewBarrierAligner(1, 100)

	ops := []Operator{&doublerFlatMap{}}

	go func() {
		for range outputCh {
		}
	}()

	go func() {
		for i := 0; i < b.N; i++ {
			inputCh <- Event{Value: []byte("bench"), EventTime: int64(i)}
		}
		close(inputCh)
	}()

	b.ResetTimer()
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	close(outputCh)
}

func BenchmarkOperatorChain_Sink(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	inputCh := make(chan Event, 1024)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 1024)
	aligner := NewBarrierAligner(1, 100)

	sink := &atomicCountingSink{}
	ops := []Operator{sink}

	go func() {
		for range outputCh {
		}
	}()

	go func() {
		for i := 0; i < b.N; i++ {
			inputCh <- Event{Value: []byte("bench"), EventTime: int64(i)}
		}
		close(inputCh)
	}()

	b.ResetTimer()
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	close(outputCh)
}

func BenchmarkOperatorChain_MapPassthrough_WithErrorHandler(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	inputCh := make(chan Event, 1024)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 1024)
	aligner := NewBarrierAligner(1, 100)

	ops := []Operator{&noopMap{}}

	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "bench-map",
		MaxRetries:   3,
		OnExhausted:  RouteToDLQ,
	}}

	go func() {
		for range outputCh {
		}
	}()

	go func() {
		for i := 0; i < b.N; i++ {
			inputCh <- Event{Value: []byte("bench"), EventTime: int64(i)}
		}
		close(inputCh)
	}()

	b.ResetTimer()
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, nil, NoopErrorMetrics())
	close(outputCh)
}

func BenchmarkBarrierAligner_BufferDrain(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		ba := NewBarrierAligner(2, 4096)
		ba.OnBarrier(0, uint64(i+1), 1)
		for j := 0; j < 100; j++ {
			_ = ba.BufferEvent(ctx, 0, Event{Value: []byte("x")})
		}
		ba.OnBarrier(1, uint64(i+1), 1)
		ba.DrainAll(uint64(i + 1))
		ba.Reset(uint64(i + 1))
	}
}
