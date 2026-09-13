package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestMuxObservesTaskRejectionWithoutCallerRead(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "tcp"
		if secure {
			name = "mutual_tls"
		}
		t.Run(name, func(t *testing.T) {
			_, client, addr := newTestMuxPairSecure(t, secure)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			output, err := client.Dial(ctx, addr, protocol.StreamHeaderMsg{SourceTaskID: "source", TargetTaskID: "missing"})
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			select {
			case <-output.done:
			case <-ctx.Done():
				t.Fatal("sender did not observe rejection")
			}
			if err := output.WriteMessage(&protocol.DataRecordMsg{Value: []byte("lost")}); !errors.Is(err, ErrTargetTaskRejected) {
				t.Fatalf("write: %v", err)
			}
		})
	}
}
