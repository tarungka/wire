// Command file-connector demonstrates a replayable custom source using public SDK APIs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/tarungka/wire/sdk"
)

type fileConfig struct {
	Path string `json:"path"`
}
type printSink struct{ output io.Writer }

func (*printSink) Open(context.Context) error { return nil }
func (*printSink) Close() error               { return nil }
func (s *printSink) Write(_ context.Context, e sdk.Event) error {
	_, err := fmt.Fprintln(s.output, string(e.Value))
	return err
}

func registry(output io.Writer) *sdk.WorkerRegistry {
	r := sdk.NewWorkerRegistry()
	r.RegisterSource("immutable-lines", func(_ context.Context, data []byte, tc sdk.WorkerTaskContext) (sdk.Source, error) {
		var cfg fileConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
		if cfg.Path == "" || tc.Parallelism != 1 {
			return nil, fmt.Errorf("immutable-lines requires path and parallelism 1")
		}
		return &fileSource{path: cfg.Path}, nil
	})
	r.RegisterSink("print", func(context.Context, []byte, sdk.WorkerTaskContext) (sdk.Sink, error) {
		return &printSink{output: output}, nil
	})
	return r
}
func main() {
	mode := flag.String("mode", "embedded", "embedded, worker, submit or export")
	path := flag.String("file", "", "immutable input file (same content on every worker)")
	rpc := flag.String("rpc", "localhost:4002", "coordinator RPC address")
	http := flag.String("http", "http://localhost:4001", "coordinator HTTP URL")
	workerID := flag.String("worker-id", "file-example", "unique worker ID")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *mode, *path, *rpc, *http, *workerID, os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, mode, path, rpc, http, workerID string, output io.Writer) error {
	if mode == "worker" {
		return sdk.RunWorker(ctx, sdk.WorkerConfig{WorkerID: workerID, CoordinatorAddr: rpc, TaskSlots: 2}, registry(output))
	}
	if path == "" {
		return fmt.Errorf("-file is required")
	}
	env := sdk.New().SetParallelism(1)
	switch mode {
	case "embedded":
		env.AddSource(&fileSource{path: path}).AddSink(&printSink{output: output})
	case "submit", "export":
		cfg, err := json.Marshal(fileConfig{Path: path})
		if err != nil {
			return err
		}
		env.SetMode(sdk.Cluster).SetCoordinator(http)
		env.AddSourceNamed("lines", "immutable-lines", cfg).AddSinkNamed("output", "print", nil)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
	if mode == "export" {
		data, err := env.ExportSubmission("file-connector")
		if err != nil {
			return err
		}
		_, err = output.Write(data)
		return err
	}
	_, err := env.ExecuteWithName(ctx, "file-connector")
	return err
}
