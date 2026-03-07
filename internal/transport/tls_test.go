package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// tlsTestCerts holds paths to ephemeral certificates for testing.
type tlsTestCerts struct {
	CACertFile     string
	ServerCertFile string
	ServerKeyFile  string
	ClientCertFile string
	ClientKeyFile  string
}

// generateTestCerts creates ephemeral CA, server, and client certs in a temp directory.
func generateTestCerts(t *testing.T) *tlsTestCerts {
	t.Helper()
	dir := t.TempDir()

	// Generate CA key and cert.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	// Write CA cert.
	caCertFile := filepath.Join(dir, "ca.crt")
	writePEM(t, caCertFile, "CERTIFICATE", caCertDER)

	// Generate server cert.
	serverKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	serverCertDER, _ := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	serverCertFile := filepath.Join(dir, "server.crt")
	serverKeyFile := filepath.Join(dir, "server.key")
	writePEM(t, serverCertFile, "CERTIFICATE", serverCertDER)
	writeKeyPEM(t, serverKeyFile, serverKey)

	// Generate client cert.
	clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientCertDER, _ := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	clientCertFile := filepath.Join(dir, "client.crt")
	clientKeyFile := filepath.Join(dir, "client.key")
	writePEM(t, clientCertFile, "CERTIFICATE", clientCertDER)
	writeKeyPEM(t, clientKeyFile, clientKey)

	return &tlsTestCerts{
		CACertFile:     caCertFile,
		ServerCertFile: serverCertFile,
		ServerKeyFile:  serverKeyFile,
		ClientCertFile: clientCertFile,
		ClientKeyFile:  clientKeyFile,
	}
}

func writePEM(t *testing.T, path, blockType string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		t.Fatalf("pem.Encode %s: %v", path, err)
	}
}

func writeKeyPEM(t *testing.T, path string, key *ecdsa.PrivateKey) {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	writePEM(t, path, "EC PRIVATE KEY", der)
}

func TestTLS_MutualAuth(t *testing.T) {
	certs := generateTestCerts(t)

	// Server TLS config with mutual auth.
	serverTLS, err := LoadTLSConfig(certs.ServerCertFile, certs.ServerKeyFile, true, certs.CACertFile)
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}

	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.TLSConfig = serverTLS
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = server.Close() }()
	addr := server.ListenAddr()

	// Client TLS config.
	clientTLS, err := NewTLSClientConfig(certs.ClientCertFile, certs.ClientKeyFile, certs.CACertFile)
	if err != nil {
		t.Fatalf("NewTLSClientConfig: %v", err)
	}

	cCfg := DefaultConfig()
	cCfg.TLSConfig = clientTLS
	clientMux := NewMux(cCfg)
	defer func() { _ = clientMux.Close() }()

	cs, err := clientMux.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = cs.Close() }()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer func() { _ = ss.Close() }()

	_, err = ss.ReceiveHandshake()
	if err != nil {
		t.Fatalf("ReceiveHandshake: %v", err)
	}

	// Exchange a DataRecord.
	dr := &protocol.DataRecordMsg{Key: []byte("tls-key"), Value: []byte("tls-value"), EventTime: 42}
	if err := cs.WriteMessage(dr); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	m, err := ss.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	got := m.(*protocol.DataRecordMsg)
	if string(got.Value) != "tls-value" {
		t.Errorf("Value: got %q", got.Value)
	}
}

func TestTLS_InvalidCert(t *testing.T) {
	certs := generateTestCerts(t)

	// Server with mutual auth.
	serverTLS, err := LoadTLSConfig(certs.ServerCertFile, certs.ServerKeyFile, true, certs.CACertFile)
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}

	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.TLSConfig = serverTLS
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = server.Close() }()
	addr := server.ListenAddr()

	// Client with no cert (should fail mutual TLS).
	clientTLS := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // Skip server verification for this test.
	}
	cCfg := DefaultConfig()
	cCfg.TLSConfig = clientTLS
	clientMux := NewMux(cCfg)
	defer func() { _ = clientMux.Close() }()

	_, err = clientMux.Dial(ctx, addr)
	if err == nil {
		t.Fatal("expected TLS handshake error with invalid/missing client cert")
	}
}

