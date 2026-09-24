package engine

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/transport"
)

func TestKeyedOutputRoutingPreservesOwnershipAndControlOrder(t *testing.T) {
	const partitions = 3
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	streams := make([]*transport.FrameStream, partitions)
	readers := make([]*transport.FrameStream, partitions)
	for i := range streams {
		streams[i], readers[i] = newTestStreamPair(t)
		t.Cleanup(func() { _ = streams[i].Close(); _ = readers[i].Close() })
	}
	messages := make(chan OutputMsg, 300)
	expected := make([][]string, partitions)
	for round := 0; round < 2; round++ {
		for i := 0; i < 128; i++ {
			key := []byte(fmt.Sprintf("key-%d", i))
			if i == 0 {
				key = nil
			}
			value := fmt.Sprintf("%d:%d", round, i)
			group := keygroup.KeyGroup(key, 128)
			// Independent floor-range membership oracle, not AssignedTask.
			owner := -1
			for p := 0; p < partitions; p++ {
				if int(group) >= p*128/partitions && int(group) < (p+1)*128/partitions {
					owner = p
				}
			}
			if owner < 0 {
				t.Fatal("missing owner")
			}
			expected[owner] = append(expected[owner], value)
			messages <- OutputMsg{Type: OutputData, Event: Event{Key: key, Value: []byte(value)}}
		}
		messages <- OutputMsg{Type: OutputBarrier, Barrier: &protocol.CheckpointBarrierMsg{CheckpointID: uint64(round + 1), EpochID: 1}}
		for p := range expected {
			expected[p] = append(expected[p], fmt.Sprintf("barrier:%d", round+1))
		}
	}
	messages <- OutputMsg{Type: OutputEnd, End: &protocol.EndOfPartitionMsg{SourceID: "source"}}
	close(messages)
	results := make(chan error, partitions)
	for p, reader := range readers {
		go func() {
			var got []string
			for {
				message, err := reader.ReadMessage()
				if err != nil {
					results <- err
					return
				}
				switch message := message.(type) {
				case *protocol.DataRecordMsg:
					got = append(got, string(message.Value))
				case *protocol.CheckpointBarrierMsg:
					got = append(got, fmt.Sprintf("barrier:%d", message.CheckpointID))
				case *protocol.EndOfPartitionMsg:
					if !reflect.DeepEqual(got, expected[p]) {
						results <- fmt.Errorf("partition %d: got %v want %v", p, got, expected[p])
						return
					}
					results <- nil
					return
				}
			}
		}()
	}
	done := make(chan error, 1)
	go func() { done <- runOutputRouter(ctx, streams, messages, testLogger(), 128) }()
	for range partitions {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
