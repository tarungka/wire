package tcp

import (
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMux_ServeAndDial(t *testing.T) {
	// Setup Server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	serverMux, err := NewMux(ln)
	require.NoError(t, err)
	defer serverMux.Close()

	go func() {
		_ = serverMux.Serve()
	}()

	// Accept loop
	go func() {
		for {
			conn, err := serverMux.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) // Echo server
			}(conn)
		}
	}()

	// Setup Client (also a Mux, though only acting as client here)
	// In real Wire, every node is both client and server
	clientLn, _ := net.Listen("tcp", "127.0.0.1:0")
	clientMux, _ := NewMux(clientLn)
	defer clientMux.Close()

	serverAddr := ln.Addr().String()

	// Test concurrent streams over single connection
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Dial a stream
			stream, err := clientMux.Dial(serverAddr, time.Second)
			require.NoError(t, err)
			defer stream.Close()

			msg := fmt.Sprintf("hello-%d", id)
			_, err = stream.Write([]byte(msg))
			require.NoError(t, err)

			buf := make([]byte, len(msg))
			_, err = io.ReadFull(stream, buf)
			require.NoError(t, err)

			assert.Equal(t, msg, string(buf))
		}(i)
	}
	wg.Wait()
}
