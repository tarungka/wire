package coordinator

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestDeploymentGenerationPersistsAcrossRecoveryAndRescale(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := slotReleaseJob(t, "job")
	for generation := uint64(1); generation <= 3; generation++ {
		c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1, LastHeartbeat: time.Now()}
		c.jobs[job.ID] = job
		if generation > 1 {
			job.Status = JobFailing
			job.RescaleRequested = generation == 3
		}
		c.scheduleJob(job)
		if job.Status != JobDeploying || job.DeploymentGeneration != generation {
			t.Fatalf("generation %d: %+v", generation, job)
		}
		commands := c.DrainCommands("worker")
		if len(commands) != 1 {
			t.Fatalf("deployment commands: %+v", commands)
		}
		var delivered rpc.TaskDescriptor
		if err := protocol.DecodeMsgPack(commands[0].Data, &delivered); err != nil {
			t.Fatal(err)
		}
		if delivered.DeploymentGeneration != generation {
			t.Fatal("external fence missing from worker deployment")
		}
		data, err := store.Get(JobMetaKey(job.ID))
		if err != nil {
			t.Fatal(err)
		}
		// Subsequent deployment starts from persisted metadata, not the old pointer.
		job = &JobMeta{}
		if err := protocol.DecodeMsgPack(data, job); err != nil {
			t.Fatal(err)
		}
		data, err = store.Get(JobAssignmentsKey(job.ID))
		if err != nil {
			t.Fatal(err)
		}
		var assignment TaskAssignmentMap
		if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
			t.Fatal(err)
		}
		if len(assignment.TaskDescriptors) == 0 {
			t.Fatal("no persisted descriptors")
		}
		for _, task := range assignment.TaskDescriptors {
			if task.DeploymentGeneration != generation {
				t.Fatalf("task fence %d, want %d", task.DeploymentGeneration, generation)
			}
		}
	}
	if job.RestartCount != 1 || job.DeploymentGeneration != 3 {
		t.Fatal("rescale reused recovery counter as external fence")
	}
}
