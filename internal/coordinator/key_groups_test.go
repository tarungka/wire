package coordinator

import (
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

func TestSubmitGraphKeyGroupsValidatedBeforePersistence(t *testing.T) {
	c, store := newReadyCoordinator(t)
	graph := linearGraph()
	for _, count := range []int{-1, 3, 65536} {
		graph.NumKeyGroups = count
		if _, err := c.SubmitJob("same-name", 1, encode(t, graph)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	graph.NumKeyGroups = 16
	if _, err := c.SubmitJob("same-name", 17, encode(t, graph)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("parallelism: %v", err)
	}
	graph.Operators[0].Parallelism = 17
	if _, err := c.SubmitJob("same-name", 1, encode(t, graph)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("operator parallelism: %v", err)
	}
	if jobs := c.ListJobs(nil); len(jobs) != 0 {
		t.Fatalf("invalid submissions persisted: %v", jobs)
	}
	graph.Operators[0].Parallelism = 0
	job, err := c.SubmitJob("same-name", 3, encode(t, graph))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.Get(JobConfigKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	graph.NumKeyGroups = 0
	if err := protocol.DecodeMsgPack(raw, &graph); err != nil {
		t.Fatal(err)
	}
	if graph.NumKeyGroups != 16 {
		t.Fatalf("persisted count=%d", graph.NumKeyGroups)
	}
}
