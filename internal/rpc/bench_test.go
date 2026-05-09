package rpc

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"

	"github.com/hashicorp/yamux"

	"github.com/tarungka/wire/internal/protocol"
)

// BenchmarkWriteRPCFrame measures the per-frame encode cost — header build
// + length prefix + payload copy via the rpcFramePool. This is on every
// outbound RPC call from worker → coordinator and back.
func BenchmarkWriteRPCFrame(b *testing.B) {
	for _, size := range []int{0, 64, 1024, 16 * 1024} {
		b.Run(payloadName(size), func(b *testing.B) {
			frame := RPCFrame{
				MethodID:  MethodHeartbeat,
				RequestID: 0xDEADBEEF,
				Payload:   make([]byte, size),
			}
			var buf bytes.Buffer
			buf.Grow(size + 64)

			b.ReportAllocs()
			b.SetBytes(int64(size + RPCLengthFieldSize + RPCHeaderSize))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				buf.Reset()
				if err := WriteRPCFrame(&buf, frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReadRPCFrame measures the per-frame decode cost — read length,
// validate, allocate payload buffer, fill it.
func BenchmarkReadRPCFrame(b *testing.B) {
	for _, size := range []int{0, 64, 1024, 16 * 1024} {
		b.Run(payloadName(size), func(b *testing.B) {
			frame := RPCFrame{
				MethodID:  MethodHeartbeat,
				RequestID: 0xDEADBEEF,
				Payload:   make([]byte, size),
			}
			var encoded bytes.Buffer
			if err := WriteRPCFrame(&encoded, frame); err != nil {
				b.Fatal(err)
			}
			data := encoded.Bytes()

			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := ReadRPCFrame(bytes.NewReader(data), 1<<20); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkEncodeRPCRequest exercises the full request-side path: msgpack
// encode the message, then build + write the RPC frame. This is the cost
// per outbound RPC.
func BenchmarkEncodeRPCRequest(b *testing.B) {
	msg := &HeartbeatRequest{
		WorkerID:  "worker-bench-1234",
		EpochID:   42,
		Timestamp: 1708819200000,
		Load: &WorkerLoad{
			CPUUsage:    0.42,
			MemoryUsage: 0.31,
			ActiveSlots: 3,
			TotalSlots:  4,
		},
	}
	var buf bytes.Buffer
	buf.Grow(256)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := EncodeRPCRequest(&buf, MethodHeartbeat, uint64(i), msg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeRPCPayload measures the msgpack decode path on a typical
// heartbeat frame. Pairs with EncodeRPCRequest above.
func BenchmarkDecodeRPCPayload(b *testing.B) {
	msg := &HeartbeatRequest{
		WorkerID:  "worker-bench-1234",
		EpochID:   42,
		Timestamp: 1708819200000,
	}
	var buf bytes.Buffer
	if err := EncodeRPCRequest(&buf, MethodHeartbeat, 1, msg); err != nil {
		b.Fatal(err)
	}
	frame, err := ReadRPCFrame(bytes.NewReader(buf.Bytes()), 1<<20)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var out HeartbeatRequest
		if err := DecodeRPCPayload(frame, &out); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMethodName checks the cost of the method-ID → string lookup
// used in logging hot paths.
func BenchmarkMethodName(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = MethodName(MethodHeartbeat)
	}
}

// benchYamuxPair mirrors testYamuxPair but for benchmarks.
func benchYamuxPair(b *testing.B) (client, server *yamux.Session) {
	b.Helper()
	c1, c2 := net.Pipe()
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard

	done := make(chan struct{})
	var sErr, cErr error
	go func() {
		server, sErr = yamux.Server(c1, cfg)
		close(done)
	}()
	client, cErr = yamux.Client(c2, cfg)
	<-done
	if sErr != nil || cErr != nil {
		b.Fatalf("yamux setup: %v / %v", sErr, cErr)
	}
	b.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

// BenchmarkClientCall_Heartbeat exercises the full Client.Call → Server
// dispatch → response round-trip for a Heartbeat RPC. This is the floor
// for any worker→coordinator (or vice-versa) call latency on a single
// host (everything in-memory; no kernel TCP, no real network).
func BenchmarkClientCall_Heartbeat(b *testing.B) {
	client, server := benchYamuxPair(b)
	cfg := DefaultConfig()

	srv := &Server{
		cfg:      cfg,
		handlers: make(map[MethodID]Handler),
		log:      testLogger(),
	}
	srv.Register(MethodHeartbeat, func(ctx context.Context, reqID uint64, payload []byte) (any, *RPCError) {
		var req HeartbeatRequest
		if err := protocol.DecodeMsgPack(payload, &req); err != nil {
			return nil, NewRPCError(ErrCodeSerializationError, err.Error())
		}
		return &HeartbeatResponse{Accepted: true, EpochID: req.EpochID}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ServeSession(ctx, server)

	rpcClient := &Client{session: client, cfg: cfg, log: testLogger()}
	req := &HeartbeatRequest{WorkerID: "worker-bench", EpochID: 42}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := rpcClient.Heartbeat(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func payloadName(size int) string {
	switch size {
	case 0:
		return "payload=empty"
	case 64:
		return "payload=64B"
	case 1024:
		return "payload=1KB"
	case 16 * 1024:
		return "payload=16KB"
	}
	return "payload=other"
}
