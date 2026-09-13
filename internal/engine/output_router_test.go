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
