package coordinator

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/pprof"

	"github.com/rs/zerolog"
)

// HTTPServer serves the coordinator's HTTP API.
type HTTPServer struct {
	coord    *Coordinator
	server   *http.Server
	log      zerolog.Logger
	listener net.Listener
}

// NewHTTPServer creates a new HTTP server for the coordinator API.
func NewHTTPServer(coord *Coordinator, listenAddr string, log zerolog.Logger) *HTTPServer {
	s := &HTTPServer{
		coord: coord,
		log:   log.With().Str("component", "http").Logger(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /api/v1/cluster/leader", s.handleLeader)

	// Job endpoints.
	mux.HandleFunc("POST /api/v1/jobs", s.leaderOnly(s.handleSubmitJob))
	mux.HandleFunc("POST /api/v1/jobs/submit", s.leaderOnly(s.handleSubmitBinary))
	mux.HandleFunc("GET /api/v1/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/v1/jobs/{job_id}", s.handleGetJob)
	mux.HandleFunc("POST /api/v1/jobs/{job_id}/cancel", s.leaderOnly(s.handleCancelJob))
	mux.HandleFunc("POST /api/v1/jobs/{job_id}/pause", s.leaderOnly(s.handlePauseJob))
	mux.HandleFunc("POST /api/v1/jobs/{job_id}/resume", s.leaderOnly(s.handleResumeJob))

	// Savepoint endpoints.
	mux.HandleFunc("POST /api/v1/jobs/{job_id}/savepoints", s.leaderOnly(s.handleTriggerSavepoint))
	mux.HandleFunc("GET /api/v1/jobs/{job_id}/savepoints", s.handleListSavepoints)
	mux.HandleFunc("GET /api/v1/jobs/{job_id}/savepoints/{savepoint_id}", s.handleGetSavepoint)
	mux.HandleFunc("DELETE /api/v1/jobs/{job_id}/savepoints/{savepoint_id}", s.leaderOnly(s.handleDeleteSavepoint))

	// Cluster endpoints.
	mux.HandleFunc("GET /api/v1/cluster", s.handleClusterStatus)
	mux.HandleFunc("DELETE /api/v1/cluster/nodes/{node_id}", s.leaderOnly(s.handleRemoveNode))

	// Profiling endpoints. Exposed on the standard /debug/pprof/* paths so
	// `go tool pprof` and `go tool trace` work without extra config.
	// /debug/pprof/mutex and /debug/pprof/block return useful data only when
	// the process was started with --profile-extra (full-rate profiling has
	// non-trivial overhead and is opt-in).
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)

	s.server = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	return s
}

// Listen binds the server to its address without serving. Call Serve() after.
func (s *HTTPServer) Listen() error {
	var err error
	s.listener, err = net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.log.Info().Str("addr", s.listener.Addr().String()).Msg("HTTP server listening")
	return nil
}

// Serve starts serving on the already-bound listener. Blocks until shutdown.
func (s *HTTPServer) Serve() error {
	return s.server.Serve(s.listener)
}

// ListenAndServe binds and serves in one call. It blocks until the server is shut down.
func (s *HTTPServer) ListenAndServe() error {
	if err := s.Listen(); err != nil {
		return err
	}
	return s.Serve()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Addr returns the listener address (useful when binding to :0 for tests).
func (s *HTTPServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.server.Addr
}

// handleHealth returns 200 if the process is alive.
func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReady returns 200 if the coordinator is a recovered leader.
// For standby nodes, it returns 307 Temporary Redirect with a Location header
// pointing to the leader, enabling transparent HA failover for HTTP clients.
func (s *HTTPServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.coord.IsReady() {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	info, _, _ := s.coord.GetLeaderInfo()
	s.writeStandbyRedirect(w, r, info)
}

// leaderResponse is the JSON response for the leader endpoint.
type leaderResponse struct {
	LeaderID       string `json:"leader_id"`
	LeaderHTTPAddr string `json:"leader_http_addr"`
	LeaderEpoch    uint64 `json:"leader_epoch"`
	IsSelf         bool   `json:"is_self"`
}

// handleLeader returns information about the current cluster leader.
func (s *HTTPServer) handleLeader(w http.ResponseWriter, r *http.Request) {
	info, isSelf, err := s.coord.GetLeaderInfo()
	if err != nil {
		s.writeStandbyRedirect(w, r, nil)
		return
	}

	resp := leaderResponse{
		LeaderID:       info.NodeID,
		LeaderHTTPAddr: info.Address,
		LeaderEpoch:    info.Epoch,
		IsSelf:         isSelf,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeStandbyRedirect writes a 307 Temporary Redirect for standby nodes,
// pointing to the leader's address so HTTP clients can seamlessly follow.
func (s *HTTPServer) writeStandbyRedirect(w http.ResponseWriter, r *http.Request, info *LeaderInfo) {
	if info != nil && info.Address != "" {
		w.Header().Set("X-Wire-Leader-Id", info.NodeID)
		w.Header().Set("X-Wire-Leader-Addr", info.Address)
		// Build redirect URL preserving the original request path.
		location := "http://" + info.Address + r.URL.Path
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		w.Header().Set("Location", location)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte("redirecting to leader"))
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("no leader"))
}
