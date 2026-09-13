package coordinator

import (
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestRescaleDeploymentRequiresCompletedMatchingSavepoint(t *testing.T) {
	for _, mode := range []string{"valid", "recovered", "periodic", "in-progress", "wrong-epoch", "wrong-job", "future-checkpoint", "missing-topology"} {
		t.Run(mode, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			old, err := buildPhysicalTasks("job", linearGraph(), 4)
			if err != nil {
				t.Fatal(err)
			}
			targets, err := buildPhysicalTasks("job", linearGraph(), 3)
			if err != nil {
				t.Fatal(err)
			}
			cp := CheckpointMeta{ID: 7, EpochID: 2, JobID: "job", Status: CheckpointCompleted, NumKeyGroups: 128, SavepointID: "save", TaskDescriptors: old, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
			for _, task := range old {
				cp.Tasks[task.TaskID] = "old-worker"
				cp.Replicas[task.TaskID] = "replica"
				cp.StatePaths[task.TaskID] = "replica"
			}
			sp := SavepointMeta{ID: "save", JobID: "job", CheckpointID: 7, EpochID: 2, NumKeyGroups: 128, Status: SavepointCompleted}
			job := &JobMeta{ID: "job", LatestCheckpoint: 7, RescaleCheckpoint: 7}
			switch mode {
			case "periodic":
				cp.SavepointID = ""
			case "in-progress":
				sp.Status = SavepointInProgress
			case "wrong-epoch":
				sp.EpochID++
			case "wrong-job":
				sp.JobID = "other"
			case "future-checkpoint":
				job.RescaleCheckpoint++
			case "missing-topology":
				cp.TaskDescriptors = nil
			}
			if err := store.Set(CheckpointKey("job", 7), encode(t, cp)); err != nil {
				t.Fatal(err)
			}
			if err := store.Set(SavepointKey("job", "save"), encode(t, sp)); err != nil {
				t.Fatal(err)
			}
			if mode == "recovered" {
				job.Status = JobFailing
				if err := store.Set(JobMetaKey(job.ID), encode(t, job)); err != nil {
					t.Fatal(err)
				}
				oldEpoch := c.epoch
				if err := c.recover(); err != nil {
					t.Fatal(err)
				}
				job = c.jobs["job"]
				if job == nil || job.RescaleCheckpoint != 7 || job.LatestCheckpoint != 7 || job.Status != JobFailing {
					t.Fatalf("lost pending rescale: %+v", job)
				}
				if c.epoch < oldEpoch {
					t.Fatal("recovery regressed deployment epoch")
				}
			}
			assignments := map[string][]rpc.TaskDescriptor{"new-worker": targets}
			err = c.attachCheckpointRestoreLocked(job, assignments)
			if mode != "valid" && mode != "recovered" {
				if err == nil {
					t.Fatal("invalid savepoint accepted")
				}
				for _, task := range targets {
					if task.RestoreCheckpoint != nil || task.RestoreRescale != nil {
						t.Fatal("partial restore descriptors published")
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, task := range targets {
				restore := task.RestoreRescale
				if task.RestoreCheckpoint != nil || restore == nil || restore.CheckpointID != 7 || restore.EpochID != 2 || restore.NumKeyGroups != 128 || len(restore.Parts) == 0 {
					t.Fatalf("wrong restore: %+v", task)
				}
				next := task.KeyGroup.Start
				for _, part := range restore.Parts {
					if part.Groups.Start != next {
						t.Fatal("range gap")
					}
					next = part.Groups.End + 1
				}
				if next != task.KeyGroup.End+1 {
					t.Fatal("incomplete restore")
				}
			}
		})
	}
}
