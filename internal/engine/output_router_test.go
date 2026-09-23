package engine

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/transport"
)

func TestOutputRouterBroadcastsOrderedControlFrames(t *testing.T) {
	a, ar := newTestStreamPair(t)
	b, br := newTestStreamPair(t)
	defer a.Close()
	defer ar.Close()
	defer b.Close()
	defer br.Close()
	messages := make(chan OutputMsg, 8)
	for i := 0; i < 2; i++ {
		messages <- OutputMsg{Type: OutputData, Event: Event{Value: []byte(fmt.Sprint(i))}}
	}
	messages <- OutputMsg{Type: OutputBarrier, Barrier: &protocol.CheckpointBarrierMsg{CheckpointID: 7, EpochID: 1}}
	messages <- OutputMsg{Type: OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: 10, SourceID: "source"}}
	for i := 2; i < 4; i++ {
		messages <- OutputMsg{Type: OutputData, Event: Event{Value: []byte(fmt.Sprint(i))}}
	}
	messages <- OutputMsg{Type: OutputEnd, End: &protocol.EndOfPartitionMsg{SourceID: "source"}}
	close(messages)
	// TaskSlot preserves a separate output context when the source finishes,
	// so closed producer queues drain even when input readers are stopping.
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { done <- runOutputRouter(ctx, []*transport.FrameStream{a, b}, messages, testLogger()) }()
	for index, input := range []*transport.FrameStream{ar, br} {
		received := make(chan []string, 1)
		failed := make(chan error, 1)
		go func() {
			var sequence []string
			for {
				msg, err := input.ReadMessage()
				if err == io.EOF {
					received <- sequence
					return
				}
				if err != nil {
					failed <- err
					return
				}
				switch m := msg.(type) {
				case *protocol.DataRecordMsg:
					sequence = append(sequence, string(m.Value))
				case *protocol.CheckpointBarrierMsg:
					sequence = append(sequence, fmt.Sprintf("barrier:%d", m.CheckpointID))
				case *protocol.WatermarkMsg:
					sequence = append(sequence, fmt.Sprintf("watermark:%d", m.Timestamp))
				case *protocol.EndOfPartitionMsg:
					sequence = append(sequence, "end")
				}
			}
		}()
		want := []string{fmt.Sprint(index), "barrier:7", "watermark:10", fmt.Sprint(index + 2), "end"}
		select {
		case got := <-received:
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("partition %d: %v, want %v", index, got, want)
			}
		case err := <-failed:
			t.Fatal(err)
		case <-time.After(time.Second):
			t.Fatalf("partition %d did not terminate", index)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("router did not drain")
	}
}

func TestOutputWritersProgressIndependentlyAndJoinOnCancel(t *testing.T) {
	a, ar := newTestStreamPair(t)
	b, br := newTestStreamPair(t)
	defer a.Close()
	defer ar.Close()
	defer b.Close()
	defer br.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	messages := make(chan OutputMsg, 2)
	messages <- OutputMsg{Type: OutputData, Event: Event{Value: make([]byte, 2*1024*1024)}}
	messages <- OutputMsg{Type: OutputData, Event: Event{Value: []byte("other partition")}}
	done := make(chan error, 1)
	go func() { done <- runOutputRouter(ctx, []*transport.FrameStream{a, b}, messages, testLogger()) }()
	received := make(chan error, 1)
	go func() {
		message, err := br.ReadMessage()
		if err == nil {
			record, ok := message.(*protocol.DataRecordMsg)
			if !ok || string(record.Value) != "other partition" {
				err = fmt.Errorf("unexpected record: %T", message)
			}
		}
		received <- err
	}()
	select {
	case err := <-received:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("one blocked stream prevented another writer from progressing")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled blocked write reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("writers did not join after cancellation")
	}
}

