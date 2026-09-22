package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type partialErrorMap struct {
	noopMap
	attempts int
	succeed  bool
}

func (m *partialErrorMap) Map(_ context.Context, _ Event) (Event, error) {
	m.attempts++
	if m.succeed && m.attempts == 2 {
		return Event{Value: []byte("success")}, nil
	}
	return Event{Value: []byte("failed partial output")}, ErrTransient
}

type partialErrorFlatMap struct {
	noopMap
	attempts       int
	succeed        bool
	panicAfterEmit bool
}

func (m *partialErrorFlatMap) FlatMap(_ context.Context, _ Event, emit func(Event)) error {
	m.attempts++
	if m.succeed && m.attempts == 2 {
		emit(Event{Value: []byte("success")})
		return nil
	}
	emit(Event{Value: []byte("failed partial output")})
	if m.panicAfterEmit {
		panic("poison")
	}
	return ErrTransient
}

func TestErrorPolicyDiscardsPartialOutput(t *testing.T) {
	for _, action := range []ExhaustedAction{DropEvent, RouteToDLQ} {
		for _, kind := range []string{"map", "flatmap", "panic-flatmap"} {
			for _, succeed := range []bool{false, true} {
				if kind == "panic-flatmap" && succeed {
					continue
				}
				t.Run(fmt.Sprintf("%d/%s/recover=%t", action, kind, succeed), func(t *testing.T) {
					metrics := newTrackingErrorMetrics()
					dlq := make(chan DLQEvent, 1)
					cc := newTestChainContext(t, dlq, metrics)
					cfg := ErrorHandlerConfig{OperatorName: "transform", MaxRetries: 1, OnExhausted: action}
					var output []Event
					var err error
					if kind == "map" {
						op := &partialErrorMap{succeed: succeed}
						var event *Event
						event, err = invokeMapWithRetry(cc, ChainLink{Operator: op, Config: cfg}, Event{Value: []byte("original")}, op)
						if event != nil {
							output = append(output, *event)
						}
					} else {
						op := &partialErrorFlatMap{succeed: succeed, panicAfterEmit: kind == "panic-flatmap"}
						output, err = invokeFlatMapWithRetry(cc, ChainLink{Operator: op, Config: cfg}, Event{Value: []byte("original")}, op)
					}
					if err != nil {
						t.Fatal(err)
					}
					if succeed {
						if len(output) != 1 || string(output[0].Value) != "success" {
							t.Fatalf("retry output: %+v", output)
						}
					} else if len(output) != 0 {
						t.Fatalf("failed invocation leaked output: %+v", output)
					}
					if !succeed && action == RouteToDLQ {
						select {
						case record := <-dlq:
							if string(record.OriginalEvent.Value) != "original" {
								t.Fatal(record)
							}
						default:
							t.Fatal("missing DLQ record")
						}
					}
				})
			}
		}
	}
}

func TestRetryExhaustionPreservesOriginalError(t *testing.T) {
	metrics := newTrackingErrorMetrics()
	cc := newTestChainContext(t, nil, metrics)
	err := invokeWithRetry(cc, ChainLink{Config: ErrorHandlerConfig{MaxRetries: 1}}, Event{}, func() error { return ErrTransient })
	if !errors.Is(err, ErrRetriesExhausted) || !errors.Is(err, ErrTransient) {
		t.Fatalf("lost error cause: %v", err)
	}
}

func TestCancellationDuringInvocationDoesNotDropRecord(t *testing.T) {
	for _, action := range []ExhaustedAction{DropEvent, RouteToDLQ} {
		for _, cancelAttempt := range []int{1, 2} {
			t.Run(fmt.Sprintf("%d/attempt=%d", action, cancelAttempt), func(t *testing.T) {
				metrics := newTrackingErrorMetrics()
				dlq := make(chan DLQEvent, 1)
				cc := newTestChainContext(t, dlq, metrics)
				ctx, cancel := context.WithCancel(cc.ctx)
				defer cancel()
				cc.ctx = ctx
				attempts := 0
				err := invokeWithRetry(cc, ChainLink{Config: ErrorHandlerConfig{MaxRetries: 2, OnExhausted: action}}, Event{}, func() error {
					attempts++
					if attempts == cancelAttempt {
						cancel()
						return context.Canceled
					}
					return ErrTransient
				})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("got %v, want cancellation", err)
				}
				if attempts != cancelAttempt {
					t.Fatalf("attempts=%d, want %d", attempts, cancelAttempt)
				}
				if len(dlq) != 0 {
					t.Fatal("canceled record routed to DLQ")
				}
			})
		}
	}
}

func TestTaskSlotMissingDLQCountsDrop(t *testing.T) {
	cfg := DefaultTaskSlotConfig()
	cfg.ErrorConfigs = []ErrorHandlerConfig{{OperatorName: "parse", OnExhausted: RouteToDLQ}}
	metrics := newTrackingErrorMetrics()
	slot := NewTaskSlot(cfg, nil, nil, []Operator{&partialErrorMap{}}, newMockSource([][]Event{{{Value: []byte("original")}}}))
	slot.ErrorMetrics = metrics
	if err := slot.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if metrics.drops["parse"] != 1 || metrics.dlqs["parse"] != 0 {
		t.Fatalf("drops=%d DLQ=%d", metrics.drops["parse"], metrics.dlqs["parse"])
	}
}

type mutatingErrorOperator struct {
	noopMap
	seen []Event
}

func (o *mutatingErrorOperator) mutate(e Event) error {
	o.seen = append(o.seen, cloneEventPayload(e))
	e.Key[0] = 'X'
	e.Value[0] = 'X'
	e.Headers["header"][0] = 'X'
	e.Headers["extra"] = []byte("added")
	return ErrTransient
}
func (o *mutatingErrorOperator) Map(_ context.Context, e Event) (Event, error) { return e, o.mutate(e) }
func (o *mutatingErrorOperator) FlatMap(_ context.Context, e Event, emit func(Event)) error {
	emit(e)
	return o.mutate(e)
}
func (o *mutatingErrorOperator) Write(_ context.Context, e Event) error { return o.mutate(e) }

func TestRetryAndDLQPreserveOriginalPayload(t *testing.T) {
	for _, kind := range []string{"map", "flatmap", "sink"} {
		t.Run(kind, func(t *testing.T) {
			metrics := newTrackingErrorMetrics()
			dlq := make(chan DLQEvent, 1)
			cc := newTestChainContext(t, dlq, metrics)
			op := &mutatingErrorOperator{}
			event := Event{Key: []byte("key"), Value: []byte("value"), Headers: map[string][]byte{"header": []byte("original")}}
			link := ChainLink{Operator: op, Config: ErrorHandlerConfig{MaxRetries: 1, OnExhausted: RouteToDLQ}}
			var err error
			switch kind {
			case "map":
				_, err = invokeMapWithRetry(cc, link, event, op)
			case "flatmap":
				_, err = invokeFlatMapWithRetry(cc, link, event, op)
			case "sink":
				err = invokeSinkWithRetry(cc, link, event, op)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(op.seen) != 2 {
				t.Fatalf("attempts=%d", len(op.seen))
			}
			if len(dlq) != 1 {
				t.Fatal("missing DLQ")
			}
			op.seen = append(op.seen, event, (<-dlq).OriginalEvent)
			for _, seen := range op.seen {
				if string(seen.Key) != "key" || string(seen.Value) != "value" || string(seen.Headers["header"]) != "original" || len(seen.Headers) != 1 {
					t.Fatalf("payload mutated: %+v", seen)
				}
			}
		})
	}
}
