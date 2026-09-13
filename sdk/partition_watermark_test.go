package sdk

import (
	"context"
	"errors"
	"reflect"
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
	close(first)
	close(second)
	downstream := make(chan engine.Event, 4)
	control := make(chan engine.ControlMsg, 1)
	router := &partitionRouter{upstreams: []<-chan engine.OutputMsg{first, second}, downstreams: []chan<- engine.Event{downstream}, controlChs: []chan<- engine.ControlMsg{control}, routeFn: forwardRouter(0)}
	if err := router.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, boundary := <-downstream, <-downstream
	if string(record.Value) != "record" {
		t.Fatal("watermark overtook record")
	}
	if !reflect.DeepEqual(boundary, engine.WatermarkEvent(80)) {
		t.Fatalf("wrong minimum boundary: %+v", boundary)
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
