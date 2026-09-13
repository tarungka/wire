package protocol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"
)

// Benchmark-only copy of ReadFrame: the sole variable is checksum computation.
// Production always verifies. TestCRCComparisonMatchesProduction guards this
// reference against drift in parsing, bounds, errors and buffer ownership.
func readFrameCRCComparison(r io.Reader, maxFrameSize uint32, verify bool) (Frame, error) {
	// 1. Read length field (4 bytes, big-endian).
	var lenBuf [LengthFieldSize]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return Frame{}, err
	}
	frameLen := binary.BigEndian.Uint32(lenBuf[:])

	// 2. Validate length.
	if frameLen < MinFrameLength {
		return Frame{Length: frameLen}, ErrFrameTooSmall
	}
	if frameLen > maxFrameSize {
		return Frame{Length: frameLen}, ErrFrameTooLarge
	}

	// 3. Read the frame body using a pooled buffer to reduce GC pressure.
	bufp := framePool.Get().(*[]byte)
	buf := *bufp
	if cap(buf) < int(frameLen) {
		buf = make([]byte, frameLen)
	} else {
		buf = buf[:frameLen]
	}

	if _, err := io.ReadFull(r, buf); err != nil {
		// Return buffer to pool on error.
		if cap(buf) <= 1024*1024 {
			*bufp = buf
			framePool.Put(bufp)
		}
		return Frame{}, err
	}

	// 4. Extract fields.
	msgType := buf[0]
	crcReceived := binary.BigEndian.Uint32(buf[1:5])

	// Verify before allocating the decoded payload. Corrupt frames must not
	// cause a second allocation of the sender-controlled frame length.
	crcComputed := crcReceived
	if verify {
		crcComputed = computeCRC32C(msgType, buf[5:])
	}
	if crcReceived != crcComputed {
		diagnostic := Frame{Length: frameLen, MsgType: msgType, ReceivedCRC: crcReceived, ComputedCRC: crcComputed, CorruptPrefix: append([]byte(nil), buf[:min(len(buf), 64)]...)}
		if cap(buf) <= 1024*1024 {
			*bufp = buf
			framePool.Put(bufp)
		}
		return diagnostic, ErrCRCMismatch
	}
	payload := append([]byte(nil), buf[5:]...)
	if cap(buf) <= 1024*1024 {
		*bufp = buf
		framePool.Put(bufp)
	}

	return Frame{Length: frameLen, MsgType: msgType, Payload: payload}, nil
}

func TestCRCComparisonMatchesProduction(t *testing.T) {
	for _, size := range []int{0, 1, 1024, 65536} {
		var encoded bytes.Buffer
		if err := EncodeAndWriteFrame(&encoded, &DataRecordMsg{Value: make([]byte, size)}); err != nil {
			t.Fatal(err)
		}
		valid := encoded.Bytes()
		corrupt := append([]byte(nil), valid...)
		corrupt[len(corrupt)-1] ^= 1
		for _, data := range [][]byte{valid, corrupt, valid[:3], valid[:len(valid)-1], {0, 0, 0, 4}, {255, 255, 255, 255}} {
			want, wantErr := ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
			got, gotErr := readFrameCRCComparison(bytes.NewReader(data), DefaultMaxFrameSize, true)
			if !reflect.DeepEqual(got, want) || !errors.Is(gotErr, wantErr) {
				t.Fatalf("size %d reference diverged: %v / %v", size, gotErr, wantErr)
			}
		}
	}
}

// BenchmarkCPUCRCVerificationLatency includes frame parsing and payload decoding.
// Both variants use the same test-only reader; only CRC computation differs.
func BenchmarkCPUCRCVerificationLatency(b *testing.B) {
	var encoded bytes.Buffer
	if err := EncodeAndWriteFrame(&encoded, &DataRecordMsg{Value: make([]byte, 1024)}); err != nil {
		b.Fatal(err)
	}
	for _, verify := range []bool{false, true} {
		name := "without_crc"
		if verify {
			name = "with_crc"
		}
		b.Run(name, func(b *testing.B) {
			reader := bytes.NewReader(encoded.Bytes())
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				reader.Reset(encoded.Bytes())
				frame, err := readFrameCRCComparison(reader, DefaultMaxFrameSize, verify)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := DecodePayload(frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTCPCRCVerificationLatency measures one frame at a time, acknowledged
// only after decode. Sender encoding is outside the timer to isolate reception;
// the CRC comparison reader and all other work are identical between variants.
func BenchmarkTCPCRCVerificationLatency(b *testing.B) {
	var encoded bytes.Buffer
	if err := EncodeAndWriteFrame(&encoded, &DataRecordMsg{Value: make([]byte, 1024)}); err != nil {
		b.Fatal(err)
	}
	for _, verify := range []bool{false, true} {
		name := "without_crc"
		if verify {
			name = "with_crc"
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
			result := make(chan error, 1)
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
				ack := []byte{1}
				for i := 0; i < b.N; i++ {
					var frame Frame
					frame, readErr = readFrameCRCComparison(reader, DefaultMaxFrameSize, verify)
					if readErr != nil {
						return
					}
					if _, readErr = DecodePayload(frame); readErr != nil {
						return
					}
					if _, readErr = receiver.Write(ack); readErr != nil {
						return
					}
				}
			}()
			ack := []byte{0}
			for i := 0; i < b.N; i++ {
				if _, err := sender.Write(encoded.Bytes()); err != nil {
					b.Fatal(err)
				}
				if _, err := io.ReadFull(sender, ack); err != nil {
					b.Fatal(err)
				}
				if ack[0] != 1 {
					b.Fatal("invalid receipt acknowledgement")
				}
			}
			if err := <-result; err != nil {
				b.Fatal(err)
			}
			b.StopTimer()
		})
	}
}
