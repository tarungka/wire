package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

func post(t *testing.T, url, body, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}
func TestSourceIngestBackpressureAndOffset(t *testing.T) {
	cfg := SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true, BufferSize: 2, MaxBatch: 1, Auth: Auth{Type: "bearer", Token: "test"}}
	s, err := NewSource(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	url := "http://" + s.Address() + "/ingest"
	body := `{"events":[{"key":"a","value":"hello","event_time":123,"headers":{"x":"y"}},{"key":"b","value":"world"}]}`
	if code := post(t, url, body, ""); code != 401 {
		t.Fatalf("auth: %d", code)
	}
	if code := post(t, url, body, "test"); code != 200 {
		t.Fatalf("ingest: %d", code)
	}
	if code := post(t, url, body, "test"); code != 429 {
		t.Fatalf("backpressure: %d", code)
	}
	events, err := s.ReadBatch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || string(events[0].Value) != "hello" || events[0].EventTime != 123 || string(events[0].Headers["x"]) != "y" {
		t.Fatalf("events: %+v", events)
	}
	offset, err := s.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint64(offset) != 1 {
		t.Fatal("checkpoint counted accepted but unread events")
	}
	recovered, err := NewSource(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err = recovered.RestoreOffset(t.Context(), offset); err != nil {
		t.Fatal(err)
	}
	if err = recovered.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	if data, _ := recovered.Checkpoint(2); binary.BigEndian.Uint64(data) != 1 {
		t.Fatal("offset not restored")
	}
	if code := post(t, url, `{"events":[{"value":"third"}]}`, "test"); code != 200 {
		t.Fatalf("drained capacity: %d", code)
	}
	if code := post(t, url, `{invalid}`, "test"); code != 400 {
		t.Fatalf("bad JSON: %d", code)
	}
}
func TestSourceCloseAndLimits(t *testing.T) {
	s, err := NewSource(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true, MaxBodySize: 32})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	if code := post(t, "http://"+s.Address()+"/ingest", `{"events":[{"value":"this is much longer than thirty two bytes"}]}`, ""); code != 413 {
		t.Fatalf("body limit: %d", code)
	}
	done := make(chan error, 1)
	go func() { _, err := s.ReadBatch(context.Background()); done <- err }()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock reader")
	}
	if _, err = NewSource(SourceConfig{Address: ":0"}); err == nil {
		t.Fatal("implicit plaintext accepted")
	}
}
func TestSinkRetryPayloadAndStableIDs(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var ids, keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, data)
		ids = append(ids, r.Header.Get("X-Wire-Batch-ID"))
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing auth")
		}
		if len(bodies) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	s, err := NewSink(SinkConfig{URL: server.URL, AllowInsecure: true, Auth: Auth{Type: "bearer", Token: "test"}, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, IdempotencyKeyField: "id"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events := []engine.Event{{Key: []byte("k"), Value: []byte(`{"id":9007199254740993}`), EventTime: 10}}
	if err = s.WriteBatch(t.Context(), events); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) || ids[0] == "" || ids[0] != ids[1] || keys[0] == "" || keys[0] != keys[1] {
		t.Fatal("retry changed request identity or payload")
	}
	var payload envelope
	if err = json.Unmarshal(bodies[0], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Events[0].Value != string(events[0].Value) {
		t.Fatal("wire payload changed")
	}
}
func TestSinkPermanentFailureAndCancellation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(400) }))
	defer server.Close()
	s, err := NewSink(SinkConfig{URL: server.URL, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.Write(t.Context(), engine.Event{Value: []byte("event")})
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || !delivery.Permanent || calls != 1 {
		t.Fatalf("permanent failure: %v (%d attempts)", err, calls)
	}
	retryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "30"); w.WriteHeader(429) }))
	defer retryServer.Close()
	retrySink, err := NewSink(SinkConfig{URL: retryServer.URL, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer retrySink.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err = retrySink.Write(ctx, engine.Event{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry cancellation: %v", err)
	}
}

func TestSinkIdempotencyPreservesLargeIntegerIDs(t *testing.T) {
	keys := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys <- r.Header.Get("X-Idempotency-Key")
		w.WriteHeader(204)
	}))
	defer server.Close()
	sink, err := NewSink(SinkConfig{URL: server.URL, AllowInsecure: true, IdempotencyKeyField: "id"})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	for _, value := range []string{`{"id":9007199254740992}`, `{"id":9007199254740993}`} {
		if err = sink.Write(t.Context(), engine.Event{Value: []byte(value)}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := <-keys, <-keys
	if first == second {
		t.Fatal("distinct integer IDs collapsed into the same idempotency key")
	}
}

func TestSinkBatchSplittingAndRedirect(t *testing.T) {
	sizes := make(chan int, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body envelope
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		sizes <- len(body.Events)
		w.WriteHeader(204)
	}))
	defer server.Close()
	sink, err := NewSink(SinkConfig{URL: server.URL, AllowInsecure: true, BatchSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err = sink.WriteBatch(t.Context(), make([]engine.Event, 5)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{2, 2, 1} {
		if got := <-sizes; got != want {
			t.Fatalf("batch: %d want %d", got, want)
		}
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	redirected, err := NewSink(SinkConfig{URL: redirect.URL, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	defer redirected.Close()
	if err = redirected.Write(t.Context(), engine.Event{}); err == nil {
		t.Fatal("followed redirect")
	}
	select {
	case <-sizes:
		t.Fatal("redirect forwarded request")
	default:
	}
}
