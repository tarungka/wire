package coordinator

import (
	"reflect"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestJobTransitionsPublishOnlyAfterDurableWrite(t *testing.T) {
	for from, targets := range validTransitions {
		for _, to := range targets {
			t.Run(from.String()+"-"+to.String(), func(t *testing.T) {
				c, store := newTestCoordinator(t)
				now := time.Now().UTC().Add(-time.Hour)
				job := &JobMeta{ID: "job", Name: "reserved", Status: from, CreatedAt: now, UpdatedAt: now, RunningSince: now, RecoveryAttempts: 2, RestartCount: 3, ConsecutiveCheckpointFailures: 4}
				if err := c.persistJob(job); err != nil {
					t.Fatal(err)
				}
				c.activeJobNames[job.Name] = job.ID
				before := *job
				fault := &workerLossStore{MetadataStore: store, failKey: JobMetaKey(job.ID)}
				c.store = fault
				if err := c.transitionJob(job, to); err == nil {
					t.Fatal("injected write failure was hidden")
				}
				if !reflect.DeepEqual(*job, before) || c.jobs[job.ID] != job {
					t.Fatalf("failed transition published state: got %+v want %+v", *job, before)
				}
				if c.activeJobNames[job.Name] != job.ID {
					t.Fatal("failed terminal transition released the job name")
				}
				fault.failKey = nil
				if err := c.transitionJob(job, to); err != nil {
					t.Fatalf("retry: %v", err)
				}
				if job.Status != to || c.jobs[job.ID] != job {
					t.Fatal("successful transition did not update the existing live job")
				}
				data, err := store.Get(JobMetaKey(job.ID))
				if err != nil {
					t.Fatal(err)
				}
				var persisted JobMeta
				if err := protocol.DecodeMsgPack(data, &persisted); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(persisted, *job) {
					t.Fatalf("cache and persisted state differ: %+v / %+v", *job, persisted)
				}
				if _, reserved := c.activeJobNames[job.Name]; reserved == to.IsTerminal() {
					t.Fatalf("name reservation does not match terminal status %s", to)
				}
				if from == JobFailing && to == JobDeploying && (job.RestartCount != before.RestartCount+1 || job.RecoveryAttempts != before.RecoveryAttempts+1) {
					t.Fatal("retry counted the failed persistence as another restart")
				}
			})
		}
	}
}
