package sdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// clusterExecutor submits a graph to a remote Wire coordinator via HTTP and
// polls for completion.
type clusterExecutor struct {
	env *StreamExecutionEnvironment
}

// submitJobRequest mirrors coordinator.submitJobRequest. Kept in the SDK as
// a minimal, stable contract.
type submitJobRequest struct {
	Name        string `json:"name"`
	Parallelism int    `json:"parallelism"`
	Config      string `json:"config,omitempty"`
	GraphBytes  string `json:"graph_bytes,omitempty"`
}

// jobStatusResponse is the subset of coordinator.jobDetailResponse that we
// care about for completion polling.
type jobStatusResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (ex *clusterExecutor) run(ctx context.Context, jobName string) (*JobResult, error) {
	if ex.env.coordinatorURL == "" {
		return nil, fmt.Errorf("sdk: cluster mode requires env.SetCoordinator(url)")
	}
	start := time.Now()

	// Encode the graph.
	graph := ex.env.graph.toJobGraph(ex.env.parallelism)
	graphBytes, err := protocol.EncodeMsgPack(&graph)
	if err != nil {
		return nil, fmt.Errorf("sdk: encode job graph: %w", err)
	}

	if jobName == "" {
		jobName = fmt.Sprintf("sdk-job-%d", time.Now().UnixNano())
	}

	// Submit.
	submit := submitJobRequest{
		Name:        jobName,
		Parallelism: ex.env.parallelism,
		GraphBytes:  base64.StdEncoding.EncodeToString(graphBytes),
	}
	jobID, err := ex.submit(ctx, submit)
	if err != nil {
		return nil, err
	}

	// Poll until terminal.
	finalStatus, err := ex.poll(ctx, jobID)
	if err != nil {
		return &JobResult{
			JobID: jobID,
			Err:   err,
			Metrics: JobMetrics{
				Duration: time.Since(start),
			},
		}, err
	}

	res := &JobResult{
		JobID: jobID,
		Metrics: JobMetrics{
			Duration: time.Since(start),
		},
	}
	switch strings.ToUpper(finalStatus) {
	case "FINISHED":
		return res, nil
	case "FAILED", "CANCELED":
		res.Err = fmt.Errorf("sdk: job %s ended with status %s", jobID, finalStatus)
		return res, res.Err
	default:
		res.Err = fmt.Errorf("sdk: job %s ended in unexpected state %s", jobID, finalStatus)
		return res, res.Err
	}
}

// submit POSTs the graph to /api/v1/jobs and returns the created jobID.
func (ex *clusterExecutor) submit(ctx context.Context, body submitJobRequest) (string, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("sdk: encode submit body: %w", err)
	}

	url := strings.TrimRight(ex.env.coordinatorURL, "/") + "/api/v1/jobs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("sdk: build submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sdk: submit: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("sdk: submit: %s — %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var created jobStatusResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return "", fmt.Errorf("sdk: decode submit response: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("sdk: submit: server returned empty job ID")
	}
	return created.ID, nil
}

// poll GETs /api/v1/jobs/{jobID} repeatedly until status is terminal or ctx
// is canceled.
func (ex *clusterExecutor) poll(ctx context.Context, jobID string) (string, error) {
	url := strings.TrimRight(ex.env.coordinatorURL, "/") + "/api/v1/jobs/" + jobID

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		status, err := ex.getStatus(ctx, url)
		if err != nil {
			return "", err
		}
		switch strings.ToUpper(status) {
		case "FINISHED", "FAILED", "CANCELED":
			return status, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (ex *clusterExecutor) getStatus(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("sdk: build poll request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sdk: poll: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("sdk: poll: %s — %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var s jobStatusResponse
	if err := json.Unmarshal(body, &s); err != nil {
		return "", fmt.Errorf("sdk: decode poll response: %w", err)
	}
	return s.Status, nil
}
