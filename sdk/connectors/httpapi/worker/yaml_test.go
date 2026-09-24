package worker_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tarungka/wire/sdk"
	httpworker "github.com/tarungka/wire/sdk/connectors/httpapi/worker"
)

func TestYAMLHTTPFactoryDelivery(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("authentication missing")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		received <- string(data)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	registry := sdk.NewWorkerRegistry()
	httpworker.Register(registry)
	httpworker.RegisterYAML(registry)
	data, err := json.Marshal(map[string]any{"url": server.URL, "allow_insecure": true, "timeout": "1s", "initial_delay": "1ms", "max_delay": "2ms", "auth": map[string]string{"type": "bearer", "token": "test-token"}})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := httpworker.YAMLSinkFactory()(t.Context(), data, sdk.WorkerTaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sink.Write(t.Context(), sdk.Event{Value: []byte(`{"id":42}`)}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Events []struct {
			Value string `json:"value"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(<-received), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Events) != 1 || envelope.Events[0].Value != `{"id":42}` {
		t.Fatalf("body=%+v", envelope)
	}
	for _, invalid := range []string{`null`, `{}`, `{"url":"https://example.com","timeout":12}`, `{"url":"https://example.com","timeout":"bad"}`, `{"url":"https://example.com","unexpected":true}`, `{"url":"https://example.com","auth":{"unknown":"secret"}}`, `{} {}`} {
		if _, err := httpworker.YAMLSinkFactory()(t.Context(), []byte(invalid), sdk.WorkerTaskContext{}); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
	sourceData := []byte(`{"address":"127.0.0.1:0","allow_insecure":true,"max_batch":2}`)
	a, err := httpworker.YAMLSourceFactory()(t.Context(), sourceData, sdk.WorkerTaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := httpworker.YAMLSourceFactory()(t.Context(), sourceData, sdk.WorkerTaskContext{})
	if err != nil || a == b {
		t.Fatalf("source ownership: %v", err)
	}
	defer a.Close()
	defer b.Close()
	if _, err := httpworker.YAMLSourceFactory()(t.Context(), []byte(`{"address":"127.0.0.1:0","allow_insecure":true,"max_bach":2}`), sdk.WorkerTaskContext{}); err == nil {
		t.Fatal("unknown source field accepted")
	}
}
