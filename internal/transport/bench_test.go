package transport

import (
	"context"
	"sync"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

// newBenchMuxPair stands up a server + client Mux pair on loopback. Mirrors
// newTestMuxPair but for benchmarks (no testing.T-only Cleanup helper).
func newBenchMuxPair(b *testing.B) (server *Mux, client *Mux, serverAddr string) {
	b.Helper()

	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	server = NewMux(sCfg)
	if err := server.Listen(context.Background()); err != nil {
		b.Fatalf("server Listen: %v", err)
	}
	serverAddr = server.ListenAddr()

	cCfg := DefaultConfig()
	client = NewMux(cCfg)

	b.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	return server, client, serverAddr
}

// dialWithHandshake dials a stream and completes the handshake on both
// sides. Returns the client and accepted server stream.
func dialWithHandshake(b *testing.B, server, client *Mux, addr string) (*FrameStream, *FrameStream) {
	b.Helper()
	ctx := context.Background()

	var (
		serverStream *FrameStream
		acceptErr    error
		wg           sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		serverStream, acceptErr = server.Accept(ctx)
		if acceptErr == nil {
			_, acceptErr = serverStream.ReceiveHandshake()
		}
	}()

	clientStream, err := client.Dial(ctx, addr)
	if err != nil {
		b.Fatalf("Dial: %v", err)
	}
	wg.Wait()
	if acceptErr != nil {
		b.Fatalf("Accept/Handshake: %v", acceptErr)
	}
	return clientStream, serverStream
}

// BenchmarkFrameStream_WriteRead measures a one-way send through a real
// loopback Yamux session: client writes a DataRecord, server reads it.
// Sequential, no parallelism — the number is per-message latency over a
// real TCP socket on localhost.
func BenchmarkFrameStream_WriteRead(b *testing.B) {
	for _, size := range []int{64, 1024, 16 * 1024} {
		b.Run(payloadSizeName(size), func(b *testing.B) {
			server, client, addr := newBenchMuxPair(b)
			clientStream, serverStream := dialWithHandshake(b, server, client, addr)
			defer clientStream.Close()
			defer serverStream.Close()

			msg := &protocol.DataRecordMsg{
				Key:       []byte("k"),
				Value:     make([]byte, size),
				EventTime: 1708819200000,
			}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if err := clientStream.WriteMessage(msg); err != nil {
					b.Fatal(err)
				}
				if _, err := serverStream.ReadMessage(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFrameStream_StreamSetup measures the cost of opening a fresh
// stream (Dial + Accept + handshake) on top of an existing Yamux session.
// This is what every new logical worker→coordinator RPC pays.
func BenchmarkFrameStream_StreamSetup(b *testing.B) {
	server, client, addr := newBenchMuxPair(b)

	// Warm one session so we measure stream-open cost only, not the
	// initial TCP/Yamux session establishment.
	cs, ss := dialWithHandshake(b, server, client, addr)
	cs.Close()
	ss.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		clientStream, serverStream := dialWithHandshake(b, server, client, addr)
		clientStream.Close()
		serverStream.Close()
	}
}

func payloadSizeName(size int) string {
	switch size {
	case 64:
		return "size=64B"
	case 1024:
		return "size=1KB"
	case 16 * 1024:
		return "size=16KB"
	}
	return "size=other"
}
