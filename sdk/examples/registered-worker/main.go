// Command registered-worker demonstrates a remotely submitted application using
// only public SDK types. Run -mode worker before -mode submit.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/tarungka/wire/sdk"
)

type source struct{ done bool }

func (*source) Open(context.Context) error { return nil }
func (*source) Close() error               { return nil }
func (*source) GenerateWatermark() int64   { return 0 }
func (s *source) ReadBatch(ctx context.Context) ([]sdk.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.done {
		return nil, nil
	}
	s.done = true
	return []sdk.Event{{Value: []byte("hello")}, {Value: []byte("world")}}, nil
}

type sink struct{ output io.Writer }

func (*sink) Open(context.Context) error { return nil }
func (*sink) Close() error               { return nil }
func (s *sink) Write(_ context.Context, event sdk.Event) error {
	_, err := fmt.Fprintln(s.output, string(event.Value))
	return err
}
func main() {
	mode := flag.String("mode", "worker", "worker or submit")
	rpc := flag.String("rpc", "localhost:4002", "coordinator RPC address")
	http := flag.String("http", "http://localhost:4001", "coordinator HTTP URL")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := run(ctx, *mode, *rpc, *http, os.Stdout)
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, mode, rpc, http string, output io.Writer) error {
	var err error
	switch mode {
	case "worker":
		registry := sdk.NewWorkerRegistry()
		registry.RegisterSource("words", func(context.Context, []byte, sdk.WorkerTaskContext) (sdk.Source, error) { return &source{}, nil })
		registry.RegisterMap("uppercase", func(context.Context, []byte, sdk.WorkerTaskContext) (sdk.MapFunc, error) {
			return func(event sdk.Event) (sdk.Event, error) {
				event.Value = []byte(strings.ToUpper(string(event.Value)))
				return event, nil
			}, nil
		})
		registry.RegisterSink("stdout", func(context.Context, []byte, sdk.WorkerTaskContext) (sdk.Sink, error) {
			return &sink{output: output}, nil
		})
		err = sdk.RunWorker(ctx, sdk.WorkerConfig{WorkerID: "example", CoordinatorAddr: rpc, TaskSlots: 4}, registry)
	case "submit":
		env := sdk.NewStreamExecutionEnvironment().SetMode(sdk.Cluster).SetCoordinator(http).SetParallelism(1)
		env.AddSourceNamed("words", "words", nil).MapNamed("uppercase", "uppercase", nil).AddSinkNamed("stdout", "stdout", nil)
		_, err = env.ExecuteWithName(ctx, "registered-example")
	default:
		err = fmt.Errorf("unknown mode %q", mode)
	}
	return err
}
