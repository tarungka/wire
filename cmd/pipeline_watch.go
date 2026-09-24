package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/tarungka/wire/internal/apiclient"
	"github.com/tarungka/wire/sdk"
)

// runPipelineWatch attaches to a job already running the supplied definition.
// Submission is separate so watching cannot accidentally create duplicate jobs.
func runPipelineWatch(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := pflag.NewFlagSet("wire jobs watch", pflag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("file", "", "current YAML pipeline definition to watch")
	endpoint := flags.String("coordinator", "http://localhost:4001", "coordinator HTTP URL")
	interval := flags.Duration("poll-interval", 250*time.Millisecond, "file polling interval")
	replacement := flags.Bool("allow-replacement", false, "allow same-layout savepoint replacement")
	var security sdk.CoordinatorSecurity
	flags.StringVar(&security.CACert, "ca-cert", "", "coordinator HTTPS CA certificate")
	flags.StringVar(&security.ClientCert, "client-cert", "", "HTTPS client certificate")
	flags.StringVar(&security.ClientKey, "client-key", "", "HTTPS client private key")
	flags.StringVar(&security.APIKeyFile, "api-key-file", "", "coordinator API key file")
	flags.StringVar(&security.Username, "username", "", "Basic authentication username")
	flags.StringVar(&security.PasswordFile, "password-file", "", "Basic authentication password file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 1 || *path == "" || *interval <= 0 {
		return fmt.Errorf("usage: wire jobs watch job-id --file pipeline.yaml [--allow-replacement]")
	}
	jobID := flags.Arg(0)
	if jobID == "" || jobID == "." || jobID == ".." || strings.ContainsAny(jobID, "/\\") {
		return fmt.Errorf("invalid job identifier")
	}
	client, err := apiclient.New(*endpoint, apiclient.Config(security), 30*time.Second)
	if err != nil {
		return err
	}
	client.CloseIdleConnections()
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > 4<<20 {
		return fmt.Errorf("pipeline file exceeds 4 MiB")
	}
	bindings := sdk.PipelineConnectors{NamedSources: map[string]string{"http-api": "http-api.yaml.v1"}, NamedSinks: map[string]string{"http-api": "http-api.yaml.v1"}}
	pipeline, err := sdk.ParsePipelineYAML(data, bindings)
	if err != nil {
		return err
	}
	pipeline.SetCoordinator(*endpoint).SetCoordinatorSecurity(security)
	encoder := json.NewEncoder(out)
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var outputErr error
	emit := func(value any) {
		if outputErr == nil {
			outputErr = encoder.Encode(value)
			if outputErr != nil {
				cancel()
			}
		}
	}
	err = pipeline.WatchLiveUpdates(watchCtx, *path, flags.Arg(0), bindings, sdk.PipelineLiveWatchConfig{
		PipelineWatchConfig: sdk.PipelineWatchConfig{PollInterval: *interval, OnRejected: func(err error) { _, _ = fmt.Fprintln(errOut, "pipeline edit rejected:", err) }},
		AllowReplacement:    *replacement,
		OnApplied:           func(plan sdk.PipelineUpdatePlan) { emit(map[string]any{"event": "applied", "kind": plan.Kind}) },
		OnReload: func(result sdk.PipelineReloadResult, err error) {
			event := map[string]any{"event": "reload", "job_id": result.JobID, "savepoint_id": result.SavepointID, "rolled_back": result.RolledBack}
			if err != nil {
				event["error"] = err.Error()
			}
			emit(event)
		},
	})
	if outputErr != nil {
		return outputErr
	}
	return err
}
