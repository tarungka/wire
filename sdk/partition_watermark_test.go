package sdk

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
)

func TestPartitionRouterForwardsMinimumAfterRecords(t *testing.T) {
	first := make(chan engine.OutputMsg, 2)
	second := make(chan engine.OutputMsg, 1)
	first <- engine.OutputMsg{Type: engine.OutputData, Event: engine.Event{EventTime: 10, Value: []byte("record")}}
	first <- engine.OutputMsg{Type: engine.OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: 100}}
	second <- engine.OutputMsg{Type: engine.OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: 80}}
	downstream := make(chan engine.Event, 4)
	control := make(chan engine.ControlMsg, 1)
	router := &partitionRouter{upstreams: []<-chan engine.OutputMsg{first, second}, downstreams: []chan<- engine.Event{downstream}, controlChs: []chan<- engine.ControlMsg{control}, routeFn: forwardRouter(0)}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- router.run(ctx) }()
	read := func() engine.Event {
		select {
		case event := <-downstream:
			return event
		case <-ctx.Done():
			t.Fatal("no boundary")
			return engine.Event{}
		}
	}
	record, boundary := read(), read()
	if string(record.Value) != "record" {
		t.Fatal("watermark overtook record")
	}
	if !reflect.DeepEqual(boundary, engine.WatermarkEvent(80)) {
		t.Fatalf("wrong minimum boundary: %+v", boundary)
	}
	close(first)
	close(second)
	var final engine.Event
	for event := range downstream {
		final = event
	}
	if !reflect.DeepEqual(final, engine.WatermarkEvent(math.MaxInt64)) {
		t.Fatal("missing final watermark")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPartitionRouterIdleInputAdvancesWithoutNewWatermark(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	active := make(chan engine.OutputMsg)
	idle := make(chan engine.OutputMsg)
	downstream := make(chan engine.Event)
	router := &partitionRouter{upstreams: []<-chan engine.OutputMsg{active, idle}, downstreams: []chan<- engine.Event{downstream}, routeFn: forwardRouter(0), idleTimeout: 50 * time.Millisecond, watermarkInterval: time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- router.run(ctx) }()
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		select {
		case active <- engine.OutputMsg{Type: engine.OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: 100}}:
		case <-ctx.Done():
			return
		}
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case active <- engine.OutputMsg{Type: engine.OutputData, Event: engine.Event{EventTime: 101}}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	found := false
	for !found {
		select {
		case event, ok := <-downstream:
			if !ok {
				t.Fatal("router closed before idle watermark")
			}
			found = reflect.DeepEqual(event, engine.WatermarkEvent(100))
		case <-ctx.Done():
			t.Fatal("idle input held back watermark")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("router cancellation: %v", err)
	}
	<-producerDone
}

func TestPartitionRouterSlowPartitionDoesNotBlockOtherRecords(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	slowInput := make(chan engine.OutputMsg)
	fastInput := make(chan engine.OutputMsg)
	slow := make(chan engine.Event, 1)
	fast := make(chan engine.Event, 1)
	slow <- engine.Event{Value: []byte("occupy capacity")}
	routedSlow := make(chan struct{})
	router := &partitionRouter{
		idleTimeout:       10 * time.Millisecond,
		watermarkInterval: time.Millisecond,
		upstreams:         []<-chan engine.OutputMsg{slowInput, fastInput},
		downstreams:       []chan<- engine.Event{slow, fast},
		routeFn: func(event engine.Event, _ int) int {
			if string(event.Value) == "slow" {
				close(routedSlow)
				return 0
			}
			return 1
		},
	}
	done := make(chan error, 1)
	go func() { done <- router.run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("router exit: %v", err)
		}
	}()
	slowInput <- engine.OutputMsg{Type: engine.OutputData, Event: engine.Event{Value: []byte("slow")}}
	<-routedSlow // The slow producer has entered its send path against a full channel.
	select {
	case fastInput <- engine.OutputMsg{Type: engine.OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: 100}}:
	case <-ctx.Done():
		t.Fatal("fast watermark input blocked")
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for range 10 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("router timed out")
		}
		select {
		case fastInput <- engine.OutputMsg{Type: engine.OutputData, Event: engine.Event{Value: []byte("fast")}}:
		case <-ctx.Done():
			t.Fatal("fast input blocked")
		}
		select {
		case event := <-fast:
			if string(event.Value) != "fast" {
				t.Fatalf("pending slow record was excluded as idle: %+v", event)
			}
		case <-ctx.Done():
			t.Fatal("full partition blocked an unrelated record")
		}
	}
}

