package coordinator

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

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

func TestJobInspectionMetricsRespectAssignmentFences(t *testing.T) {
	for _, invalid := range []string{"", "worker", "job", "attempt", "epoch", "lost", "removed", "missing"} {
		t.Run(invalid, func(t *testing.T) {
			store := NewMemoryStore()
			defer func() { _ = store.Close() }()
			c := New(CoordinatorConfig{}, store, nil, zerolog.Nop())
			c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
			assignment := TaskAssignmentMap{JobID: "job", AttemptID: "attempt", EpochID: 3, Assignments: map[string]string{"task": "worker"}}
			raw, err := protocol.EncodeMsgPack(assignment)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(JobAssignmentsKey("job"), raw); err != nil {
				t.Fatal(err)
			}
			report := rpc.RunningTaskSummary{JobID: "job", TaskID: "task", AttemptID: "attempt", EpochID: 3, Metrics: &rpc.TaskMetrics{RecordsIn: 11, RecordsOut: 9, BytesIn: 100, BytesOut: 80, BackpressureMs: 7}}
			worker := &WorkerMeta{ID: "worker", LastHeartbeat: time.Unix(100, 0)}
			switch invalid {
			case "worker":
				worker.ID = "other"
			case "job":
				report.JobID = "other"
			case "attempt":
				report.AttemptID = "old"
			case "epoch":
				report.EpochID = 2
			case "lost":
				worker.Lost = true
			case "removed":
				worker.Removed = true
			case "missing":
				report.Metrics = nil
			}
			worker.TaskReports = []rpc.RunningTaskSummary{report}
			c.workers[worker.ID] = worker
			detail, err := c.jobInspection("job")
			if err != nil {
				t.Fatal(err)
			}
			metrics := detail.Tasks[0].Metrics
			if invalid != "" {
				if metrics != nil {
					t.Fatalf("exposed %s metrics: %+v", invalid, metrics)
				}
				return
			}
			if metrics == nil || metrics.RecordsIn != 11 || metrics.RecordsOut != 9 || metrics.BytesIn != 100 || metrics.BytesOut != 80 || metrics.BackpressureMs != 7 || metrics.ReportedAt != formatTime(worker.LastHeartbeat) {
				t.Fatalf("metrics=%+v", metrics)
			}
			report.Metrics.RecordsIn = 999
			if metrics.RecordsIn != 11 {
				t.Fatal("response aliases mutable heartbeat")
			}
		})
	}
}