func TestTLS_ServerNameAutoInference(t *testing.T) {
	// Verify that if TLSConfig.ServerName is empty, NewClientSession
	// automatically infers it from the dial address.
	certs := generateTestCerts(t)

	// Server without mutual auth (so we don't need client certs for this test).
	serverTLS, err := LoadTLSConfig(certs.ServerCertFile, certs.ServerKeyFile, false, "")
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}

	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.TLSConfig = serverTLS
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = server.Close() }()

	// Client TLS config with ServerName intentionally empty.
	clientTLS, err := NewTLSClientConfig(certs.ClientCertFile, certs.ClientKeyFile, certs.CACertFile)
	if err != nil {
		t.Fatalf("NewTLSClientConfig: %v", err)
	}
	clientTLS.ServerName = "" // Clear to test auto-inference.

	cCfg := DefaultConfig()
	cCfg.TLSConfig = clientTLS
	clientMux := NewMux(cCfg)
	defer func() { _ = clientMux.Close() }()

	// Dial should succeed — ServerName should be auto-populated from the address.
	cs, err := clientMux.Dial(ctx, server.ListenAddr())
	if err != nil {
		t.Fatalf("Dial with empty ServerName should auto-infer: %v", err)
	}
	defer func() { _ = cs.Close() }()

	// Verify the stream actually works.
	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer func() { _ = ss.Close() }()
	_, err = ss.ReceiveHandshake()
	if err != nil {
		t.Fatalf("ReceiveHandshake: %v", err)
	}
}

func TestTLS_AllMessageTypes(t *testing.T) {
	certs := generateTestCerts(t)

	serverTLS, err := LoadTLSConfig(certs.ServerCertFile, certs.ServerKeyFile, true, certs.CACertFile)
	if err != nil {
		t.Fatalf("LoadTLSConfig: %v", err)
	}
	sCfg := DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	sCfg.TLSConfig = serverTLS
	server := NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = server.Close() }()
	addr := server.ListenAddr()

	clientTLS, err := NewTLSClientConfig(certs.ClientCertFile, certs.ClientKeyFile, certs.CACertFile)
	if err != nil {
		t.Fatalf("NewTLSClientConfig: %v", err)
	}
	cCfg := DefaultConfig()
	cCfg.TLSConfig = clientTLS
	clientMux := NewMux(cCfg)
	defer func() { _ = clientMux.Close() }()

	cs, err := clientMux.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = cs.Close() }()

	ss, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer func() { _ = ss.Close() }()
	if _, err := ss.ReceiveHandshake(); err != nil {
		t.Fatalf("ReceiveHandshake: %v", err)
	}

	// Send all message types (except Handshake which was already sent).
	messages := []any{
		&protocol.DataRecordMsg{Key: []byte("k"), Value: []byte("v"), EventTime: 1},
		&protocol.CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1000},
		&protocol.WatermarkMsg{Timestamp: 500, SourceID: "s-0"},
		&protocol.BackpressureMsg{StreamID: 1, State: protocol.BackpressurePause},
		&protocol.EndOfPartitionMsg{SourceID: "s", Reason: protocol.EndReasonExhausted},
	}

	for _, msg := range messages {
		if err := cs.WriteMessage(msg); err != nil {
			t.Fatalf("WriteMessage(%T): %v", msg, err)
		}
	}

	expected := []uint8{
		protocol.MsgTypeDataRecord,
		protocol.MsgTypeCheckpointBarrier,
		protocol.MsgTypeWatermark,
		protocol.MsgTypeBackpressure,
		protocol.MsgTypeEndOfPartition,
	}

	for i, want := range expected {
		m, err := ss.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		got := msgTypeOf(m)
		if got != want {
			t.Errorf("[%d]: got 0x%02X, want 0x%02X", i, got, want)
		}
	}
}