func TestPartitionRouterAdvancingWatermarksDoNotBlockOtherPartitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inputs := []chan engine.OutputMsg{make(chan engine.OutputMsg), make(chan engine.OutputMsg)}
	slow := make(chan engine.Event, 1)
	fast := make(chan engine.Event)
	slow <- engine.Event{Value: []byte("fill")}
	router := &partitionRouter{upstreams: []<-chan engine.OutputMsg{inputs[0], inputs[1]}, downstreams: []chan<- engine.Event{slow, fast}, routeFn: forwardRouter(1), idleTimeout: time.Hour, watermarkInterval: time.Hour}
	done := make(chan error, 1)
	go func() { done <- router.run(ctx) }()
	defer func() { cancel(); <-done }()
	send := func(input int, msg engine.OutputMsg) {
		t.Helper()
		select {
		case inputs[input] <- msg:
		case <-ctx.Done():
			t.Fatal("watermark blocked upstream intake")
		}
	}
	watermark := func(input int, ts int64) {
		t.Helper()
		send(input, engine.OutputMsg{Type: engine.OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: ts}})
	}
	watermark(0, 10)
	watermark(1, 10)
	watermark(1, 20)
	send(1, engine.OutputMsg{Type: engine.OutputData, Event: engine.Event{Value: []byte("first")}})
	// The fast partition may receive its watermark before or after the later
	// record. It must keep making progress while the slow partition stays full.
	observed := make([][]engine.Event, 2)
	receiveRecord := func(value string) {
		t.Helper()
		for {
			select {
			case event := <-fast:
				observed[1] = append(observed[1], event)
				if string(event.Value) == value {
					return
				}
			case <-ctx.Done():
				t.Fatal("full partition blocked watermark/data delivery elsewhere")
			}
		}
	}
	receiveRecord("first")
	// Move the actual minimum repeatedly, not merely one input's watermark.
	watermark(0, 30)
	watermark(1, 40)
	send(1, engine.OutputMsg{Type: engine.OutputData, Event: engine.Event{Value: []byte("second")}})
	receiveRecord("second")
	close(inputs[0])
	close(inputs[1])
	// Releasing slow capacity must flush its pending boundary before shutdown.
	if event := <-slow; string(event.Value) != "fill" {
		t.Fatalf("unexpected head: %+v", event)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	for partition, channel := range []chan engine.Event{slow, fast} {
		go func() {
			defer wg.Done()
			for event := range channel {
				observed[partition] = append(observed[partition], event)
			}
		}()
	}
	wg.Wait()
	for partition, events := range observed {
		last := int64(0)
		for _, event := range events {
			if len(event.Value) != 0 {
				continue
			}
			found := false
			for _, timestamp := range []int64{10, 20, 30, 40, math.MaxInt64} {
				if reflect.DeepEqual(event, engine.WatermarkEvent(timestamp)) {
					if timestamp <= last {
						t.Fatalf("partition %d regressed or duplicated watermark: %d after %d", partition, timestamp, last)
					}
					last, found = timestamp, true
					break
				}
			}
			if !found {
				t.Fatalf("unexpected boundary: %+v", event)
			}
		}
		if last != math.MaxInt64 {
			t.Fatalf("partition %d lost final coalesced watermark: %d", partition, last)
		}
	}
}

func TestPartitionRouterPreservesPerInputIdleTimeouts(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	first, second := make(chan engine.OutputMsg), make(chan engine.OutputMsg, 1)
	second <- engine.OutputMsg{Type: engine.OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: 100}}
	downstream := make(chan engine.Event, 4)
	router := &partitionRouter{upstreams: []<-chan engine.OutputMsg{first, second}, downstreams: []chan<- engine.Event{downstream}, routeFn: forwardRouter(0), inputIdleTimeouts: []time.Duration{time.Millisecond, time.Hour}, watermarkInterval: time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- router.run(ctx) }()
	select {
	case event := <-downstream:
		if !reflect.DeepEqual(event, engine.WatermarkEvent(100)) {
			t.Fatal("unexpected watermark")
		}
	case <-ctx.Done():
		t.Fatal("per-input timeout was lost")
	}
	close(first)
	close(second)
	for range downstream {
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
