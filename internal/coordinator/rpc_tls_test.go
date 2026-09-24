package coordinator

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
	"github.com/tarungka/wire/internal/worker"
	"github.com/tarungka/wire/sdk/connectors/memory"
)

func rpcTestTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "worker"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	pair := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, RootCAs: pool}
}

func TestCoordinatorRPCMutualTLSAndWorkerIdentity(t *testing.T) {
	c, _ := newTestCoordinator(t)
	serverTLS, clientTLS := rpcTestTLS(t)
	srv := NewTransportServer(c, "127.0.0.1:0", zerolog.Nop(), serverTLS)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Shutdown(context.Background()); <-done }()
	for _, mode := range []string{"valid", "wrong identity", "no certificate"} {
		t.Run(mode, func(t *testing.T) {
			cfg := transport.DefaultConfig()
			cfg.TLSConfig = clientTLS.Clone()
			if mode == "no certificate" {
				cfg.TLSConfig.Certificates = nil
			}
			ctx, stop := context.WithTimeout(ctx, time.Second)
			defer stop()
			session, err := transport.NewClientSessionContext(ctx, srv.Addr(), cfg)
			if err != nil {
				if mode == "no certificate" {
					return
				}
				t.Fatal(err)
			}
			defer session.Close()
			id := "worker"
			if mode == "wrong identity" {
				id = "impostor"
			}
			_, err = rpc.NewClient(session.YamuxSession(), rpc.DefaultConfig()).RegisterWorker(ctx, &rpc.RegisterWorkerRequest{WorkerID: id, Address: "worker:1", TaskSlotsTotal: 1})
			if (err == nil) != (mode == "valid") {
				t.Fatalf("mode=%s registration error=%v", mode, err)
			}
		})
	}
}

func TestReservedJobRunsOverMutualTLS(t *testing.T) {
	c, _ := newTestCoordinator(t)
	serverTLS, clientTLS := rpcTestTLS(t)
	srv := NewTransportServer(c, "127.0.0.1:0", zerolog.Nop(), serverTLS)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan struct{})
	go func() { defer close(serverDone); _ = srv.Serve(ctx) }()
	registry := worker.NewRegistry()
	registry.RegisterSource("memory-source", memory.SourceFactory())
	registry.RegisterSink("memory-sink", memory.SinkFactory())
	w := worker.NewWithRegistry(worker.Config{WorkerID: "worker", TaskSlots: 1, CoordinatorAddr: srv.Addr(), RPCTLSConfig: clientTLS}, registry, zerolog.Nop())
	workerDone := make(chan error, 1)
	go func() { workerDone <- w.Run(ctx) }()
	defer func() {
		cancel()
		_ = srv.Shutdown(context.Background())
		<-serverDone
		select {
		case err := <-workerDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	wait := func(condition func() bool) {
		t.Helper()
		until := time.Now().Add(3 * time.Second)
		for !condition() {
			if time.Now().After(until) {
				t.Fatal("RPC lifecycle did not finish")
			}
			time.Sleep(time.Millisecond)
		}
	}
	wait(func() bool {
		c.mu.RLock()
		defer c.mu.RUnlock()
		// Registration publishes the reverse peer before its reply reaches the
		// worker. WatchCommands starts only after the worker accepts/persists
		// the epoch, so reservations are then ready. This test schedules once;
		// unlike the production scheduler it cannot retry an early refusal.
		return c.workers["worker"] != nil && c.workers["worker"].RPCClient != nil && c.cmdStreams["worker"] != nil
	})
	sinkID := t.Name()
	defer memory.Reset(sinkID)
	graph := rpc.JobGraph{Operators: []rpc.OperatorDescriptor{
		{OperatorID: "source", Type: rpc.OperatorTypeSource, Parallelism: 1, ClassName: "memory-source", Config: encode(t, memory.SourceConfig{Events: [][]byte{[]byte("record")}})},
		{OperatorID: "sink", Type: rpc.OperatorTypeSink, Parallelism: 1, ClassName: "memory-sink", Config: encode(t, memory.SinkConfig{SinkID: sinkID})},
	}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyForward}}}
	job := &JobMeta{ID: "tls-job", Status: JobCreated, Parallelism: 1, Config: encode(t, graph)}
	c.mu.Lock()
	c.jobs[job.ID] = job
	c.mu.Unlock()
	c.scheduleJob(job)
	wait(func() bool { c.mu.RLock(); defer c.mu.RUnlock(); return job.Status == JobFinished })
	records := memory.Collected(sinkID)
	if len(records) != 1 || string(records[0].Value) != "record" {
		t.Fatalf("incorrect output: %v", records)
	}
}
