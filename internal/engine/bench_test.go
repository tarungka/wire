package engine

import (
	"context"
	"testing"
)

func BenchmarkOperatorChain_MapPassthrough(b *testing.B) {
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
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil)
	close(outputCh)
}

func BenchmarkOperatorChain_FlatMap(b *testing.B) {
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
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil)
	close(outputCh)
}

func BenchmarkOperatorChain_Sink(b *testing.B) {
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
	_ = runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil)
	close(outputCh)
}

func BenchmarkBarrierAligner_BufferDrain(b *testing.B) {
	ctx := context.Background()

	for i := 0; i < b.N; i++ {
		ba := NewBarrierAligner(2, 4096)
		ba.OnBarrier(0, uint64(i+1), 1)
		for j := 0; j < 100; j++ {
			ba.BufferEvent(ctx, 0, Event{Value: []byte("x")})
		}
		ba.OnBarrier(1, uint64(i+1), 1)
		ba.DrainAll(uint64(i + 1))
		ba.Reset(uint64(i + 1))
	}
}
