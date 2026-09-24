package coordinator

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type liveConfigFailStore struct{ MetadataStore }

func (liveConfigFailStore) WriteBatch([]KVPair) error { return errors.New("disk unavailable") }

func TestLiveCheckpointIntervalIsDurableWithoutRedeployment(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	job := c.jobs["job"]
	job.Config = encode(t, rpc.JobGraph{})
	now := time.Now().UTC()
	job.RunningSince = now.Add(-time.Minute)
	job.CheckpointPolicy = &rpc.CheckpointPolicy{Interval: time.Hour, Timeout: time.Minute, MinPause: time.Second}
	assignments, err := store.Get(JobAssignmentsKey("job"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.duePeriodicCheckpoints(now)) != 0 {
		t.Fatal("old interval already due")
	}
	updated, err := c.SetCheckpointInterval("job", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != JobRunning || updated.RunningSince != now.Add(-time.Minute) || len(c.duePeriodicCheckpoints(now)) != 1 {
		t.Fatal("live interval not scheduled or job restarted")
	}
	persisted, err := store.Get(JobMetaKey("job"))
	if err != nil {
		t.Fatal(err)
	}
	var restored JobMeta
	if err := protocol.DecodeMsgPack(persisted, &restored); err != nil {
		t.Fatal(err)
	}
	graphData, err := store.Get(JobConfigKey("job"))
	if err != nil {
		t.Fatal(err)
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(graphData, &graph); err != nil {
		t.Fatal(err)
	}
	if restored.CheckpointPolicy.Interval != time.Second || graph.CheckpointPolicy.Interval != time.Second || graph.CheckpointPolicy.Timeout != time.Minute {
		t.Fatal("policy not persisted consistently")
	}
	after, err := store.Get(JobAssignmentsKey("job"))
	if err != nil || string(after) != string(assignments) || len(c.DrainCommands("worker")) != 0 {
		t.Fatal("live update changed deployment")
	}
	c.store = liveConfigFailStore{store}
	if _, err := c.SetCheckpointInterval("job", time.Hour); err == nil {
		t.Fatal("write failure ignored")
	}
	if job.CheckpointPolicy.Interval != time.Second {
		t.Fatal("failed update published")
	}
	c.store = store
	if _, err := c.SetCheckpointInterval("job", 0); err != nil {
		t.Fatal(err)
	}
	if len(c.duePeriodicCheckpoints(now)) != 0 {
		t.Fatal("disabled interval still scheduled")
	}
}

func TestHTTPLiveCheckpointIntervalValidation(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	c.jobs["job"].Config = encode(t, rpc.JobGraph{})
	server := NewHTTPServer(c, "", zerolog.Nop())
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"interval":"2s"}`, 200}, {`{"interval":"0s"}`, 200},
		{`{}`, 400}, {`{"interval":null}`, 400}, {`{"interval":"-1s"}`, 400},
		{`{"interval":"2s","extra":true}`, 400}, {`{"interval":"2s"} {}`, 400},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/api/v1/jobs/job/checkpoint-interval", strings.NewReader(tc.body))
		server.server.Handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("body=%s status=%d response=%s", tc.body, response.Code, response.Body.String())
		}
		if tc.status == 200 {
			var result struct {
				Interval string `json:"checkpoint_interval"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Interval != c.jobs["job"].CheckpointPolicy.Interval.String() {
				t.Fatal("status did not expose updated interval")
			}
		}
	}
	if apiRoleAllowed("viewer", http.MethodPut, "/api/v1/jobs/job/checkpoint-interval") || !apiRoleAllowed("operator", http.MethodPut, "/api/v1/jobs/job/checkpoint-interval") {
		t.Fatal("incorrect live configuration authorization")
	}
}

func TestLiveIntervalPreconditionRejectsStaleWriter(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	job := c.jobs["job"]
	job.Config = encode(t, rpc.JobGraph{})
	if _, err := c.SetCheckpointInterval("job", time.Second); err != nil {
		t.Fatal(err)
	}
	expected := time.Second
	if _, err := c.setCheckpointInterval("job", 2*time.Second, &expected); err != nil {
		t.Fatal(err)
	}
	before, err := store.Get(JobMetaKey("job"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.setCheckpointInterval("job", 3*time.Second, &expected); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("stale update=%v", err)
	}
	after, err := store.Get(JobMetaKey("job"))
	if err != nil || string(before) != string(after) || job.CheckpointPolicy.Interval != 2*time.Second {
		t.Fatal("stale update mutated state")
	}
}

func TestHTTPStaleIntervalReturnsConflict(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	c.jobs["job"].Config = encode(t, rpc.JobGraph{})
	if _, err := c.SetCheckpointInterval("job", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server := NewHTTPServer(c, "", zerolog.Nop())
	server.server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/api/v1/jobs/job/checkpoint-interval", strings.NewReader(`{"interval":"3s","expected_interval":"1s"}`)))
	if response.Code != http.StatusConflict || c.jobs["job"].CheckpointPolicy.Interval != 2*time.Second {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
