package tcp

import (
	"net"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/logger"
)

// BenchmarkMuxDispatch measures the per-connection demux cost: 1-byte read
// + map lookup + channel send. Uses net.Pipe so there is zero kernel/TCP
// overhead and the number is a pure dispatch ceiling.
func BenchmarkMuxDispatch(b *testing.B) {
	mux := &Mux{
		m:       make(map[byte]*listener),
		Timeout: 5 * time.Second,
		Logger:  logger.GetLogger("mux-bench"),
	}
	const header byte = 0x42
	ln := mux.Listen(header)
	internal := mux.m[header] // keep a typed reference so we can close ln.c

	// Drain the listener concurrently. Stays alive until ln.c is closed.
	done := make(chan struct{})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(done)
				return
			}
			c.Close()
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		serverConn, clientConn := net.Pipe()
		go func() {
			_, _ = clientConn.Write([]byte{header})
		}()
		// handleConn will defer wg.Done(), so balance with an Add(1)
		// per iteration since we are not driving it through Serve().
		mux.wg.Add(1)
		mux.handleConn(serverConn)
		clientConn.Close()
	}

	b.StopTimer()
	close(internal.c)
	<-done
}
