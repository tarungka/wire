package coordinator

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

// TransportServer listens for worker RPC connections over TCP/Yamux.
type TransportServer struct {
	coord      *Coordinator
	listenAddr string
	rpcServer  *rpc.Server
	listener   net.Listener
	log        zerolog.Logger
	wg         sync.WaitGroup
}

// NewTransportServer creates a TransportServer that bridges worker RPC
// connections to the coordinator.
func NewTransportServer(coord *Coordinator, listenAddr string, log zerolog.Logger) *TransportServer {
	rpcCfg := rpc.DefaultConfig()
	srv := rpc.NewServer(rpcCfg)

	srv.Register(rpc.MethodRegisterWorker, coord.HandleRegisterWorker)
	srv.Register(rpc.MethodHeartbeat, coord.HandleHeartbeat)
	srv.Register(rpc.MethodUpdateTaskStatus, coord.HandleUpdateTaskStatus)

	return &TransportServer{
		coord:      coord,
		listenAddr: listenAddr,
		rpcServer:  srv,
		log:        log.With().Str("component", "transport-server").Logger(),
	}
}

// ListenAndServe starts accepting TCP connections. It blocks until ctx is
// canceled or an unrecoverable error occurs.
func (ts *TransportServer) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", ts.listenAddr)
	if err != nil {
		return fmt.Errorf("transport server: listen %s: %w", ts.listenAddr, err)
	}
	ts.listener = ln
	ts.log.Info().Str("addr", ts.listenAddr).Msg("transport server listening")

	// Close listener when context is canceled.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			ts.log.Warn().Err(err).Msg("accept failed")
			continue
		}

		ts.wg.Add(1)
		go func(c net.Conn) {
			defer ts.wg.Done()
			ts.handleConn(ctx, c)
		}(conn)
	}
}

// handleConn wraps a raw TCP connection in a Yamux server session and
// serves RPCs on it.
func (ts *TransportServer) handleConn(ctx context.Context, conn net.Conn) {
	tcfg := transport.DefaultConfig()
	session, err := transport.NewServerSession(conn, tcfg)
	if err != nil {
		ts.log.Warn().Err(err).Str("remote", conn.RemoteAddr().String()).Msg("session setup failed")
		_ = conn.Close()
		return
	}

	ts.log.Info().Str("remote", session.Addr()).Msg("worker connected")
	ts.rpcServer.ServeSession(ctx, session.YamuxSession())
	ts.log.Info().Str("remote", session.Addr()).Msg("worker disconnected")
}

// Shutdown closes the listener and waits for all connections to drain.
func (ts *TransportServer) Shutdown(_ context.Context) error {
	if ts.listener != nil {
		_ = ts.listener.Close()
	}
	ts.wg.Wait()
	ts.rpcServer.Stop()
	return nil
}
