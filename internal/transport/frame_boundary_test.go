package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestTransportFrameBoundaries(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := "tcp"
		if secure {
			name = "mutual_tls"
		}
		t.Run(name, func(t *testing.T) {
			t.Run("maximum", func(t *testing.T) {
				server, client, addr := newTestMuxPairSecure(t, secure)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				output, err := client.Dial(ctx, addr)
				if err != nil {
					t.Fatal(err)
				}
				defer output.Close()
				input, err := server.Accept(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				record := &protocol.DataRecordMsg{Value: make([]byte, 65536)}
				payload, err := protocol.EncodeMsgPack(record)
				if err != nil {
					t.Fatal(err)
				}
				overhead := len(payload) - len(record.Value)
				record.Value = make([]byte, int(protocol.DefaultMaxFrameSize)-protocol.MinFrameLength-overhead)
				for i := range record.Value {
					record.Value[i] = byte(i % 251)
				}
				result := make(chan error, 1)
				go func() {
					message, err := input.ReadMessage()
					if err == nil {
						decoded, ok := message.(*protocol.DataRecordMsg)
						if !ok || !bytes.Equal(decoded.Value, record.Value) {
							err = fmt.Errorf("maximum frame changed")
						}
					}
					result <- err
				}()
				if err := output.WriteMessageContext(ctx, record); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-result:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			})
			for _, length := range []uint32{0, 4, protocol.DefaultMaxFrameSize + 1} {
				t.Run(fmt.Sprint("invalid-", length), func(t *testing.T) {
					server, client, addr := newTestMuxPairSecure(t, secure)
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					output, err := client.Dial(ctx, addr)
					if err != nil {
						t.Fatal(err)
					}
					defer output.Close()
					input, err := server.Accept(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer input.Close()
					var prefix [4]byte
					binary.BigEndian.PutUint32(prefix[:], length)
					if _, err := output.raw.Write(prefix[:]); err != nil {
						t.Fatal(err)
					}
					result := make(chan error, 1)
					go func() { _, err := input.ReadMessage(); result <- err }()
					want := protocol.ErrFrameTooSmall
					if length > protocol.DefaultMaxFrameSize {
						want = protocol.ErrFrameTooLarge
					}
					select {
					case err := <-result:
						if !errors.Is(err, want) {
							t.Fatalf("got %v, want %v", err, want)
						}
					case <-ctx.Done():
						t.Fatal("reader waited for an invalid frame body")
					}
				})
			}
		})
	}
}
