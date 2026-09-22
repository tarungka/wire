package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/sdk/connectors/httpapi"
)

func TestMiniClusterErrorPolicyRouting(t *testing.T) {
	for _, scenario := range []string{"dlq", "drop", "fail", "panic-dlq", "missing-dlq"} {
		t.Run(scenario, func(t *testing.T) {
			mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
			defer mc.Shutdown()
			env := mc.GetExecutionEnvironment()
			main, dlq := &collectSink{}, &collectSink{}
			events := make([]Event, 100)
			for i := range events {
				events[i] = Event{Key: []byte(fmt.Sprint(i)), Value: []byte(fmt.Sprint(i))}
			}
			stream := env.AddSource(&sliceSource{events: events}).MapWithName("parse", func(e Event) (Event, error) {
				if string(e.Key) == "50" {
					if scenario == "panic-dlq" {
						panic("invalid record")
					}
					return Event{}, errors.New("invalid record")
				}
				return e, nil
			})
			action := scenario
			if scenario == "panic-dlq" || scenario == "missing-dlq" {
				action = "dlq"
			}
			stream.WithErrorHandler(ErrorHandler{OnExhausted: action})
			if scenario == "dlq" || scenario == "panic-dlq" {
				stream.WithDLQSink(dlq)
			}
			stream.AddSink(main)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := env.Execute(ctx)
			if scenario == "fail" {
				if err == nil || result == nil || result.Err == nil {
					t.Fatal("default fail policy did not fail execution")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(main.Events()) != 99 {
				t.Fatalf("main records=%d", len(main.Events()))
			}
			seen := map[string]bool{}
			for _, event := range main.Events() {
				key := string(event.Key)
				if seen[key] || key == "50" {
					t.Fatalf("duplicate or poison record %q", key)
				}
				seen[key] = true
			}
			if scenario == "dlq" || scenario == "panic-dlq" {
				if len(dlq.Events()) != 1 {
					t.Fatalf("DLQ records=%d", len(dlq.Events()))
				}
				var record struct {
					Original   struct{ Key, Value []byte } `json:"original_event"`
					Error      string                      `json:"error"`
					RetryCount int                         `json:"retry_count"`
				}
				if err := json.Unmarshal(dlq.Events()[0].Value, &record); err != nil {
					t.Fatal(err)
				}
				if string(record.Original.Key) != "50" || string(record.Original.Value) != "50" || record.RetryCount != 0 {
					t.Fatalf("wrong DLQ record: %+v", record)
				}
			} else if len(dlq.Events()) != 0 {
				t.Fatal("unexpected DLQ record")
			}
		})
	}
}

// The HTTP connector's production Write delegates to WriteBatch. Disable its
// own retries so recovery here must come from the WIP-11 operator policy.
func TestMiniClusterBatchSinkRetries(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(fmt.Sprintf("exhausted=%t", permanent), func(t *testing.T) {
			var mu sync.Mutex
			attempts := 0
			var accepted [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				attempts++
				if permanent || attempts < 3 {
					w.WriteHeader(503)
					return
				}
				accepted = append(accepted, body)
				w.WriteHeader(204)
			}))
			defer server.Close()
			sink, err := httpapi.NewSink(httpapi.SinkConfig{URL: server.URL, AllowInsecure: true, MaxAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
			defer mc.Shutdown()
			env := mc.GetExecutionEnvironment()
			env.AddSource(&sliceSource{events: []Event{{Value: []byte(`{"id":1}`)}, {Value: []byte(`{"id":2}`)}, {Value: []byte(`{"id":3}`)}}}).AddSink(sink).WithErrorHandler(ErrorHandler{MaxRetries: 2, Backoff: "fixed", InitialDelayMS: 1})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = env.Execute(ctx)
			mu.Lock()
			defer mu.Unlock()
			if permanent {
				if !errors.Is(err, engine.ErrRetriesExhausted) || !errors.Is(err, ErrTransient) || attempts != 3 || len(accepted) != 0 {
					t.Fatalf("err=%v attempts=%d accepted=%d", err, attempts, len(accepted))
				}
			} else {
				if err != nil || attempts != 5 || len(accepted) != 3 {
					t.Fatalf("err=%v attempts=%d accepted=%d", err, attempts, len(accepted))
				}
				for i, body := range accepted {
					var envelope struct {
						Events []struct {
							Value string `json:"value"`
						} `json:"events"`
					}
					if err := json.Unmarshal(body, &envelope); err != nil {
						t.Fatal(err)
					}
					if len(envelope.Events) != 1 || envelope.Events[0].Value != fmt.Sprintf(`{"id":%d}`, i+1) {
						t.Fatalf("wrong delivered batch: %s", body)
					}
				}
			}
		})
	}
}

func TestMiniClusterTransformationErrorPolicies(t *testing.T) {
	for _, kind := range []string{"filter", "flatmap", "process"} {
		t.Run(kind, func(t *testing.T) {
			mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
			defer mc.Shutdown()
			env := mc.GetExecutionEnvironment().SetStateBackend(NewHashMapStateBackend(1))
			source := env.AddSource(&sliceSource{events: []Event{{Value: []byte("good")}, {Value: []byte("bad")}, {Value: []byte("later")}}})
			var transform *DataStream
			fn := func(e Event) ([]Event, error) {
				if string(e.Value) == "bad" {
					return []Event{e}, errors.New("poison with partial output")
				}
				return []Event{e}, nil
			}
			switch kind {
			case "filter":
				transform = source.Filter(func(e Event) (bool, error) { _, err := fn(e); return true, err })
			case "flatmap":
				transform = source.FlatMap(fn)
			case "process":
				transform = source.KeyBy(func(e Event) ([]byte, error) { return e.Value, nil }).Process(func(_ ProcessContext, e Event) ([]Event, error) { return fn(e) })
			}
			main, dlq := &collectSink{}, &collectSink{}
			transform.WithErrorHandler(ErrorHandler{OnExhausted: "dlq"}).WithDLQSink(dlq).AddSink(main)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := env.Execute(ctx); err != nil {
				t.Fatal(err)
			}
			if len(main.Events()) != 2 || len(dlq.Events()) != 1 {
				t.Fatalf("main=%d dlq=%d", len(main.Events()), len(dlq.Events()))
			}
			for _, e := range main.Events() {
				if string(e.Value) == "bad" {
					t.Fatal("failed output leaked")
				}
			}
		})
	}
}

type acceptanceLifecycleSink struct {
	collectSink
	failOpen, panics bool
	opens, closes    int
}

func (s *acceptanceLifecycleSink) Open(context.Context) error {
	s.opens++
	if s.failOpen {
		if s.panics {
			panic("DLQ open")
		}
		return errors.New("DLQ open")
	}
	return nil
}
func (s *acceptanceLifecycleSink) Close() error {
	s.closes++
	if s.panics {
		panic("DLQ close")
	}
	return errors.New("DLQ close")
}
func TestMiniClusterDLQLifecycleFailures(t *testing.T) {
	for _, panics := range []bool{false, true} {
		for _, failOpen := range []bool{false, true} {
			t.Run(fmt.Sprintf("panic=%t/open=%t", panics, failOpen), func(t *testing.T) {
				mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
				defer mc.Shutdown()
				env := mc.GetExecutionEnvironment()
				main := &collectSink{}
				dlq := &acceptanceLifecycleSink{failOpen: failOpen, panics: panics}
				env.AddSource(&sliceSource{events: []Event{{Value: []byte("bad")}, {Value: []byte("good")}}}).Map(func(e Event) (Event, error) {
					if string(e.Value) == "bad" {
						return Event{}, errors.New("bad record")
					}
					return e, nil
				}).WithErrorHandler(ErrorHandler{OnExhausted: "dlq"}).WithDLQSink(dlq).AddSink(main)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := env.Execute(ctx); err != nil {
					t.Fatal(err)
				}
				if len(main.Events()) != 1 || dlq.opens != 1 || dlq.closes != 1 {
					t.Fatalf("main=%d open=%d close=%d", len(main.Events()), dlq.opens, dlq.closes)
				}
				want := 1
				if failOpen {
					want = 0
				}
				if len(dlq.Events()) != want {
					t.Fatalf("DLQ=%d want=%d", len(dlq.Events()), want)
				}
			})
		}
	}
}
