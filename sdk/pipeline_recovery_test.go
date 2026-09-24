package sdk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type yamlReplaySource struct {
	maxRestore uint64
	offset     uint64
	committed  <-chan struct{}
	restores   *atomic.Int32
}

func (*yamlReplaySource) Open(context.Context) error { return nil }
func (*yamlReplaySource) Close() error               { return nil }
func (*yamlReplaySource) GenerateWatermark() int64   { return 0 }
func (s *yamlReplaySource) Checkpoint(uint64) ([]byte, error) {
	return binary.BigEndian.AppendUint64(nil, s.offset), nil
}
func (s *yamlReplaySource) RestoreOffset(_ context.Context, state []byte) error {
	if len(state) != 8 || binary.BigEndian.Uint64(state) < 1 || binary.BigEndian.Uint64(state) > s.maxRestore {
		return fmt.Errorf("invalid completed offset %x", state)
	}
	s.offset = binary.BigEndian.Uint64(state)
	s.restores.Add(1)
	return nil
}
func (s *yamlReplaySource) ReadBatch(ctx context.Context) ([]Event, error) {
	if s.offset == 1 {
		select {
		case <-s.committed:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Millisecond):
			return []Event{}, nil
		}
	}
	if s.offset == 2 {
		return nil, nil
	}
	s.offset++
	return []Event{{Key: []byte("k"), Value: []byte(fmt.Sprint(s.offset))}}, nil
}

type yamlRecoverySink struct {
	failWhen func(Event) bool
	*pauseTransactionSink
	committed chan struct{}
	once      *sync.Once
	failed    *atomic.Bool
}

func (s *yamlRecoverySink) Write(ctx context.Context, event Event) error {
	if err := s.pauseTransactionSink.Write(ctx, event); err != nil {
		return err
	}
	if s.failWhen(event) && s.failed.CompareAndSwap(false, true) {
		return errors.New("injected failure after staging second record")
	}
	return nil
}
func (s *yamlRecoverySink) Commit(ctx context.Context, id uint64) error {
	if err := s.pauseTransactionSink.Commit(ctx, id); err != nil {
		return err
	}
	s.once.Do(func() { close(s.committed) })
	return nil
}

func TestYAMLPeriodicCheckpointRecoversTransactionalOutput(t *testing.T) {
	for _, mode := range []string{"mapped", "hashmap-window", "pebble-window"} {
		t.Run(mode, func(t *testing.T) { testYAMLTransactionalRecovery(t, mode) })
	}
}
func testYAMLTransactionalRecovery(t *testing.T, mode string) {
	committed := make(chan struct{})
	var once sync.Once
	var failed atomic.Bool
	var sources, sinks, restores atomic.Int32
	ledger := &pauseTransactionLedger{prepared: map[uint64][]string{}, committed: map[uint64]bool{}}
	observed := &collectSink{}
	data := `apiVersion: wire/v1
kind: Pipeline
metadata: {name: yaml-recovery}
spec:
  checkpoint: {interval: 50ms, timeout: 5s}
  restart: {strategy: fixed-delay, max-attempts: 3, delay: 10ms}
  sources:
    - {name: input, type: replay}
  transforms:
    - name: mapped
      type: map
      input: input
      config: {expression: "value + 10"}
  sinks:
    - {name: output, type: transactional, input: mapped}
`
	maxRestore := uint64(1)
	failWhen := func(event Event) bool { return string(event.Value) == "12" }
	if mode != "mapped" {
		maxRestore = 2
		backend := strings.TrimSuffix(mode, "-window")
		data = strings.Replace(data, "spec:\n", "spec:\n  state_backend: {type: "+backend+"}\n", 1)
		data = strings.Replace(data, "type: map", "type: tumbling-window", 1)
		data = strings.Replace(data, `config: {expression: "value + 10"}`, "config: {size: 10ms, aggregation: count}", 1)
		failWhen = func(event Event) bool {
			var value struct {
				Count uint64 `json:"count"`
			}
			return json.Unmarshal(event.Value, &value) == nil && value.Count == 2
		}
	}
	pipeline, err := ParsePipelineYAML([]byte(data), PipelineConnectors{
		SourceInstances: map[string]func(map[string]any, InstanceContext) (Source, error){"replay": func(map[string]any, InstanceContext) (Source, error) {
			sources.Add(1)
			return &yamlReplaySource{maxRestore: maxRestore, committed: committed, restores: &restores}, nil
		}},
		SinkInstances: map[string]func(map[string]any, InstanceContext) (Sink, error){"transactional": func(map[string]any, InstanceContext) (Sink, error) {
			sinks.Add(1)
			return &yamlRecoverySink{failWhen: failWhen, pauseTransactionSink: &pauseTransactionSink{ledger: ledger, observed: observed}, committed: committed, once: &once, failed: &failed}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := pipeline.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if !failed.Load() || restores.Load() != 1 || sources.Load() != 2 || sinks.Load() != 2 {
		t.Fatalf("failure=%t restores=%d sources=%d sinks=%d", failed.Load(), restores.Load(), sources.Load(), sinks.Load())
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if mode == "mapped" {
		if fmt.Sprint(ledger.visible) != "[11 12]" {
			t.Fatalf("duplicated/lost committed output: %v", ledger.visible)
		}
	} else {
		if len(ledger.visible) != 1 || !failWhen(Event{Value: []byte(ledger.visible[0])}) {
			t.Fatalf("window state lost or duplicated: %v", ledger.visible)
		}
	}
	if len(ledger.committed) < 2 {
		t.Fatalf("periodic and final commits missing: %v", ledger.committed)
	}
}
