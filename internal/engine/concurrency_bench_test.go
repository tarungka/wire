package engine

import (
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

// BenchmarkDeserializationPlacement compares the same bounded two-goroutine
// handoff with decoding on either side. It isolates placement, not network I/O.
func BenchmarkDeserializationPlacement(b *testing.B) {
	raw, err := protocol.EncodeMsgPack(&protocol.DataRecordMsg{Key: []byte("key"), Value: make([]byte, 1024), EventTime: 1})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("reader", func(b *testing.B) {
		events := make(chan Event, 1024)
		done := make(chan struct{})
		b.ReportAllocs()
		b.ResetTimer()
		go func() {
			defer close(done)
			defer close(events)
			for i := 0; i < b.N; i++ {
				var message protocol.DataRecordMsg
				if err := protocol.DecodeMsgPack(raw, &message); err != nil {
					b.Error(err)
					return
				}
				events <- EventFromProto(&message)
			}
		}()
		count := 0
		for event := range events {
			if len(event.Value) != 1024 {
				b.Error("bad decoded payload")
			}
			count++
		}
		<-done
		if count != b.N {
			b.Fatalf("processed %d of %d", count, b.N)
		}
	})
	b.Run("chain", func(b *testing.B) {
		frames := make(chan []byte, 1024)
		done := make(chan struct{})
		b.ReportAllocs()
		b.ResetTimer()
		go func() {
			defer close(done)
			defer close(frames)
			for i := 0; i < b.N; i++ {
				frames <- raw
			}
		}()
		for frame := range frames {
			var message protocol.DataRecordMsg
			if err := protocol.DecodeMsgPack(frame, &message); err != nil {
				b.Error(err)
			}
			if len(message.Value) != 1024 {
				b.Error("bad decoded payload")
			}
		}
		<-done
	})
}

func BenchmarkEventChannel(b *testing.B) {
	events := make(chan Event, 1024)
	done := make(chan struct{})
	event := Event{Value: make([]byte, 1024)}
	b.ReportAllocs()
	b.ResetTimer()
	go func() {
		defer close(done)
		defer close(events)
		for i := 0; i < b.N; i++ {
			events <- event
		}
	}()
	for range events {
	}
	<-done
}
