package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestSubmitRejectsInvalidRestartPolicy(t *testing.T) {
	c, _ := newTestCoordinator(t)
	graph := linearGraph()
	graph.RestartPolicy = &rpc.RestartPolicy{Type: rpc.RestartStrategyExponentialBackoff, Delay: time.Minute, MaxDelay: time.Second, Multiplier: 2}
	if _, err := c.SubmitJob("invalid", 1, encode(t, graph)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid policy accepted: %v", err)
	}
	if len(c.jobs) != 0 {
		t.Fatal("invalid submission reserved metadata")
	}
}

func TestExplicitRestartPolicyBudgetAndFirstDelay(t *testing.T) {
	for _, kind := range []rpc.RestartStrategyType{rpc.RestartStrategyNoRestart, rpc.RestartStrategyFixedDelay, rpc.RestartStrategyExponentialBackoff} {
		t.Run(kind.String(), func(t *testing.T) {
			c, store := newTestCoordinator(t)
			policy := &rpc.RestartPolicy{Type: kind, MaxAttempts: 2, Delay: time.Hour, MaxDelay: 4 * time.Hour, Multiplier: 2}
			if kind == rpc.RestartStrategyNoRestart {
				policy.MaxAttempts = 0
			}
			graph := linearGraph()
			graph.RestartPolicy = policy
			job, err := c.SubmitJob("restart", 1, encode(t, graph))
			if err != nil {
				t.Fatal(err)
			}
			// Read the durable policy, then use it for actual restart admission.
			raw, err := store.Get(JobMetaKey(job.ID))
			if err != nil {
				t.Fatal(err)
			}
			if err := protocol.DecodeMsgPack(raw, job); err != nil {
				t.Fatal(err)
			}
			if job.RestartPolicy == nil || *job.RestartPolicy != *policy {
				t.Fatal("policy not persisted")
			}
			job.Status = JobFailing
			job.UpdatedAt = time.Now()
			c.jobs[job.ID] = job
			if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID})); err != nil {
				t.Fatal(err)
			}
			if c.prepareTaskRestart(job) {
				t.Fatal("first retry skipped policy")
			}
			if kind == rpc.RestartStrategyNoRestart {
				if job.Status != JobFailed {
					t.Fatal("no-restart job not terminal")
				}
				return
			}
			job.UpdatedAt = time.Now().Add(-time.Hour - time.Second)
			if !c.prepareTaskRestart(job) {
				t.Fatal("first retry blocked after delay")
			}
			job.RecoveryAttempts = 1
			if got := c.prepareTaskRestart(job); got != (kind == rpc.RestartStrategyFixedDelay) {
				t.Fatal("second retry did not use configured delay")
			}
			job.RecoveryAttempts = 2
			if c.prepareTaskRestart(job) || job.Status != JobFailed {
				t.Fatal("budget not enforced")
			}
		})
	}
}

func TestExplicitRestartBudgetSurvivesStableRunning(t *testing.T) {
	c, _ := newTestCoordinator(t)
	job := &JobMeta{Status: JobRunning, RunningSince: time.Now().Add(-24 * time.Hour), RecoveryAttempts: 2, RestartPolicy: &rpc.RestartPolicy{Type: rpc.RestartStrategyFixedDelay, MaxAttempts: 3}}
	c.resetStableRecoveryBudget(job, time.Now())
	if job.RecoveryAttempts != 2 {
		t.Fatal("explicit job budget reset by coordinator default")
	}
}