func TestGroupedRouterSeparatesLateDataAndFencesBothOutputs(t *testing.T) {
	main, mainRead := newTestStreamPair(t)
	late, lateRead := newTestStreamPair(t)
	defer main.Close()
	defer mainRead.Close()
	defer late.Close()
	defer lateRead.Close()
	messages := make(chan OutputMsg, 6)
	messages <- OutputMsg{Type: OutputData, Event: Event{Value: []byte("main")}}
	messages <- OutputMsg{Type: OutputData, SideOutput: "late", Event: Event{Value: []byte("late")}}
	messages <- OutputMsg{Type: OutputBarrier, Barrier: &protocol.CheckpointBarrierMsg{CheckpointID: 3, EpochID: 1}}
	messages <- OutputMsg{Type: OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: 15}}
	messages <- OutputMsg{Type: OutputData, SideOutput: "late", Event: Event{Value: []byte("later")}}
	messages <- OutputMsg{Type: OutputEnd, End: &protocol.EndOfPartitionMsg{}}
	close(messages)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for i, reader := range []*transport.FrameStream{mainRead, lateRead} {
		go func() {
			var got []string
			for {
				msg, err := reader.ReadMessage()
				if err != nil {
					results <- err
					return
				}
				switch m := msg.(type) {
				case *protocol.DataRecordMsg:
					got = append(got, string(m.Value))
				case *protocol.CheckpointBarrierMsg:
					got = append(got, "barrier")
				case *protocol.WatermarkMsg:
					got = append(got, "watermark")
				case *protocol.EndOfPartitionMsg:
					want := []string{"main", "barrier", "watermark"}
					if i == 1 {
						want = []string{"late", "barrier", "watermark", "later"}
					}
					if !reflect.DeepEqual(got, want) {
						results <- fmt.Errorf("output %d: %v != %v", i, got, want)
					} else {
						results <- nil
					}
					return
				}
			}
		}()
	}
	if err := runGroupedOutputRouter(ctx, []*transport.FrameStream{main, late}, messages, testLogger(), []OutputGroup{{Streams: []int{0}}, {SideOutput: "late", Streams: []int{1}}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestGroupedRouterBroadcastsRecordsBeforeFences(t *testing.T) {
	first, firstRead := newTestStreamPair(t)
	second, secondRead := newTestStreamPair(t)
	defer first.Close()
	defer firstRead.Close()
	defer second.Close()
	defer secondRead.Close()
	messages := make(chan OutputMsg, 3)
	messages <- OutputMsg{Type: OutputData, Event: Event{Value: []byte("record")}}
	messages <- OutputMsg{Type: OutputBarrier, Barrier: &protocol.CheckpointBarrierMsg{CheckpointID: 7, EpochID: 1}}
	messages <- OutputMsg{Type: OutputEnd, End: &protocol.EndOfPartitionMsg{}}
	close(messages)
	results := make(chan error, 2)
	for _, reader := range []*transport.FrameStream{firstRead, secondRead} {
		go func() {
			for index := 0; index < 3; index++ {
				message, err := reader.ReadMessage()
				if err != nil {
					results <- err
					return
				}
				switch index {
				case 0:
					record, ok := message.(*protocol.DataRecordMsg)
					if !ok || string(record.Value) != "record" {
						results <- fmt.Errorf("missing broadcast record: %T", message)
						return
					}
				case 1:
					barrier, ok := message.(*protocol.CheckpointBarrierMsg)
					if !ok || barrier.CheckpointID != 7 {
						results <- fmt.Errorf("barrier overtook data: %T", message)
						return
					}
				case 2:
					if _, ok := message.(*protocol.EndOfPartitionMsg); !ok {
						results <- fmt.Errorf("missing end: %T", message)
						return
					}
				}
			}
			results <- nil
		}()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := runGroupedOutputRouter(ctx, []*transport.FrameStream{first, second}, messages, testLogger(), []OutputGroup{{Streams: []int{0, 1}, Broadcast: true}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
