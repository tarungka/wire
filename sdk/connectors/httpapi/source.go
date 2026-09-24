package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

type SourceConfig struct {
	Address       string `codec:"address" json:"address"`
	Path          string `codec:"path" json:"path"`
	BufferSize    int    `codec:"buffer_size" json:"buffer_size"`
	MaxBatch      int    `codec:"max_batch" json:"max_batch"`
	MaxBodySize   int64  `codec:"max_body_size" json:"max_body_size"`
	Auth          Auth   `codec:"auth" json:"auth"`
	AllowInsecure bool   `codec:"allow_insecure" json:"allow_insecure"`
	CertFile      string `codec:"cert_file" json:"cert_file"`
	KeyFile       string `codec:"key_file" json:"key_file"`
}

// Source acknowledges acceptance into memory, not a durable checkpoint. Its
// sequence offset is diagnostic unless the sender implements replay externally.
type Source struct {
	config             SourceConfig
	mu                 sync.Mutex
	server             *http.Server
	address            string
	queue              []engine.Event
	received, consumed uint64
	notify             chan struct{}
	done               chan struct{}
	closed             bool
	serveErr           error
}

func NewSource(c SourceConfig) (*Source, error) {
	if c.Address == "" {
		return nil, fmt.Errorf("http-api: source address required")
	}
	if c.Path == "" {
		c.Path = "/ingest"
	}
	if c.Path[0] != '/' {
		return nil, fmt.Errorf("http-api: path must begin with /")
	}
	if c.BufferSize == 0 {
		c.BufferSize = 10000
	}
	if c.MaxBatch == 0 {
		c.MaxBatch = 100
	}
	if c.MaxBodySize == 0 {
		c.MaxBodySize = 10 << 20
	}
	if c.BufferSize < 1 || c.MaxBatch < 1 || c.MaxBodySize < 1 {
		return nil, fmt.Errorf("http-api: invalid source limits")
	}
	if !c.AllowInsecure && (c.CertFile == "" || c.KeyFile == "") {
		return nil, fmt.Errorf("http-api: TLS certificate/key required unless allow_insecure is enabled")
	}
	if err := c.Auth.validate(); err != nil {
		return nil, err
	}
	return &Source{config: c, notify: make(chan struct{}, 1), done: make(chan struct{})}, nil
}
func (s *Source) Open(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil || s.closed {
		return fmt.Errorf("http-api: source already opened or closed")
	}
	listener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		return err
	}
	if s.config.CertFile != "" || s.config.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.config.CertFile, s.config.KeyFile)
		if err != nil {
			_ = listener.Close()
			return err
		}
		listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
	s.address = listener.Addr().String()
	s.server = &http.Server{Handler: http.HandlerFunc(s.ingest), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		err := s.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.serveErr = err
			s.mu.Unlock()
			_ = s.Close()
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	return nil
}
func (s *Source) Address() string { s.mu.Lock(); defer s.mu.Unlock(); return s.address }
func (s *Source) ingest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.config.Path {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.config.Auth.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.config.MaxBodySize))
	decoder.DisallowUnknownFields()
	var request envelope
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		code := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			code = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "invalid event body", code)
		return
	}
	if decoder.Decode(new(any)) != io.EOF || len(request.Events) == 0 {
		http.Error(w, "expected one nonempty event envelope", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		http.Error(w, "source closed", http.StatusServiceUnavailable)
		return
	}
	if len(request.Events) > s.config.BufferSize-len(s.queue) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "buffer full", http.StatusTooManyRequests)
		return
	}
	if uint64(len(request.Events)) > math.MaxUint64-s.received {
		http.Error(w, "sequence exhausted", http.StatusServiceUnavailable)
		return
	}
	for _, event := range request.Events {
		s.queue = append(s.queue, event.event())
	}
	s.received += uint64(len(request.Events))
	select {
	case s.notify <- struct{}{}:
	default:
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"accepted": len(request.Events), "sequence": s.received})
}
func (s *Source) ReadBatch(ctx context.Context) ([]engine.Event, error) {
	// Yield a non-nil empty batch while idle so the runtime can service source
	// checkpoint boundaries. A nil batch is reserved for end of input.
	idle := time.NewTimer(100 * time.Millisecond)
	defer idle.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.closed {
			err := s.serveErr
			s.mu.Unlock()
			return nil, err
		}
		if s.server == nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("http-api: source not open")
		}
		if len(s.queue) > 0 {
			n := min(len(s.queue), s.config.MaxBatch)
			events := append([]engine.Event(nil), s.queue[:n]...)
			clear(s.queue[:n])
			s.queue = s.queue[n:]
			s.consumed += uint64(n)
			s.mu.Unlock()
			return events, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
		case <-s.notify:
		case <-idle.C:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return []engine.Event{}, nil
		}
	}
}
func (s *Source) GenerateWatermark() int64 { return 0 }
func (s *Source) Checkpoint(_ uint64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return binary.BigEndian.AppendUint64(nil, s.consumed), nil
}

// RestoreOffset restores the sequence only; it cannot replay the volatile queue.
// Call before Open, then arrange replay with the sender before accepting traffic.
func (s *Source) RestoreOffset(_ context.Context, offset []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil || s.closed {
		return fmt.Errorf("http-api: restore offset before Open")
	}
	if len(offset) != 8 {
		return fmt.Errorf("http-api: invalid offset")
	}
	s.consumed = binary.BigEndian.Uint64(offset)
	s.received = s.consumed
	return nil
}
func (s *Source) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	server := s.server
	s.queue = nil
	s.mu.Unlock()
	if server != nil {
		return server.Close()
	}
	return nil
}

// RestoreOffsetBeforeOpen lets worker and SDK runtimes restore sequence state
// before publishing the ingress listener. It does not recover queued events.
func (s *Source) RestoreOffsetBeforeOpen(ctx context.Context, offset []byte) error {
	return s.RestoreOffset(ctx, offset)
}
