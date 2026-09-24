package sdk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/tarungka/wire/sdk/connectors/httpapi"
)

func TestHTTPRuntimeBatchesFlatMapOutput(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	var values []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []struct {
				Value string `json:"value"`
			} `json:"events"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		sizes = append(sizes, len(body.Events))
		for _, e := range body.Events {
			values = append(values, e.Value)
		}
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer server.Close()
	sink, err := httpapi.NewSink(httpapi.SinkConfig{URL: server.URL, AllowInsecure: true, BatchSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	env := New()
	env.AddSource(&sliceSource{events: []Event{{Value: []byte("seed")}}}).FlatMap(func(Event) ([]Event, error) {
		events := make([]Event, 205)
		for i := range events {
			events[i] = Event{Value: []byte(fmt.Sprint(i))}
		}
		return events, nil
	}).AddSink(sink)
	if _, err := env.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(sizes, []int{100, 100, 5}) {
		t.Fatalf("batch sizes=%v", sizes)
	}
	for i, value := range values {
		if value != fmt.Sprint(i) {
			t.Fatalf("record %d=%q", i, value)
		}
	}
}
