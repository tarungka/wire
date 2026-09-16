package coordinator

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// HAService keeps discovery available while campaigning and gives each grant
// its own coordinator, caches, sessions, and irrevocable metadata handle. A
// delayed old request can never see a subsequent term's store or mutable state.
type HAService struct {
	cfg       CoordinatorConfig
	election  LeaderElection
	openStore func() (MetadataStore, error)
	log       zerolog.Logger
	active    atomic.Pointer[haTerm]
	standby   *HTTPServer
	http      *http.Server
	transport *TransportServer
}

type haTerm struct {
	coord   *Coordinator
	ctx     context.Context
	handler http.Handler
}

func NewHAService(cfg CoordinatorConfig, rpcAddr string, election LeaderElection, openStore func() (MetadataStore, error), tlsConfig *tls.Config, log zerolog.Logger) *HAService {
	cfg.resolve()
	h := &HAService{cfg: cfg, election: election, openStore: openStore, log: log}
	standby := New(cfg, nil, election, log)
	h.standby = NewHTTPServer(standby, cfg.ListenAddr, log)
	h.http = &http.Server{Addr: cfg.ListenAddr, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(h.serveHTTP)}
	h.transport = NewTermTransportServer(func() (*Coordinator, context.Context) {
		term := h.active.Load()
		if term == nil {
			return nil, context.Background()
		}
		return term.coord, term.ctx
	}, rpcAddr, log, tlsConfig)
	return h
}

func (h *HAService) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if term := h.active.Load(); term != nil && term.coord.IsReady() {
		term.handler.ServeHTTP(w, r)
		return
	}
	// Standbys have no metadata store: only discovery and health are served.
	switch r.URL.Path {
	case "/healthz", "/readyz", "/api/v1/cluster/leader":
		h.standby.server.Handler.ServeHTTP(w, r)
	default:
		info, _, _ := h.standby.coord.GetLeaderInfo()
		h.standby.writeStandbyRedirect(w, r, info)
	}
}

// Run serves the node and campaigns until shutdown. Metadata is never opened
// while waiting for election. The database is closed before voluntary resign.
func (h *HAService) Run(ctx context.Context) error {
	if h.election == nil || h.openStore == nil {
		return fmt.Errorf("HA requires election and authoritative storage")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer h.election.Close()
	if err := h.transport.Listen(); err != nil {
		return err
	}
	defer h.transport.Shutdown(context.Background())
	httpDone := make(chan error, 1)
	rpcDone := make(chan error, 1)
	go func() { err := h.http.ListenAndServe(); httpDone <- err; cancel() }()
	go func() { err := h.transport.Serve(ctx); rpcDone <- err; cancel() }()
	err := h.campaign(ctx)
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	_ = h.http.Shutdown(shutdownCtx)
	httpErr, rpcErr := <-httpDone, <-rpcDone
	if errors.Is(httpErr, http.ErrServerClosed) {
		httpErr = nil
	}
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return errors.Join(err, httpErr, rpcErr)
}

func (h *HAService) campaign(ctx context.Context) error {
	for ctx.Err() == nil {
		grant, err := h.election.Campaign(ctx, h.cfg.NodeID)
		if err != nil {
			return err
		}
		err = h.runTerm(ctx, grant)
		// runTerm drains metadata ownership before this releases election.
		resignErr := h.election.Resign(context.Background())
		if resignErr != nil {
			return errors.Join(err, resignErr)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			// Invalid or inaccessible metadata is not a reason to advertise readiness
			// or restore an older snapshot. Leave authority and retry with backoff.
			h.log.Error().Err(err).Msg("HA term ended without serving or lost authority")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return ctx.Err()
}

func (h *HAService) runTerm(parent context.Context, grant *LeaderContext) error {
	ctx, cancel := context.WithCancel(grant.Ctx)
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	defer cancel()
	store, err := OpenLeadershipStore(ctx, h.openStore)
	if err != nil {
		return err
	}
	defer store.Close()
	coord := New(h.cfg, store, h.election, h.log)
	coord.state = StateLeader
	coord.epoch = grant.Epoch
	coord.leaderCtx, coord.leaderCancel = ctx, cancel
	if err := coord.recover(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if discovery, ok := h.election.(LeaderDiscovery); ok {
		if err := discovery.PublishLeader(ctx, LeaderInfo{NodeID: h.cfg.NodeID, Address: h.cfg.HTTPAdvertiseAddr, RPCAddress: h.cfg.RPCAdvertiseAddr, Epoch: coord.CurrentEpoch()}); err != nil {
			return err
		}
	}
	term := &haTerm{coord: coord, ctx: ctx, handler: NewHTTPServer(coord, h.cfg.ListenAddr, h.log).server.Handler}
	h.active.Store(term)
	defer h.active.CompareAndSwap(term, nil)
	err = coord.serve(ctx)
	cancel()
	return err
}
