package sdk

import (
	"context"
	"errors"
	"fmt"
)

// ErrPipelineMigrationRequired means a valid edit cannot be applied through
// the live configuration path. The existing job has not been stopped.
var ErrPipelineMigrationRequired = errors.New("sdk: pipeline edit requires migration")

// PipelineLiveWatchConfig controls live updates of an already running job.
// OnApplied is called serially after a confirmed update (or unchanged candidate).
type PipelineLiveWatchConfig struct {
	PipelineWatchConfig
	OnApplied func(PipelineUpdatePlan)
}

// WatchLiveUpdates watches a named-worker definition for interval-only edits.
// The receiver must describe the currently running job and carry its coordinator
// URL/security. Callers must own configuration updates for this job exclusively;
// this method does not reconcile independent external edits. Receiver and
// bindings must not be mutated while watching.
//
// Successful interval updates advance the watcher's private baseline. Invalid
// YAML leaves the job unchanged. A migration-required edit or uncertain HTTP
// update stops the watcher; it never pauses, cancels or redeploys the job.
// Full savepoint migration and live parallelism are separate unfinished work.
func (p *YAMLPipeline) WatchLiveUpdates(ctx context.Context, path, jobID string, bindings PipelineConnectors, config PipelineLiveWatchConfig) error {
	if p == nil || p.env == nil || p.env.coordinatorURL == "" {
		return fmt.Errorf("%w: live watcher requires a remote pipeline", ErrInvalidConfig)
	}
	current := p
	return WatchPipelineFile(ctx, path, bindings, config.PipelineWatchConfig, func(ctx context.Context, candidate *YAMLPipeline) error {
		plan, err := current.PlanUpdate(candidate)
		if err != nil {
			return err
		}
		switch plan.Kind {
		case PipelineUnchanged:
		case PipelineIntervalUpdate:
			if err := p.UpdateCheckpointInterval(ctx, jobID, plan.CheckpointInterval); err != nil {
				return err
			}
		default:
			return ErrPipelineMigrationRequired
		}
		current = candidate
		if config.OnApplied != nil {
			config.OnApplied(plan)
		}
		return nil
	})
}
