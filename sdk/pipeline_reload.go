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
	// AllowReplacement enables same-layout savepoint reload. Topology changes
	// remain rejected by preflight without stopping the job.
	AllowReplacement bool
	OnReload         func(PipelineReloadResult, error)
}

// WatchLiveUpdates watches interval edits and optional same-layout replacements.
// The receiver must describe the currently running job and carry its coordinator
// URL/security. Callers must own configuration updates for this job exclusively;
// this method does not reconcile independent external edits. Receiver and
// bindings must not be mutated while watching.
//
// Successful interval updates advance the watcher's private baseline. Invalid
// YAML leaves the job unchanged. By default migration-required edits stop the watcher. AllowReplacement enables
// same-layout savepoint replacement; errors and rollback stop the watcher without
// advancing its baseline. Changed-topology migration and live parallelism remain
// unfinished.
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
			if err := p.updateCheckpointInterval(ctx, jobID, plan.CheckpointInterval, &current.env.checkpointInterval); err != nil {
				return err
			}
		default:
			if !config.AllowReplacement {
				return ErrPipelineMigrationRequired
			}
			candidate.SetCoordinator(p.env.coordinatorURL).SetCoordinatorSecurity(p.env.coordinatorSecurity)
			result, err := candidate.Reload(ctx, jobID)
			if config.OnReload != nil {
				config.OnReload(result, err)
			}
			if err != nil {
				return err
			}
		}
		current = candidate
		if config.OnApplied != nil {
			config.OnApplied(plan)
		}
		return nil
	})
}
