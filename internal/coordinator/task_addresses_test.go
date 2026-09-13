package coordinator

import (
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestTaskAddressResolutionIsAtomic(t *testing.T) {
	c := &Coordinator{workers: map[string]*WorkerMeta{"a": {Address: "a:4001"}, "b": {Address: "b:4001"}}}
	assignments := map[string][]rpc.TaskDescriptor{
		"a": {{TaskID: "source", Downstream: []rpc.DownstreamChannelInfo{{TaskID: "sink"}, {TaskID: "missing"}}}},
		"b": {{TaskID: "sink", Upstream: []rpc.UpstreamChannelInfo{{TaskID: "source"}}}},
	}
	if err := c.attachTaskAddressesLocked(assignments); err == nil {
		t.Fatal("accepted missing task")
	}
	if assignments["a"][0].Downstream[0].Address != "" || assignments["b"][0].Upstream[0].Address != "" {
		t.Fatal("partially mutated failed plan")
	}
	assignments["a"][0].Downstream = assignments["a"][0].Downstream[:1]
	if err := c.attachTaskAddressesLocked(assignments); err != nil {
		t.Fatal(err)
	}
	if assignments["a"][0].Downstream[0].Address != "b:4001" || assignments["b"][0].Upstream[0].Address != "a:4001" {
		t.Fatal("incorrect resolved endpoint")
	}
}
