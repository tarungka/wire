package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type beforeCheckpointSource struct {
	first, emitted bool
	observed       <-chan struct{}
}

func (*beforeCheckpointSource) Open(context.Context) error { return nil }
func (*beforeCheckpointSource) Close() error               { return nil }
func (*beforeCheckpointSource) GenerateWatermark() int64   { return 0 }
func (s *beforeCheckpointSource) ReadBatch(ctx context.Context) ([]Event, error) {
	if !s.emitted {
		s.emitted = true
		return []Event{{Key: []byte("key"), Value: []byte("record")}}, nil
	}
	if !s.first {
		return nil, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.observed:
		return nil, errors.New("injected failure before first checkpoint")
	}
}

type beforeCheckpointSink struct {
	collectSink
	observed chan<- struct{}
}

func (s *beforeCheckpointSink) Write(ctx context.Context, e Event) error {
	if err := s.collectSink.Write(ctx, e); err != nil {
		return err
	}
	select {
	case s.observed <- struct{}{}:
	default:
	}
	return nil
}
func TestMiniClusterRetryBeforeCheckpointStartsWithEmptyDiskState(t *testing.T) {
	cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
	defer cluster.Shutdown()
	env := cluster.GetExecutionEnvironment().SetStateBackend(NewPebbleStateBackend(t.TempDir())).SetRestartStrategy(FixedDelay(1, 0))
	observed := make(chan struct{}, 1)
	var attempts atomic.Int32
	sink := &beforeCheckpointSink{observed: observed}
	env.AddSourceFactory("source", func(InstanceContext) (Source, error) {
		return &beforeCheckpointSource{first: attempts.Add(1) == 1, observed: observed}, nil
	}).KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).Process(func(c ProcessContext, e Event) ([]Event, error) {
		state := c.GetState("count")
		n, err := state.ValueInt64()
		if err != nil {
			return nil, err
		}
		if err := state.SetInt64(n + 1); err != nil {
			return nil, err
		}
		e.Value = []byte(fmt.Sprint(n + 1))
		return []Event{e}, nil
	}).AddSink(sink)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err := env.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if len(events) != 2 || string(events[0].Value) != "1" || string(events[1].Value) != "1" {
		t.Fatalf("replacement retained failed attempt state: %+v", events)
	}
}

func TestStateBackendOnlyConfiguresManagedOperators(t *testing.T) {
	env := New().SetStateBackend(NewHashMapStateBackend(3))
	graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{{Type: rpc.OperatorTypeSource}, {Type: rpc.OperatorTypeMap}, {Type: rpc.OperatorTypeProcess}, {Type: rpc.OperatorTypeWindow}, {Type: rpc.OperatorTypeSink}}}
	env.configureGraphStateBackend(&graph)
	for _, op := range graph.Operators {
		managed := op.Type == rpc.OperatorTypeProcess || op.Type == rpc.OperatorTypeWindow
		if (op.StateBackend != nil) != managed {
			t.Fatalf("wrong configured operator: %+v", op)
		}
		if managed && (op.StateBackend.Type != "hashmap" || op.StateBackend.MaxMemoryBytes != 3*1024*1024) {
			t.Fatalf("wrong backend: %+v", op.StateBackend)
		}
	}
}

func TestMiniClusterEnforcesDeployedStateMemoryLimit(t *testing.T) {
	cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
	defer cluster.Shutdown()
	env := cluster.GetExecutionEnvironment().SetStateBackend(NewHashMapStateBackend(1))
	env.AddSource(&sliceSource{events: []Event{{Key: []byte("k"), Value: []byte("input")}}}).KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).Process(func(c ProcessContext, e Event) ([]Event, error) {
		c.GetState("large").Set(make([]byte, 2*1024*1024))
		return []Event{e}, nil
	}).AddSink(&collectSink{})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := env.Execute(ctx); !errors.Is(err, engine.ErrMemoryLimitExceeded) {
		t.Fatalf("worker did not enforce the configured one-MiB state limit: %v", err)
	}
}

func TestClusterSubmissionCarriesStateBackend(t *testing.T) {
	graphs := make(chan rpc.JobGraph, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var request submitJobRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			raw, err := base64.StdEncoding.DecodeString(request.GraphBytes)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var graph rpc.JobGraph
			if err := protocol.DecodeMsgPack(raw, &graph); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			graphs <- graph
		}
		_, _ = w.Write([]byte(`{"id":"job","status":"FINISHED"}`))
	}))
	defer server.Close()
	env := New().SetMode(Cluster).SetCoordinator(server.URL).SetStateBackend(StateBackendConfig{Type: "pebble", DataDir: "worker-state", MaxCompactionConcurrency: 3})
	env.AddSourceNamed("source", "registered-source", nil).KeyByNamed("key", "registered-key", nil).ProcessNamed("process", "registered-process", nil).AddSinkNamed("sink", "registered-sink", nil)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	graph := <-graphs
	for _, op := range graph.Operators {
		if op.Type == rpc.OperatorTypeProcess {
			if op.StateBackend == nil || op.StateBackend.Type != "pebble" || op.StateBackend.DataDir != "worker-state" || op.StateBackend.MaxCompactionConcurrency != 3 {
				t.Fatalf("lost backend selection: %+v", op.StateBackend)
			}
		} else if op.StateBackend != nil {
			t.Fatal("configured backend on stateless operator")
		}
	}
}
