package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type batchProbe struct {
	sizes     []int
	values    []byte
	singles   int
	fail      error
	snapshots []int
}

func (*batchProbe) Open(context.Context) error { return nil }
func (*batchProbe) Close() error               { return nil }
func (s *batchProbe) Checkpoint(uint64) ([]byte, error) {
	s.snapshots = append(s.snapshots, len(s.values))
	return nil, nil
}
func (s *batchProbe) Write(_ context.Context, e Event) error {
	s.singles++
	s.values = append(s.values, e.Value...)
	return nil
}
func (s *batchProbe) WriteBatch(_ context.Context, events []Event) error {
	s.sizes = append(s.sizes, len(events))
	if s.fail != nil {
		return s.fail
	}
	for _, e := range events {
		s.values = append(s.values, e.Value...)
	}
	return nil
}
func TestSinkBatchBoundsAndPartialFlush(t *testing.T) {
	sink := &batchProbe{}
	input := make(chan Event, 205)
	for i := range 205 {
		input <- Event{Value: []byte{byte(i)}}
	}
	close(input)
	err := runOperatorChain(t.Context(), []Operator{sink}, input, make(chan ControlMsg), make(chan OutputMsg, 2), NewBarrierAligner(1, 1000), 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sink.sizes, []int{100, 100, 5}) || sink.singles != 0 || len(sink.values) != 205 {
		t.Fatalf("sizes=%v singles=%d values=%d", sink.sizes, sink.singles, len(sink.values))
	}
	for i, v := range sink.values {
		if v != byte(i) {
			t.Fatalf("record %d=%d", i, v)
		}
	}
}
func TestSinkBatchBoundaryFlushAndFailure(t *testing.T) {
	for _, boundary := range []string{"checkpoint", "watermark", "end", "shutdown"} {
		for _, fail := range []bool{false, true} {
			t.Run(boundary+map[bool]string{false: "/success", true: "/failure"}[fail], func(t *testing.T) {
				sentinel := errors.New("delivery failed")
				sink := &batchProbe{}
				if fail {
					sink.fail = sentinel
				}
				links := buildChainLinks([]Operator{sink}, nil)
				configureSinkBatches(links)
				output := make(chan OutputMsg, 4)
				aligner := NewBarrierAligner(1, 100)
				cc := &chainContext{ctx: t.Context(), links: links, inputCh: make(chan Event), outputCh: output, aligner: aligner, numInputs: 1, cpMetrics: NoopCheckpointMetrics(), errMetrics: NoopErrorMetrics(), log: testLogger()}
				payload := []byte{42}
				if err := processEvent(cc, Event{Value: payload}); err != nil {
					t.Fatal(err)
				}
				payload[0] = 99 // Buffered payload must be owned by the chain.
				var err error
				eof := 0
				switch boundary {
				case "checkpoint":
					aligner.OnBarrier(0, 1, 1)
					err = handleControl(cc, ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 1, EpochID: 1}, &eof)
				case "watermark":
					err = processWatermark(cc, 10)
				case "end":
					err = handleControl(cc, ControlMsg{Type: CtrlEndOfPartition}, &eof)
				case "shutdown":
					err = handleControl(cc, ControlMsg{Type: CtrlShutdown}, &eof)
				}
				if fail {
					if !errors.Is(err, sentinel) {
						t.Fatalf("error=%v", err)
					}
					if len(output) != 0 || len(sink.snapshots) != 0 {
						t.Fatal("failed batch crossed boundary")
					}
					return
				}
				if err != nil && err != errChainDone {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(sink.values, []byte{42}) {
					t.Fatalf("values=%v", sink.values)
				}
				if boundary == "checkpoint" && !reflect.DeepEqual(sink.snapshots, []int{1}) {
					t.Fatalf("snapshots=%v", sink.snapshots)
				}
			})
		}
	}
}
func TestSinkBatchRecordPolicyKeepsPerRecordWrites(t *testing.T) {
	sink := &batchProbe{}
	links := buildChainLinks([]Operator{sink}, []ErrorHandlerConfig{{MaxRetries: 1}})
	configureSinkBatches(links)
	cc := &chainContext{ctx: t.Context(), links: links, errMetrics: NoopErrorMetrics()}
	if err := processEvent(cc, Event{Value: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	if sink.singles != 1 || len(sink.sizes) != 0 {
		t.Fatalf("single=%d batches=%v", sink.singles, sink.sizes)
	}
}

type notifyingBatchSink struct {
	batchProbe
	delivered chan struct{}
}

func (s *notifyingBatchSink) WriteBatch(ctx context.Context, events []Event) error {
	if err := s.batchProbe.WriteBatch(ctx, events); err != nil {
		return err
	}
	s.delivered <- struct{}{}
	return nil
}
func TestSinkBatchSparseInputDoesNotWaitForMoreRecords(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := &notifyingBatchSink{delivered: make(chan struct{}, 1)}
	input := make(chan Event)
	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, []Operator{sink}, input, make(chan ControlMsg), make(chan OutputMsg, 2), NewBarrierAligner(1, 100), 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	}()
	select {
	case input <- Event{Value: []byte{1}}:
	case <-t.Context().Done():
		t.Fatal("input blocked")
	}
	select {
	case <-sink.delivered:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("partial batch never flushed while input idle")
	}
	close(input)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type transactionalBatchProbe struct {
	batchProbe
	prepared int
}

func (*transactionalBatchProbe) BeginTransaction(context.Context) error { return nil }
func (s *transactionalBatchProbe) PreCommit(context.Context, uint64) error {
	s.prepared = len(s.values)
	return nil
}
func (*transactionalBatchProbe) Commit(context.Context, uint64) error { return nil }
func (*transactionalBatchProbe) Abort(context.Context) error          { return nil }
func TestSinkBatchFlushesBeforeTransactionPrepare(t *testing.T) {
	sink := &transactionalBatchProbe{}
	links := buildChainLinks([]Operator{sink}, nil)
	configureSinkBatches(links)
	aligner := NewBarrierAligner(1, 100)
	cc := &chainContext{ctx: t.Context(), links: links, inputCh: make(chan Event), outputCh: make(chan OutputMsg, 4), aligner: aligner, numInputs: 1, cpMetrics: NoopCheckpointMetrics(), errMetrics: NoopErrorMetrics(), log: testLogger(), txnSink: sink}
	if err := processEvent(cc, Event{Value: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	aligner.OnBarrier(0, 1, 1)
	eof := 0
	if err := handleControl(cc, ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 1, EpochID: 1}, &eof); err != nil {
		t.Fatal(err)
	}
	if sink.prepared != 1 || !cc.transactionPrepared || !reflect.DeepEqual(sink.snapshots, []int{1}) {
		t.Fatalf("prepared=%d snapshots=%v state=%v", sink.prepared, sink.snapshots, cc.transactionPrepared)
	}
}
