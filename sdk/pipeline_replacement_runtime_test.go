package sdk

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestSameJobReplacementRestoresSourceAndNewCode(t *testing.T) {
	for _, scenario := range []string{"success", "rollback", "no-restart"} {
		t.Run(scenario, func(t *testing.T) { testSameJobReplacement(t, scenario != "success", scenario != "no-restart") })
	}
}

func testSameJobReplacement(t *testing.T, fail, recovery bool) {
	release := make(chan struct{})
	restored := make(chan uint64, 4)
	var closed atomic.Int32
	registry := NewWorkerRegistry()
	registry.RegisterSource("replay", func(context.Context, []byte, WorkerTaskContext) (Source, error) {
		return &pauseReplaySource{release: release, restored: restored, closed: &closed}, nil
	})
	for _, version := range []string{"v1", "v2"} {
		registry.RegisterMap(version, func(context.Context, []byte, WorkerTaskContext) (MapFunc, error) {
			if fail && version == "v2" {
				return nil, errors.New("replacement factory unavailable")
			}
			return func(e Event) (Event, error) { e.Value = append([]byte(version+":"), e.Value...); return e, nil }, nil
		})
	}
	output := &collectSink{}
	registry.RegisterSink("output", func(context.Context, []byte, WorkerTaskContext) (Sink, error) {
		return &pipelineRemoteSink{target: output}, nil
	})
	ctx, coord, url := lifecycleCluster(t, registry)
	env := New().SetMode(Cluster).SetCoordinator(url)
	if recovery {
		env.SetRestartStrategy(FixedDelay(3, 0))
	}
	env.AddSourceNamed("source", "replay", nil).MapNamed("map", "v1", nil).AddSinkNamed("sink", "output", nil)
	done := make(chan error, 1)
	go func() { _, err := env.ExecuteWithName(ctx, "replacement-runtime"); done <- err }()
	var jobID string
	lifecycleWait(t, ctx, func() bool {
		jobs := coord.ListJobs(nil)
		if len(jobs) != 1 || jobs[0].Status != coordinator.JobRunning || len(output.Events()) != 1 {
			return false
		}
		jobID = jobs[0].ID
		return true
	})
	sp, err := coord.TriggerSavepoint(jobID)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleWait(t, ctx, func() bool {
		saved, err := coord.GetSavepoint(jobID, sp.ID)
		return err == nil && saved.Status == coordinator.SavepointCompleted
	})
	old, err := coord.GetJob(jobID)
	if err != nil {
		t.Fatal(err)
	}
	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(old.Config, &graph); err != nil {
		t.Fatal(err)
	}
	for i := range graph.Operators {
		if graph.Operators[i].ClassName == "v1" {
			graph.Operators[i].ClassName = "v2"
		}
	}
	config, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coord.ReplaceJobFromSavepoint(jobID, sp.ID, old.Parallelism, config); err != nil {
		t.Fatal(err)
	}
	if !recovery {
		lifecycleWait(t, ctx, func() bool { job, err := coord.GetJob(jobID); return err == nil && job.Status == coordinator.JobFailed })
		job, _ := coord.GetJob(jobID)
		if string(job.Config) != string(old.Config) || job.RescaleFailure == "" {
			t.Fatal("no-restart failure lost original config")
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("failed replacement reported success")
			}
		case <-ctx.Done():
			t.Fatal("job result not delivered")
		}
		if len(output.Events()) != 1 {
			t.Fatal("disabled recovery emitted extra output")
		}
		return
	}
	select {
	case offset := <-restored:
		if offset != 1 {
			t.Fatalf("offset=%d", offset)
		}
	case <-ctx.Done():
		job, _ := coord.GetJob(jobID)
		t.Fatalf("replacement never restored source: %+v", job)
	}
	lifecycleWait(t, ctx, func() bool {
		job, err := coord.GetJob(jobID)
		return err == nil && job.Status == coordinator.JobRunning && job.DeploymentGeneration > old.DeploymentGeneration
	})
	if (!fail && closed.Load() != 1) || closed.Load() < 1 {
		t.Fatalf("old source not closed before replacement: %d", closed.Load())
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("replacement did not finish")
	}
	if fail {
		job, err := coord.GetJob(jobID)
		if err != nil || job.RescaleFailure == "" || string(job.Config) != string(old.Config) {
			t.Fatalf("rollback status=%+v err=%v", job, err)
		}
	}
	expected := "v2:second"
	if fail {
		expected = "v1:second"
	}
	events := output.Events()
	if len(events) != 2 || string(events[0].Value) != "v1:first" || string(events[1].Value) != expected {
		t.Fatalf("output=%v", events)
	}
	if len(coord.ListJobs(nil)) != 1 {
		t.Fatal("replacement created another job")
	}
}
