package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tarungka/wire/internal/jobcli"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

const cliYAML = `apiVersion: wire/v1
kind: Pipeline
metadata: {name: cli-yaml}
spec:
  sources:
    - {name: input, type: http-api, config: {address: "127.0.0.1:8000", allow_insecure: true}}
  transforms:
    - {name: mapped, type: map, input: input, config: {expression: "value"}}
  sinks:
    - {name: output, type: http-api, input: mapped, config: {url: "https://example.invalid"}}
`

func TestYAMLCLISubmission(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/jobs" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		var body struct {
			Name      string `json:"name"`
			Graph     string `json:"graph_bytes"`
			Savepoint string `json:"savepoint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Name != "cli-yaml" || body.Savepoint != "savepoints/job/1" {
			t.Errorf("body=%+v", body)
		}
		data, err := base64.StdEncoding.DecodeString(body.Graph)
		if err != nil {
			t.Error(err)
		}
		var graph rpc.JobGraph
		if err := protocol.DecodeMsgPack(data, &graph); err != nil {
			t.Error(err)
		}
		if len(graph.Operators) != 3 {
			t.Errorf("operators=%v", graph.Operators)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"id":"job"}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "pipeline.yaml")
	if err := os.WriteFile(path, []byte(cliYAML), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"jobs", "submit", "--file", path, "--format", "yaml", "--coordinator", server.URL, "--savepoint", "savepoints/job/1"}
	if err := jobcli.RunWithPipelineCompiler(t.Context(), args, &out, io.Discard, compileYAMLPipeline); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !bytes.Contains(out.Bytes(), []byte(`"job"`)) {
		t.Fatalf("calls=%d output=%s", calls.Load(), out.String())
	}
	if err := os.WriteFile(path, []byte("invalid: pipeline"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := jobcli.RunWithPipelineCompiler(t.Context(), args, io.Discard, io.Discard, compileYAMLPipeline); err == nil {
		t.Fatal("invalid YAML accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("invalid pipeline sent to coordinator")
	}
}
