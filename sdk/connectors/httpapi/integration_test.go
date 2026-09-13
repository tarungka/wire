package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tarungka/wire/sdk"
)

func TestEmbeddedHTTPSourceToSink(t *testing.T) {
	received := make(chan []byte, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(204)
	}))
	defer target.Close()
	source, err := NewSource(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := NewSink(SinkConfig{URL: target.URL, AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	env := sdk.New()
	env.AddSource(source).AddSink(sink)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := env.Execute(ctx); done <- err }()
	deadline := time.After(time.Second)
	for source.Address() == "" {
		select {
		case err := <-done:
			t.Fatalf("execution stopped: %v", err)
		case <-deadline:
			t.Fatal("source not opened by runtime")
		case <-time.After(time.Millisecond):
		}
	}
	if code := post(t, "http://"+source.Address()+"/ingest", `{"events":[{"key":"k","value":"v"}]}`, ""); code != 200 {
		t.Fatalf("ingest: %d", code)
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("sink did not receive event")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("execution did not stop")
	}
}
