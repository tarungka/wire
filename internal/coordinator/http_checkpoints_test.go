package coordinator

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

func TestHTTPCheckpointTriggerAndStatus(t *testing.T) {
	c, store := newReadyCoordinator(t)
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
	data, err := protocol.EncodeMsgPack(TaskAssignmentMap{JobID: "job", Assignments: map[string]string{"task": "worker"}, Replicas: map[string]string{"task": "peer:4004"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(JobAssignmentsKey("job"), data); err != nil {
		t.Fatal(err)
	}
	server := startTestHTTPServer(t, c)
	response, err := http.Post(fmt.Sprintf("http://%s/api/v1/jobs/job/checkpoints", server.Addr()), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("trigger status: %d", response.StatusCode)
	}
	response, err = http.Post(fmt.Sprintf("http://%s/api/v1/jobs/job/checkpoints", server.Addr()), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("overlapping trigger status: %d", response.StatusCode)
	}
	for _, tc := range []struct {
		id     string
		status int
	}{{"1", http.StatusOK}, {"2", http.StatusNotFound}, {"bad", http.StatusBadRequest}} {
		response, err := http.Get(fmt.Sprintf("http://%s/api/v1/jobs/job/checkpoints/%s", server.Addr(), tc.id))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("%s: status %d", tc.id, response.StatusCode)
		}
	}
}
