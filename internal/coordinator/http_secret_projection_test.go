package coordinator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestHTTPSJobInspectionOmitsConnectorCredentials(t *testing.T) {
	const resolved = "resolved-inspection-token"
	t.Setenv("WIRE_INSPECTION_TOKEN", resolved)
	c, store := newTestCoordinator(t)
	s := NewHTTPServer(c, "", zerolog.Nop())
	key := "wk_live_" + strings.Repeat("a", 32)
	authFile := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"users":[{"username":"admin","role":"admin","api_key":"`+key+`"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureAuth(authFile); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(s.server.Handler)
	defer server.Close()
	graph := linearGraph()
	config := []byte(`{"password":"literal-db-password","headers":{"Authorization":"Bearer ${WIRE_INSPECTION_TOKEN}"},"nested":{"api_key":"literal-api-key"}}`)
	graph.Operators[0].Config = config
	encoded, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(submitJobRequest{Name: "inspection", Parallelism: 1, GraphBytes: base64.StdEncoding.EncodeToString(encoded)})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body []byte, status int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
		}
		for _, credential := range []string{resolved, "literal-db-password", "literal-api-key", key, base64.StdEncoding.EncodeToString(config), base64.StdEncoding.EncodeToString(encoded)} {
			if bytes.Contains(data, []byte(credential)) {
				t.Fatal("credential exposed in API response")
			}
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		assertNoConfigurationFields(t, value)
		return data
	}
	created := request("POST", "/api/v1/jobs", body, http.StatusCreated)
	var job jobDetailResponse
	if err := json.Unmarshal(created, &job); err != nil {
		t.Fatal(err)
	}
	if job.ID == "" {
		t.Fatal("submission omitted job identity")
	}
	taskID := job.ID + "/source/0"
	assignments := TaskAssignmentMap{JobID: job.ID, AttemptID: "attempt", Assignments: map[string]string{taskID: "worker"}, TaskDescriptors: []rpc.TaskDescriptor{{TaskID: taskID, OperatorID: "source", OperatorChain: graph.Operators}}}
	raw, err := protocol.EncodeMsgPack(assignments)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(JobAssignmentsKey(job.ID), raw); err != nil {
		t.Fatal(err)
	}
	request("GET", "/api/v1/jobs", nil, http.StatusOK)
	inspected := request("GET", "/api/v1/jobs/"+job.ID, nil, http.StatusOK)
	var detail jobDetailResponse
	if err := json.Unmarshal(inspected, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != job.ID || len(detail.Tasks) != 1 || detail.Tasks[0].TaskID != taskID {
		t.Fatal("inspection lost non-sensitive job or task details")
	}
	persisted, err := store.Get(JobConfigKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persisted, encoded) {
		t.Fatal("API projection changed durable configuration")
	}
}

func assertNoConfigurationFields(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch strings.ToLower(key) {
			case "config", "cfg", "graph_bytes", "operatorchain", "operator_chain", "secretvalues", "secret_values":
				t.Fatalf("API exposed configuration field %q", key)
			}
			assertNoConfigurationFields(t, child)
		}
	case []any:
		for _, child := range value {
			assertNoConfigurationFields(t, child)
		}
	}
}
