package httpapi

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/worker"
)

func TestConfigurationRejection(t *testing.T) {
	for _, auth := range []Auth{{Type: "unknown"}, {Type: "bearer"}, {Type: "basic", Username: "u"}} {
		if err := auth.validate(); err == nil {
			t.Fatalf("accepted invalid auth %+v", auth)
		}
	}
	for _, cfg := range []SourceConfig{
		{}, {Address: ":0", Path: "invalid", AllowInsecure: true}, {Address: ":0", BufferSize: -1, AllowInsecure: true},
		{Address: ":0", MaxBatch: -1, AllowInsecure: true}, {Address: ":0", MaxBodySize: -1, AllowInsecure: true},
		{Address: ":0", AllowInsecure: true, Auth: Auth{Type: "bearer"}},
	} {
		if _, err := NewSource(cfg); err == nil {
			t.Fatalf("accepted invalid source config %+v", cfg)
		}
	}
	for _, cfg := range []SinkConfig{
		{}, {URL: "http://example.com"}, {URL: "https://user:password@example.com"}, {URL: "https://example.com/#fragment"},
		{URL: "https://example.com", BatchSize: -1}, {URL: "https://example.com", Timeout: -1}, {URL: "https://example.com", MaxAttempts: -1},
		{URL: "https://example.com", Backoff: "invalid"}, {URL: "https://example.com", InitialDelay: time.Second, MaxDelay: time.Millisecond},
		{URL: "https://example.com", Auth: Auth{Type: "bearer"}},
	} {
		if _, err := NewSink(cfg); err == nil {
			t.Fatalf("accepted invalid sink config %+v", cfg)
		}
	}
}
func TestWorkerFactories(t *testing.T) {
	sourceCfg, err := protocol.EncodeMsgPack(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	source, err := SourceFactory()(t.Context(), sourceCfg, worker.TaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.GenerateWatermark() != 0 {
		t.Fatal("unexpected watermark")
	}
	sinkCfg, err := protocol.EncodeMsgPack(SinkConfig{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := SinkFactory()(t.Context(), sinkCfg, worker.TaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if data, err := sink.Checkpoint(1); err != nil || data != nil {
		t.Fatalf("nontransactional sink snapshot %v %v", data, err)
	}
	if _, err = SourceFactory()(t.Context(), []byte{0xc1}, worker.TaskContext{}); err == nil {
		t.Fatal("decoded corrupt source config")
	}
	if _, err = SinkFactory()(t.Context(), []byte{0xc1}, worker.TaskContext{}); err == nil {
		t.Fatal("decoded corrupt sink config")
	}
}
func TestSourceLifecycleErrorsAndSequenceOverflow(t *testing.T) {
	source, err := NewSource(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.ReadBatch(t.Context()); err == nil {
		t.Fatal("read before Open succeeded")
	}
	if err = source.RestoreOffset(t.Context(), []byte{1}); err == nil {
		t.Fatal("accepted malformed offset")
	}
	if err = source.RestoreOffset(t.Context(), binary.BigEndian.AppendUint64(nil, math.MaxUint64)); err != nil {
		t.Fatal(err)
	}
	if err = source.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = source.Open(t.Context()); err == nil {
		t.Fatal("double Open succeeded")
	}
	if err = source.RestoreOffset(t.Context(), make([]byte, 8)); err == nil {
		t.Fatal("restore while running succeeded")
	}
	request := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(`{"events":[{"value":"v"}]}`))
	response := httptest.NewRecorder()
	source.ingest(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("sequence overflow: %d", response.Code)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = source.ReadBatch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("reader cancellation: %v", err)
	}
	broken, err := NewSource(SourceConfig{Address: "127.0.0.1:0", CertFile: "missing.crt", KeyFile: "missing.key"})
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	if err = broken.Open(t.Context()); err == nil {
		t.Fatal("opened missing TLS credentials")
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	source.ingest(response, httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(`{"events":[{}]}`)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed source ingest: %d", response.Code)
	}
}
func TestIngestRejectsMethodsPathsAndTrailingData(t *testing.T) {
	source, err := NewSource(SourceConfig{Address: ":0", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{http.MethodGet, "/ingest", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/other", "", http.StatusNotFound},
		{http.MethodPost, "/ingest", `{"events":[]}`, http.StatusBadRequest},
		{http.MethodPost, "/ingest", `{"events":[{}]} {}`, http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		source.ingest(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if response.Code != tc.code {
			t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
		}
	}
}
func TestSinkRetryExhaustionAndBasicAuth(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		u, p, ok := r.BasicAuth()
		if !ok || u != "u" || p != "p" {
			t.Error("missing basic auth")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	sink, err := NewSink(SinkConfig{URL: server.URL, AllowInsecure: true, Auth: Auth{Type: "basic", Username: "u", Password: "p"}, MaxAttempts: 3, InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	err = sink.Write(t.Context(), engine.Event{})
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || delivery.Permanent || calls != 3 || !strings.Contains(err.Error(), "503") {
		t.Fatalf("retry exhaustion: %v (%d calls)", err, calls)
	}
}
func TestSinkRejectsUnrepresentableEvents(t *testing.T) {
	sink, err := NewSink(SinkConfig{URL: "https://example.com", IdempotencyKeyField: "id"})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	for _, e := range []engine.Event{
		{Key: []byte{0xff}}, {Value: []byte{0xff}}, {Headers: map[string][]byte{"x": {0xff}}},
		{Value: []byte("not JSON")}, {Value: []byte(`{"other":1}`)}, {Value: []byte(`{"id":null}`)},
	} {
		if err = sink.Write(t.Context(), e); err == nil {
			t.Fatalf("accepted invalid event %+v", e)
		}
	}
	event, err := toJSON(engine.Event{Headers: map[string][]byte{"source": []byte("test")}})
	if err != nil || event.Headers["source"] != "test" {
		t.Fatalf("headers: %+v %v", event, err)
	}
}
func TestRetryAfterBounds(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"invalid", time.Second}, {"-1", time.Second}, {"0", 0}, {"2", 2 * time.Second}, {"999999999999", 3 * time.Second}} {
		if got := retryAfter(tc.value, time.Second, 3*time.Second); got != tc.want {
			t.Fatalf("Retry-After %q: %v", tc.value, got)
		}
	}
	if got := retryAfter(time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), time.Second, 3*time.Second); got != 0 {
		t.Fatalf("past date: %v", got)
	}
}
