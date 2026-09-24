package worker

import (
	"context"
	"fmt"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

type registeredTaskContextKey struct{}

func channelTaskID(jobID, taskID, operatorID string, subtask int32) string {
	if taskID != "" {
		return taskID
	}
	return fmt.Sprintf("%s/%s/%d", jobID, operatorID, subtask)
}

// connectTaskStreams owns registration and stream cleanup for one deployment.
// Descriptor order fixes input indices independently of connection arrival order.
func connectTaskStreams(ctx context.Context, mux *transport.Mux, jobID, taskID string, desc rpc.TaskDescriptor) (inputs, outputs []*transport.FrameStream, cleanup func(), err error) {
	cleanup = func() {}
	if len(desc.Upstream) == 0 && len(desc.Downstream) == 0 {
		return
	}
	if mux == nil {
		err = fmt.Errorf("worker: network channels require a data transport")
		return
	}
	registered := false
	cleanup = func() {
		if registered {
			mux.UnregisterTask(taskID)
		}
		for _, stream := range inputs {
			if stream != nil {
				_ = stream.Close()
			}
		}
		for _, stream := range outputs {
			_ = stream.Close()
		}
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	type inputKey struct {
		source    string
		partition uint16
	}
	expected := make(map[inputKey]int, len(desc.Upstream))
	for index, upstream := range desc.Upstream {
		key := inputKey{channelTaskID(jobID, upstream.TaskID, upstream.OperatorID, upstream.SubtaskIndex), upstream.PartitionIndex}
		if _, duplicate := expected[key]; duplicate {
			err = fmt.Errorf("worker: duplicate upstream channel %q", key.source)
			return
		}
		expected[key] = index
	}
	if len(desc.Upstream) > 0 && ctx.Value(registeredTaskContextKey{}) != taskID {
		if err = registerTaskSources(mux, jobID, taskID, desc); err != nil {
			return
		}
		registered = true
	}
	for _, downstream := range desc.Downstream {
		target := channelTaskID(jobID, downstream.TaskID, downstream.OperatorID, downstream.SubtaskIndex)
		if downstream.Address == "" {
			err = fmt.Errorf("worker: downstream task %q has no address", target)
			return
		}
		var stream *transport.FrameStream
		stream, err = mux.Dial(ctx, downstream.Address, protocol.StreamHeaderMsg{SourceTaskID: taskID, TargetTaskID: target, PartitionIndex: downstream.PartitionIndex})
		if err != nil {
			return
		}
		outputs = append(outputs, stream)
	}
	inputs = make([]*transport.FrameStream, len(desc.Upstream))
	for remaining := len(expected); remaining > 0; {
		var stream *transport.FrameStream
		stream, err = mux.AcceptTask(ctx, taskID)
		if err != nil {
			return
		}
		header, _ := stream.Header()
		key := inputKey{header.SourceTaskID, header.PartitionIndex}
		index, ok := expected[key]
		if !ok || inputs[index] != nil {
			_ = stream.Close()
			err = fmt.Errorf("worker: unexpected or duplicate upstream %q partition %d", header.SourceTaskID, header.PartitionIndex)
			return
		}
		inputs[index] = stream
		remaining--
	}
	return
}

// Register the coordinator's source ownership before restore can queue inputs.
func registerTaskSources(mux *transport.Mux, jobID, taskID string, desc rpc.TaskDescriptor) error {
	sources := make([]transport.TaskSource, 0, len(desc.Upstream))
	for _, upstream := range desc.Upstream {
		sources = append(sources, transport.TaskSource{TaskID: channelTaskID(jobID, upstream.TaskID, upstream.OperatorID, upstream.SubtaskIndex), WorkerID: upstream.WorkerID, PartitionIndex: upstream.PartitionIndex})
	}
	return mux.RegisterTaskSources(taskID, sources)
}
