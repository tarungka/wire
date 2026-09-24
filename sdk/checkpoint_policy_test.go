package sdk

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestClusterSubmissionCarriesCheckpointPolicy(t *testing.T) {
	policies := make(chan *rpc.CheckpointPolicy, 1)
	restarts := make(chan *rpc.RestartPolicy, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var request submitJobRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			raw, err := base64.StdEncoding.DecodeString(request.GraphBytes)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var graph rpc.JobGraph
			if err := protocol.DecodeMsgPack(raw, &graph); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			policies <- graph.CheckpointPolicy
			restarts <- graph.RestartPolicy
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"job","status":"FINISHED"}`))
	}))
	defer server.Close()
	env := New().SetCoordinator(server.URL).SetCheckpointInterval(time.Second).SetCheckpointTimeout(time.Minute).SetCheckpointMinPause(2 * time.Second).SetRestartStrategy(ExponentialBackoff(7, time.Second, time.Minute, 1.5))
	env.SetMode(Cluster)
	env.AddSourceNamed("source", "source", nil).AddSinkNamed("sink", "sink", nil)
	if _, err := env.ExecuteWithName(t.Context(), "policy"); err != nil {
		t.Fatal(err)
	}
	wantRestart, err := env.restartPolicy()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case policy := <-restarts:
		if policy == nil || *policy != *wantRestart {
			t.Fatalf("restart policy missing: %+v", policy)
		}
	default:
		t.Fatal("no restart policy received")
	}
	select {
	case policy := <-policies:
		if policy == nil || *policy != *env.checkpointPolicy() {
			t.Fatalf("policy missing from submission: %+v", policy)
		}
	default:
		t.Fatal("no submission")
	}
}
