package coordinator

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestTransactionLineageComposesAcrossTaskRenames(t *testing.T) {
	legacy := &JobMeta{ID: "root"}
	first := &JobMeta{ID: "next", TransactionJobID: "root", TransactionTaskIDs: remapTransactionIdentities(legacy, map[string]string{"next/new-head/0": "root/old-head/0"})}
	second := &JobMeta{ID: "last", TransactionJobID: "root", TransactionTaskIDs: remapTransactionIdentities(first, map[string]string{"last/newer-head/0": "next/new-head/0"})}
	encoded := encode(t, second)
	var recovered JobMeta
	if err := protocol.DecodeMsgPack(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	job, task := transactionIdentity(&recovered, "last/newer-head/0")
	if job != "root" || task != "root/old-head/0" {
		t.Fatalf("namespace moved: %s %s", job, task)
	}
	first.TransactionTaskIDs["next/new-head/0"] = "mutated"
	if second.TransactionTaskIDs["last/newer-head/0"] != "root/old-head/0" {
		t.Fatal("lineage map aliases predecessor")
	}
}

func TestUpgradeRetainsTaskLineageAfterCoordinatorRecovery(t *testing.T) {
	c, _, source, path := stoppedUpgradeSource(t)
	tasks, err := generateTaskDescriptors(source)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	source.TransactionJobID = "root"
	source.TransactionTaskIDs = map[string]string{tasks[0].TaskID: "root/original-head/0"}
	if err := c.persistJobLocked(source); err != nil {
		t.Fatal(err)
	}
	next, err := c.SubmitJobFromSavepoint("successor", 1, source.Config, path)
	if err != nil {
		t.Fatal(err)
	}
	nextTasks, err := generateTaskDescriptors(next)
	if err != nil {
		t.Fatal(err)
	}
	c.epoch++
	if err := c.recover(); err != nil {
		t.Fatal(err)
	}
	recovered, err := c.GetJob(next.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, task := transactionIdentity(recovered, nextTasks[0].TaskID)
	if job != "root" || task != "root/original-head/0" {
		t.Fatalf("upgrade reset lineage: %s %s", job, task)
	}
	recovered.TransactionTaskIDs[nextTasks[0].TaskID] = "mutated"
	listed := c.ListJobs(nil)
	for _, item := range listed {
		if item.ID == next.ID {
			item.TransactionTaskIDs[nextTasks[0].TaskID] = "also-mutated"
		}
	}
	snapshot, err := c.GetJob(next.ID)
	if err != nil || snapshot.TransactionTaskIDs[nextTasks[0].TaskID] != "root/original-head/0" {
		t.Fatalf("read-only snapshot leaked mutable lineage: %+v %v", snapshot, err)
	}
}

func TestDeploymentCarriesPersistedTransactionTaskIdentity(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := slotReleaseJob(t, "job")
	tasks, err := generateTaskDescriptors(job)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%v error=%v", tasks, err)
	}
	job.TransactionJobID = "root"
	job.TransactionTaskIDs = map[string]string{tasks[0].TaskID: "root/old-head/0"}
	for generation := uint64(1); generation <= 2; generation++ {
		c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1, LastHeartbeat: time.Now()}
		c.jobs[job.ID] = job
		if generation > 1 {
			job.Status = JobFailing
		}
		c.scheduleJob(job)
		commands := c.DrainCommands("worker")
		if len(commands) != 1 {
			t.Fatalf("commands=%+v", commands)
		}
		var delivered rpc.TaskDescriptor
		if err := protocol.DecodeMsgPack(commands[0].Data, &delivered); err != nil {
			t.Fatal(err)
		}
		if delivered.TransactionJobID != "root" || delivered.TransactionTaskID != "root/old-head/0" || delivered.DeploymentGeneration != generation {
			t.Fatalf("sink authority changed: %+v", delivered)
		}
		data, err := store.Get(JobMetaKey(job.ID))
		if err != nil {
			t.Fatal(err)
		}
		job = &JobMeta{}
		if err := protocol.DecodeMsgPack(data, job); err != nil {
			t.Fatal(err)
		}
	}
}
