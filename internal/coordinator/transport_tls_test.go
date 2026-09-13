package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/worker"
)

func TestWorkerRegistersOverTLS(t *testing.T) {
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	c, _ := newReadyCoordinator(t)
	srv := NewTransportServer(c, "127.0.0.1:0", zerolog.Nop(), &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { serverDone <- srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Shutdown(context.Background()); <-serverDone }()
	w := worker.New(worker.Config{WorkerID: "tls-worker", CoordinatorAddr: srv.Addr(), TaskSlots: 1, TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}, zerolog.Nop())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() { cancel(); _ = w.Shutdown(context.Background()); <-done }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, registered := range c.ListWorkers() {
			if registered.ID == "tls-worker" {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("TLS worker did not register")
		case <-tick.C:
		}
	}
}
