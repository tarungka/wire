package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExportSubmissionMatchesRemoteExecution(t *testing.T) {
	var exported submitJobRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var actual submitJobRequest
			if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
				t.Error(err)
			}
			if actual != exported {
				t.Error("exported graph differs from submitted graph")
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"job"}`)
		} else {
			_, _ = io.WriteString(w, `{"id":"job","status":"FINISHED"}`)
		}
	}))
	defer server.Close()
	env := New().SetMode(Cluster).SetCoordinator(server.URL).SetParallelism(2).SetKeyGroups(16).SetCheckpointInterval(time.Second).SetRestartStrategy(FixedDelay(3, time.Second))
	env.AddSourceNamed("source", "source-class", []byte(`{"offset":4}`)).MapNamed("map", "map-class", nil).AddSinkNamed("sink", "sink-class", nil)
	first, err := env.ExportSubmission("exported")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.ExportSubmission("exported")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || env.executed {
		t.Fatal("export changed environment or payload")
	}
	if err := json.Unmarshal(first, &exported); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := env.ExecuteWithName(ctx, "exported"); err != nil {
		t.Fatal(err)
	}
}

func TestExportSubmissionRejectsInvalidAndOversizedGraphs(t *testing.T) {
	for _, mode := range []string{"empty-name", "key-groups", "anonymous", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			env := New()
			name := "export"
			config := []byte(nil)
			if mode == "oversized" {
				config = []byte(strings.Repeat("x", 4<<20))
			}
			stream := env.AddSourceNamed("source", "source", config)
			if mode == "anonymous" {
				stream = stream.Map(func(e Event) (Event, error) { return e, nil })
			}
			stream.AddSinkNamed("sink", "sink", nil)
			if mode == "empty-name" {
				name = ""
			}
			if mode == "key-groups" {
				env.SetKeyGroups(3)
			}
			if _, err := env.ExportSubmission(name); err == nil {
				t.Fatal("invalid export accepted")
			}
			if env.executed {
				t.Fatal("failed export consumed execution")
			}
		})
	}
}
