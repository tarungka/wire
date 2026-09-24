package sdk

import (
	"encoding/base64"
	"fmt"
	"reflect"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// PipelineUpdateKind describes the transition required by a validated edit.
type PipelineUpdateKind string

const (
	PipelineUnchanged         PipelineUpdateKind = "unchanged"
	PipelineIntervalUpdate    PipelineUpdateKind = "checkpoint-interval"
	PipelineMigrationRequired PipelineUpdateKind = "migration-required"
)

// PipelineUpdatePlan does not execute changes. MigrationRequired includes
// parallelism, topology, connector, expression and state-layout edits; it does
// not imply the current savepoint restore implementation can accept them.
type PipelineUpdatePlan struct {
	Kind               PipelineUpdateKind
	CheckpointInterval time.Duration
}

// PlanUpdate validates both deployment graphs and identifies interval-only
// changes. It never classifies a change by YAML spelling or by a subset of
// operator fields, so other edits cannot accidentally bypass migration.
func (p *YAMLPipeline) PlanUpdate(next *YAMLPipeline) (PipelineUpdatePlan, error) {
	if p == nil || next == nil || p.env == nil || next.env == nil {
		return PipelineUpdatePlan{}, fmt.Errorf("%w: missing pipeline", ErrInvalidConfig)
	}
	oldRequest, err := p.env.submissionRequest(p.Name)
	if err != nil {
		return PipelineUpdatePlan{}, err
	}
	newRequest, err := next.env.submissionRequest(next.Name)
	if err != nil {
		return PipelineUpdatePlan{}, err
	}
	if !reflect.DeepEqual(p.env.stateBackend, next.env.stateBackend) {
		return PipelineUpdatePlan{Kind: PipelineMigrationRequired}, nil
	}
	if reflect.DeepEqual(oldRequest, newRequest) && reflect.DeepEqual(p.Labels, next.Labels) {
		return PipelineUpdatePlan{Kind: PipelineUnchanged}, nil
	}
	return planValidatedPipelineUpdate(oldRequest, newRequest, p.Labels, next.Labels)
}

func planValidatedPipelineUpdate(oldRequest, newRequest submitJobRequest, oldLabels, newLabels map[string]string) (PipelineUpdatePlan, error) {
	migration := PipelineUpdatePlan{Kind: PipelineMigrationRequired}
	if oldRequest.Name != newRequest.Name || oldRequest.Parallelism != newRequest.Parallelism || oldRequest.Config != newRequest.Config || !reflect.DeepEqual(oldLabels, newLabels) {
		return migration, nil
	}
	decode := func(encoded string) (rpc.JobGraph, error) {
		var graph rpc.JobGraph
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return graph, err
		}
		err = protocol.DecodeMsgPack(data, &graph)
		return graph, err
	}
	oldGraph, err := decode(oldRequest.GraphBytes)
	if err != nil {
		return PipelineUpdatePlan{}, err
	}
	newGraph, err := decode(newRequest.GraphBytes)
	if err != nil {
		return PipelineUpdatePlan{}, err
	}
	if oldGraph.CheckpointPolicy == nil || newGraph.CheckpointPolicy == nil {
		return migration, nil
	}
	interval := newGraph.CheckpointPolicy.Interval
	oldGraph.CheckpointPolicy.Interval = 0
	newGraph.CheckpointPolicy.Interval = 0
	if reflect.DeepEqual(oldGraph, newGraph) {
		return PipelineUpdatePlan{Kind: PipelineIntervalUpdate, CheckpointInterval: interval}, nil
	}
	return migration, nil
}
