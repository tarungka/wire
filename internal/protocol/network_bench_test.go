package protocol

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/go-msgpack/v2/codec"
)

// BenchmarkTCPFramingThroughput includes encoding, TCP delivery and decoding of
// the same 1 KiB record. Both readers use the same buffering; neither sender
// batches records. Setup is excluded, but draining the receiver is timed.
func BenchmarkTCPFramingThroughput(b *testing.B) {
	for _, framed := range []bool{false, true} {
		name := "raw_msgpack"
		if framed {
			name = "wire_bounded"
		}
		b.Run(name, func(b *testing.B) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				b.Fatal(err)
			}
			defer listener.Close()
			sender, err := net.Dial("tcp", listener.Addr().String())
			if err != nil {
				b.Fatal(err)
			}
			defer sender.Close()
			receiver, err := listener.Accept()
			if err != nil {
				b.Fatal(err)
			}
			defer receiver.Close()
			deadline := time.Now().Add(time.Minute)
			if err := sender.SetDeadline(deadline); err != nil {
				b.Fatal(err)
			}
			if err := receiver.SetDeadline(deadline); err != nil {
				b.Fatal(err)
			}
			reader := bufio.NewReader(receiver)
			decoder := codec.NewDecoder(reader, &msgpackHandle)
			msg := &DataRecordMsg{Key: []byte("benchmark-key"), Value: make([]byte, 1024), EventTime: 1708819200000}
			result := make(chan error, 1)
			b.SetBytes(1024)
			b.ReportAllocs()
			b.ResetTimer()
			go func() {
				var readErr error
				defer func() {
					if readErr != nil {
						_ = receiver.Close()
					}
					result <- readErr
				}()
				for i := 0; i < b.N; i++ {
					var record *DataRecordMsg
					if framed {
						var frame Frame
						frame, readErr = ReadFrame(reader, DefaultMaxFrameSize)
						if readErr != nil {
							return
						}
						var decoded any
						decoded, readErr = DecodePayload(frame)
						if readErr != nil {
							return
						}
						record = decoded.(*DataRecordMsg)
					} else {
						record = new(DataRecordMsg)
						readErr = decoder.Decode(record)
						if readErr != nil {
							return
						}
					}
					if !bytes.Equal(record.Value, msg.Value) || record.EventTime != msg.EventTime || string(record.Key) != string(msg.Key) {
						readErr = fmt.Errorf("record %d changed during delivery", i)
						return
					}
				}
			}()
			for i := 0; i < b.N; i++ {
				if framed {
					err = EncodeAndWriteFrame(sender, msg)
				} else {
					var payload []byte
					payload, err = EncodeMsgPack(msg)
					if err == nil {
						_, err = sender.Write(payload)
					}
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			if err := <-result; err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
		})
	}
}
