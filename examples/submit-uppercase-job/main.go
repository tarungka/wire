// submit-uppercase-job builds a Source -> Map(upper) -> Sink pipeline using
// the SDK and submits it to a Wire coordinator in Cluster mode.
//
// It pairs with the wire-worker-example binary (which must be running and
// connected to the same coordinator) and demonstrates the end-to-end
// submission flow described in Phase 1 of the distributed-execution plan.
//
// Usage:
//
//	go run ./examples/submit-uppercase-job \
//	    --coordinator-url http://127.0.0.1:4001 \
//	    --message hello --message world --message wire
//
// The collected output is printed by the worker process; this binary just
// reports the job's terminal status. (In a real test you would wire up a
// sink that writes to a shared store rather than the in-process MemorySink.)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/sdk"
	"github.com/tarungka/wire/sdk/connectors/memory"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var messages stringList
	coordURL := flag.String("coordinator-url", "http://127.0.0.1:4001", "coordinator HTTP URL")
	jobName := flag.String("name", "uppercase-demo", "job name")
	sinkID := flag.String("sink-id", "uppercase-demo", "sink ID (worker-side memory.Collected key)")
	flag.Var(&messages, "message", "an event the source will emit; repeatable")
	flag.Parse()

	if len(messages) == 0 {
		messages = stringList{"hello", "world", "wire"}
	}

	srcCfg, err := protocol.EncodeMsgPack(memory.SourceConfig{Events: toBytes(messages)})
	check(err)
	sinkCfg, err := protocol.EncodeMsgPack(memory.SinkConfig{SinkID: *sinkID})
	check(err)

	env := sdk.New().
		SetMode(sdk.Cluster).
		SetCoordinator(*coordURL).
		SetParallelism(1)

	env.AddSourceNamed("src", "memory-source", srcCfg).
		MapNamed("upper", "upper", nil).
		AddSinkNamed("sink", "memory-sink", sinkCfg)

	res, err := env.ExecuteWithName(context.Background(), *jobName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "execute: %v\n", err)
		os.Exit(1)
	}
	if res.Err != nil {
		fmt.Fprintf(os.Stderr, "job err: %v\n", res.Err)
		os.Exit(1)
	}
	fmt.Printf("job %s finished in %s\n", res.JobID, res.Metrics.Duration)
	fmt.Println("(captured events live inside the worker process; check its logs or use a real sink for cross-process output)")
}

func toBytes(ss []string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

func check(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
