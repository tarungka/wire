package sdk

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/apiclient"
)

var ErrPipelineReplacementRolledBack = errors.New("sdk: pipeline replacement rolled back")

// PipelineReloadResult preserves savepoint and request identities for reconciliation,
// including when a later mutation fails or its response is uncertain.
type PipelineReloadResult struct {
	JobID, SavepointID   string
	ReplacementRequestID string
	RolledBack           bool
}

// Reload preflights a same-layout candidate, takes a savepoint, requests fenced
// replacement, and waits for RUNNING/FINISHED or failure/rollback. The caller
// must exclusively own job configuration changes and bound the operation with
// ctx. Mutations are sent once. A lost replacement reply is reconciled only if
// job status confirms its persisted request ID; other errors may require manual
// reconciliation. Retained
// savepoints are not deleted automatically. Changed topology is not supported.
func (p *YAMLPipeline) Reload(ctx context.Context, jobID string) (PipelineReloadResult, error) {
	result := PipelineReloadResult{JobID: jobID}
	if err := p.ValidateReplacement(ctx, jobID); err != nil {
		return result, err
	}
	client, err := apiclient.New(p.env.coordinatorURL, apiclient.Config(p.env.coordinatorSecurity), 30*time.Second)
	if err != nil {
		return result, err
	}
	defer client.CloseIdleConnections()
	base := strings.TrimRight(p.env.coordinatorURL, "/") + "/api/v1/jobs/" + url.PathEscape(jobID)
	requestJSON := func(method, target string, status int, out any) error {
		request, err := http.NewRequestWithContext(ctx, method, target, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != status {
			return fmt.Errorf("sdk: reload %s: HTTP %d", method, response.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out)
	}
	var saved struct {
		ID     string `json:"id"`
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := requestJSON(http.MethodPost, base+"/savepoints", http.StatusAccepted, &saved); err != nil {
		return result, err
	}
	if saved.ID == "" || saved.JobID != jobID {
		return result, fmt.Errorf("sdk: invalid reload savepoint identity")
	}
	result.SavepointID = saved.ID
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	wait := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			return nil
		}
	}
	for saved.Status != "COMPLETED" {
		if saved.Status == "FAILED" {
			return result, fmt.Errorf("sdk: reload savepoint failed")
		}
		if saved.Status != "IN_PROGRESS" {
			return result, fmt.Errorf("sdk: unexpected savepoint status %q", saved.Status)
		}
		if err := wait(); err != nil {
			return result, err
		}
		if err := requestJSON(http.MethodGet, base+"/savepoints/"+url.PathEscape(result.SavepointID), http.StatusOK, &saved); err != nil {
			return result, err
		}
		if saved.ID != result.SavepointID || saved.JobID != jobID {
			return result, fmt.Errorf("sdk: reload savepoint identity changed")
		}
	}
	result.ReplacementRequestID = rand.Text()
	// Send the mutation once. An error may mean the coordinator accepted it
	// but the reply was lost; only the persisted correlation ID proves that.
	replaceErr := p.replaceFromSavepoint(ctx, jobID, result.SavepointID, result.ReplacementRequestID)
	for {
		var job struct {
			ID        string `json:"id"`
			RequestID string `json:"replacement_request_id"`
			Status    string `json:"status"`
			Failure   string `json:"rescale_failure"`
		}
		if err := requestJSON(http.MethodGet, base, http.StatusOK, &job); err != nil {
			return result, errors.Join(replaceErr, err)
		}
		if job.ID != jobID {
			return result, fmt.Errorf("sdk: reload job identity changed")
		}
		if job.RequestID != result.ReplacementRequestID {
			return result, errors.Join(replaceErr, fmt.Errorf("sdk: replacement request not confirmed; reconcile job before resubmitting"))
		}
		if job.Failure != "" {
			result.RolledBack = true
			return result, ErrPipelineReplacementRolledBack
		}
		switch job.Status {
		case "RUNNING", "FINISHED":
			return result, nil
		case "FAILED", "CANCELED":
			return result, fmt.Errorf("sdk: replacement ended %s", job.Status)
		case "FAILING", "DEPLOYING", "FINISHING":
		default:
			return result, fmt.Errorf("sdk: unexpected replacement status %q", job.Status)
		}
		if err := wait(); err != nil {
			return result, err
		}
	}
}
