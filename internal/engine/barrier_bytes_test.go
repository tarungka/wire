package engine

import (
	"context"
	"testing"
)

func TestAlignmentBytesReleased(t *testing.T) {
	for _, operation := range []string{"finish", "drain", "reset", "shutdown"} {
		t.Run(operation, func(t *testing.T) {
			a := NewBarrierAligner(2, 2)
			a.OnBarrier(0, 1, 1)
			event := Event{Key: []byte("key"), Value: []byte("value"), Headers: map[string][]byte{"h": []byte("vv")}}
			if err := a.BufferEvent(context.Background(), 0, event); err != nil {
				t.Fatal(err)
			}
			if ok, err := a.BufferAlignedEvent(context.Background(), 0, event); !ok || err != nil {
				t.Fatalf("buffer: %v %v", ok, err)
			}
			if got := a.BufferedBytes(); got != 38 {
				t.Fatalf("bytes=%d want 38", got)
			}
			if err := a.BufferEvent(context.Background(), 0, event); err == nil {
				t.Fatal("full buffer accepted")
			}
			if got := a.BufferedBytes(); got != 38 {
				t.Fatalf("rejected record counted: %d", got)
			}
			backing := a.sideBuffers[0]
			var released []Event
			switch operation {
			case "finish":
				released = a.FinishAlignment(1)
			case "drain":
				released = a.DrainAll(1)
			case "reset":
				a.Reset(1)
			case "shutdown":
				released = a.BeginDrain()
			}
			if got := a.BufferedBytes(); got != 0 {
				t.Fatalf("released bytes=%d", got)
			}
			if operation != "reset" && (len(released) != 2 || string(released[0].Value) != "value") {
				t.Fatalf("released events corrupted: %v", released)
			}
			if operation != "shutdown" {
				for _, retained := range backing {
					if retained.Key != nil || retained.Value != nil || retained.Headers != nil {
						t.Fatal("empty buffer retains payload")
					}
				}
			}
		})
	}
}
