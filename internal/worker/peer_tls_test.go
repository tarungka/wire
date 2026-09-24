package worker

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

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/transport"
)

func testPeerTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "peer-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "worker"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der, caDER}, PrivateKey: key}}, RootCAs: pool, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}
}
func TestPeerTLSRequiresMutualVerification(t *testing.T) {
	for _, mutate := range []func(*tls.Config){func(c *tls.Config) { c.InsecureSkipVerify = true }, func(c *tls.Config) { c.ClientAuth = tls.NoClientCert }, func(c *tls.Config) { c.RootCAs = nil }, func(c *tls.Config) { c.ClientCAs = nil }, func(c *tls.Config) { c.Certificates = nil }} {
		config := testPeerTLS(t)
		mutate(config)
		if err := validatePeerTLS(config); err == nil {
			t.Fatal("accepted incomplete mutual TLS")
		}
	}
	if err := validatePeerTLS(testPeerTLS(t)); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerPeerDataTransportUsesMutualTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	worker := &Worker{cfg: Config{PeerTLSConfig: testPeerTLS(t)}}
	cfg := worker.peerTransportConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.NodeID = "worker"
	server := transport.NewMux(cfg)
	defer server.Close()
	if err := server.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	clientConfig := worker.peerTransportConfig()
	clientConfig.NodeID = "worker"
	client := transport.NewMux(clientConfig)
	defer client.Close()
	sender, err := client.Dial(ctx, server.ListenAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	receiver, err := server.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	if _, err := receiver.ReceiveHandshake(); err != nil {
		t.Fatal(err)
	}
	if err := sender.WriteMessageContext(ctx, &protocol.DataRecordMsg{Key: []byte("key"), Value: []byte("secret-record")}); err != nil {
		t.Fatal(err)
	}
	message, err := receiver.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	record, ok := message.(*protocol.DataRecordMsg)
	if !ok || string(record.Value) != "secret-record" {
		t.Fatalf("record=%v", message)
	}
	for _, kind := range []string{"plaintext", "untrusted", "wrong-identity"} {
		t.Run(kind, func(t *testing.T) {
			rejected := transport.DefaultConfig()
			if kind == "untrusted" {
				rejected.TLSConfig = testPeerTLS(t)
			}
			if kind == "wrong-identity" {
				rejected = worker.peerTransportConfig()
				rejected.NodeID = "impostor"
			}
			mux := transport.NewMux(rejected)
			defer mux.Close()
			attempt, stop := context.WithTimeout(ctx, time.Second)
			defer stop()
			if stream, err := mux.Dial(attempt, server.ListenAddr()); err == nil {
				stream.Close()
				t.Fatal("unauthenticated data connection accepted")
			}
		})
	}
}

func TestWorkerPeerRejectsServerIdentityMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	worker := &Worker{cfg: Config{PeerTLSConfig: testPeerTLS(t)}}
	serverConfig := worker.peerTransportConfig()
	serverConfig.NodeID = "wrong-server"
	serverConfig.ListenAddr = "127.0.0.1:0"
	server := transport.NewMux(serverConfig)
	defer server.Close()
	if err := server.Listen(ctx); err != nil {
		t.Fatal(err)
	}
	clientConfig := worker.peerTransportConfig()
	clientConfig.NodeID = "worker"
	client := transport.NewMux(clientConfig)
	defer client.Close()
	if stream, err := client.Dial(ctx, server.ListenAddr()); err == nil {
		stream.Close()
		t.Fatal("accepted server identity not certified by its certificate")
	}
}
