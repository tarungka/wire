package coordinator

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestJobInspectionAssignmentSnapshot(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()
	c := New(CoordinatorConfig{}, store, nil, zerolog.Nop())
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
	assignment := TaskAssignmentMap{JobID: "job", AttemptID: "attempt-2", Assignments: map[string]string{"b": "worker-2", "a": "worker-1"}, TaskDescriptors: []rpc.TaskDescriptor{{TaskID: "a", OperatorID: "source", SubtaskIndex: 2}}}
	data, err := protocol.EncodeMsgPack(assignment)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(JobAssignmentsKey("job"), data); err != nil {
		t.Fatal(err)
	}
	c.taskStatuses["a"] = rpc.TaskStatusRunning
	detail, err := c.jobInspection("job")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Tasks) != 2 {
		t.Fatalf("tasks=%+v", detail.Tasks)
	}
	task := detail.Tasks[0]
	if task.TaskID != "a" || task.Operator != "source" || task.SubtaskIndex != 2 || task.WorkerID != "worker-1" || task.AttemptID != "attempt-2" || task.Status != "RUNNING" {
		t.Fatalf("task=%+v", task)
	}
	if detail.Tasks[1].Status != "UNKNOWN" {
		t.Fatal("unreported task status fabricated")
	}
	request := httptest.NewRequest("GET", "/api/v1/jobs/job", nil)
	request.SetPathValue("job_id", "job")
	response := httptest.NewRecorder()
	server := NewHTTPServer(c, "", zerolog.Nop())
	server.handleGetJob(response, request)
	var wireDetail jobDetailResponse
	if err := json.Unmarshal(response.Body.Bytes(), &wireDetail); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(wireDetail.Tasks) != 2 || wireDetail.Tasks[0].AttemptID != "attempt-2" {
		t.Fatalf("HTTP response: %d %s", response.Code, response.Body.String())
	}
	c.taskStatuses["a"] = rpc.TaskStatusFinished
	if detail.Tasks[0].Status != "RUNNING" {
		t.Fatal("snapshot changed")
	}
	if _, err := c.jobInspection("missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := store.Set(JobAssignmentsKey("job"), []byte("broken")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.jobInspection("job"); err == nil {
		t.Fatal("corruption silently hidden")
	}
}
