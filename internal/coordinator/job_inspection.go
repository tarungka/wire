package coordinator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type jobTaskMetricsResponse struct {
	RecordsIn      int64  `json:"records_in"`
	RecordsOut     int64  `json:"records_out"`
	BytesIn        int64  `json:"bytes_in"`
	BytesOut       int64  `json:"bytes_out"`
	BackpressureMs int64  `json:"backpressure_ms"`
	ReportedAt     string `json:"reported_at"`
}

type jobTaskResponse struct {
	Metrics      *jobTaskMetricsResponse `json:"metrics,omitempty"`
	TaskID       string                  `json:"task_id"`
	Operator     string                  `json:"operator,omitempty"`
	SubtaskIndex int32                   `json:"subtask_index"`
	Status       string                  `json:"status"`
	WorkerID     string                  `json:"worker_id"`
	AttemptID    string                  `json:"attempt_id,omitempty"`
}

// jobInspection snapshots the job and its current assignment together. Statuses
// are advisory: after leadership recovery a task remains UNKNOWN until reported.
func (c *Coordinator) jobInspection(jobID string) (jobDetailResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	job := c.jobs[jobID]
	if job == nil {
		return jobDetailResponse{}, ErrJobNotFound
	}
	result := jobDetailFromMeta(job)
	data, err := c.store.Get(JobAssignmentsKey(jobID))
	if err != nil {
		return jobDetailResponse{}, err
	}
	if len(data) == 0 {
		return result, nil
	}
	var assignment TaskAssignmentMap
	if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
		return jobDetailResponse{}, err
	}
	if assignment.JobID != jobID {
		return jobDetailResponse{}, fmt.Errorf("assignment job mismatch")
	}
	descriptors := make(map[string]rpc.TaskDescriptor, len(assignment.TaskDescriptors))
	for _, descriptor := range assignment.TaskDescriptors {
		descriptors[descriptor.TaskID] = descriptor
	}
	// Reports are advisory and ephemeral. Match all ownership fences before
	// attaching them, so a previous deployment cannot impersonate the new one.
	metrics := make(map[string]*jobTaskMetricsResponse)
	for _, worker := range c.workers {
		if worker.Lost || worker.Removed {
			continue
		}
		for _, report := range worker.TaskReports {
			if report.JobID != jobID || report.EpochID != assignment.EpochID || report.AttemptID != assignment.AttemptID || assignment.Assignments[report.TaskID] != worker.ID || report.Metrics == nil {
				continue
			}
			m := report.Metrics
			metrics[report.TaskID] = &jobTaskMetricsResponse{RecordsIn: m.RecordsIn, RecordsOut: m.RecordsOut, BytesIn: m.BytesIn, BytesOut: m.BytesOut, BackpressureMs: m.BackpressureMs, ReportedAt: formatTime(worker.LastHeartbeat)}
		}
	}
	for taskID, workerID := range assignment.Assignments {
		descriptor := descriptors[taskID]
		result.Tasks = append(result.Tasks, jobTaskResponse{Metrics: metrics[taskID], TaskID: taskID, Operator: descriptor.OperatorID, SubtaskIndex: descriptor.SubtaskIndex, WorkerID: workerID, AttemptID: assignment.AttemptID, Status: strings.ToUpper(c.taskStatuses[taskID].String())})
	}
	sort.Slice(result.Tasks, func(i, j int) bool { return result.Tasks[i].TaskID < result.Tasks[j].TaskID })
	return result, nil
}
