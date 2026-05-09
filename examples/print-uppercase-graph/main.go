// print-uppercase-graph builds the same Source -> Map(upper) -> Sink graph as
// submit-uppercase-job and prints its base64-encoded msgpack bytes to stdout.
//
// The output is the value the coordinator's POST /api/v1/jobs endpoint expects
// in the "graph_bytes" field — useful for load tools (k6, vegeta) that need
// a static blob to replay rather than rebuilding the graph in JS/JSON.
package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/sdk/connectors/memory"
)

func main() {
	srcCfg, err := protocol.EncodeMsgPack(memory.SourceConfig{
		Events: [][]byte{[]byte("hello"), []byte("world"), []byte("wire")},
	})
	check(err)
	sinkCfg, err := protocol.EncodeMsgPack(memory.SinkConfig{SinkID: "loadtest"})
	check(err)

	graph := rpc.JobGraph{
		Operators: []rpc.OperatorDescriptor{
			{OperatorID: "src", Name: "src", Type: rpc.OperatorTypeSource, Parallelism: 1, ClassName: "memory-source", Config: srcCfg},
			{OperatorID: "upper", Name: "upper", Type: rpc.OperatorTypeMap, Parallelism: 1, ClassName: "upper"},
			{OperatorID: "sink", Name: "sink", Type: rpc.OperatorTypeSink, Parallelism: 1, ClassName: "memory-sink", Config: sinkCfg},
		},
		Edges: []rpc.EdgeDescriptor{
			{SourceOperatorID: "src", TargetOperatorID: "upper", Shuffle: rpc.ShuffleStrategyForward},
			{SourceOperatorID: "upper", TargetOperatorID: "sink", Shuffle: rpc.ShuffleStrategyForward},
		},
	}
	data, err := protocol.EncodeMsgPack(&graph)
	check(err)
	fmt.Println(base64.StdEncoding.EncodeToString(data))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
