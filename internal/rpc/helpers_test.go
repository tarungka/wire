package rpc

import (
	"io"
	"net"
	"testing"

	"github.com/hashicorp/yamux"
	"github.com/rs/zerolog"
)

// testLogger returns a no-op zerolog logger for tests.
func testLogger() zerolog.Logger {
	return zerolog.Nop()
}

// testYamuxPair creates an in-memory yamux client/server pair for testing.
// Both sessions are cleaned up when the test finishes.
func testYamuxPair(t *testing.T) (client *yamux.Session, server *yamux.Session) {
	t.Helper()

	c1, c2 := net.Pipe()

	yamuxCfg := yamux.DefaultConfig()
	yamuxCfg.LogOutput = io.Discard

	var serverErr, clientErr error

	serverDone := make(chan struct{})
	go func() {
		server, serverErr = yamux.Server(c1, yamuxCfg)
		close(serverDone)
	}()

	client, clientErr = yamux.Client(c2, yamuxCfg)
	<-serverDone

	if serverErr != nil {
		t.Fatalf("yamux server creation failed: %v", serverErr)
	}
	if clientErr != nil {
		t.Fatalf("yamux client creation failed: %v", clientErr)
	}

	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	return client, server
}
