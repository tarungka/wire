package coordinator

import (
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestReplacementPreflightRejectsLayoutBeforeStopping(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	job := c.jobs["job"]
	job.Parallelism = 2
	job.Config = encode(t, linearGraph())
	assignments, _ := store.Get(JobAssignmentsKey(job.ID))
	for _, tc := range []struct {
		name        string
		parallelism int
		change      func(*rpc.JobGraph)
		valid       bool
	}{
		{"same", 2, func(*rpc.JobGraph) {}, true},
		{"code", 2, func(g *rpc.JobGraph) { g.Operators[0].ClassName = "new-source-code" }, true},
		{"parallelism", 3, func(*rpc.JobGraph) {}, false},
		{"identity", 2, func(g *rpc.JobGraph) { g.Operators[0].OperatorID = "different" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graph := linearGraph()
			tc.change(&graph)
			err := c.ValidateReplacementLayout(job.ID, tc.parallelism, encode(t, graph))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if job.Status != JobRunning || job.PauseSavepointID != "" || job.UpgradeSuccessorID != "" {
				t.Fatal("preflight mutated predecessor")
			}
			after, _ := store.Get(JobAssignmentsKey(job.ID))
			if string(after) != string(assignments) || len(c.DrainCommands("worker")) != 0 {
				t.Fatal("preflight changed assignment or sent commands")
			}
		})
	}
}
