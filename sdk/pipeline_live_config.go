package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/apiclient"
)

// UpdateCheckpointInterval changes a running remote job's periodic checkpoint
// schedule without redeployment. It does not retry a failed/ambiguous request.
// The pipeline must have SetCoordinator configured; its security settings apply.
// Zero disables periodic triggers. Manual checkpoints remain available.
func (p *YAMLPipeline) UpdateCheckpointInterval(ctx context.Context, jobID string, interval time.Duration) error {
	if interval < 0 || jobID == "" || jobID == "." || jobID == ".." || strings.ContainsAny(jobID, "/\\") {
		return fmt.Errorf("%w: invalid job ID or checkpoint interval", ErrInvalidConfig)
	}
	client, err := apiclient.New(p.env.coordinatorURL, apiclient.Config(p.env.coordinatorSecurity), 30*time.Second)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	data, err := json.Marshal(struct {
		Interval string `json:"interval"`
	}{interval.String()})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(p.env.coordinatorURL, "/")+"/api/v1/jobs/"+url.PathEscape(jobID)+"/checkpoint-interval", bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("sdk: checkpoint interval update: HTTP %d", response.StatusCode)
	}
	var result struct {
		ID       string `json:"id"`
		Interval string `json:"checkpoint_interval"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("sdk: invalid checkpoint interval response: %w", err)
	}
	if result.ID != jobID || result.Interval != interval.String() {
		return fmt.Errorf("sdk: checkpoint interval response does not confirm requested update")
	}
	return nil
}

// ValidateReplacement checks this candidate's physical layout against a running
// remote job without stopping it. Success does not certify archive availability
// or application state compatibility; actual restore repeats its own checks.
func (p *YAMLPipeline) ValidateReplacement(ctx context.Context, jobID string) error {
	if jobID == "" || jobID == "." || jobID == ".." || strings.ContainsAny(jobID, "/\\") {
		return fmt.Errorf("%w: invalid job ID", ErrInvalidConfig)
	}
	data, err := p.ExportSubmission()
	if err != nil {
		return err
	}
	client, err := apiclient.New(p.env.coordinatorURL, apiclient.Config(p.env.coordinatorSecurity), 30*time.Second)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.env.coordinatorURL, "/")+"/api/v1/jobs/"+url.PathEscape(jobID)+"/replacement/validate", bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("sdk: replacement layout preflight: HTTP %d", response.StatusCode)
	}
	return nil
}

// ReplaceFromSavepoint requests same-layout replacement under an existing job
// identity. A nil error means accepted, not finished. Inspect job status and
// rescale_failure to distinguish deployment success from rollback. Mutations
// are never retried; after an uncertain response reconcile before resubmitting.
func (p *YAMLPipeline) ReplaceFromSavepoint(ctx context.Context, jobID, savepointID string) error {
	if jobID == "" || jobID == "." || jobID == ".." || strings.ContainsAny(jobID, "/\\") || savepointID == "" {
		return fmt.Errorf("%w: invalid job or savepoint ID", ErrInvalidConfig)
	}
	submission, err := p.env.submissionRequest(p.Name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Name        string `json:"name"`
		Parallelism int    `json:"parallelism"`
		GraphBytes  string `json:"graph_bytes"`
		SavepointID string `json:"savepoint_id"`
	}{submission.Name, submission.Parallelism, submission.GraphBytes, savepointID})
	if err != nil {
		return err
	}
	client, err := apiclient.New(p.env.coordinatorURL, apiclient.Config(p.env.coordinatorSecurity), 30*time.Second)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.env.coordinatorURL, "/")+"/api/v1/jobs/"+url.PathEscape(jobID)+"/replacement", bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("sdk: replacement request: HTTP %d", response.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("sdk: invalid replacement response: %w", err)
	}
	if result.ID != jobID {
		return fmt.Errorf("sdk: replacement response identifies another job")
	}
	return nil
}
