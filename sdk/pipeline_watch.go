package sdk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

const maxPipelineFileBytes = 4 << 20

// PipelineWatchConfig controls candidate detection. PollInterval defaults to
// 250ms. A changed file must have identical bytes on two consecutive polls.
// OnRejected receives read/validation errors after the initial application;
// invalid candidates never reach Apply. Callbacks run serially on the watcher.
type PipelineWatchConfig struct {
	PollInterval time.Duration
	OnRejected   func(error)
}

// WatchPipelineFile compiles named-worker YAML and calls apply for the initial
// definition and each stable, distinct valid edit. It does not itself stop jobs,
// migrate state or submit replacements: apply must implement that protocol.
// An apply error terminates the watcher, because retrying an ambiguous mutation
// could create duplicate jobs. Validation/read errors after startup leave the
// current job untouched and keep watching. Cancellation returns ctx.Err().
// Only named connector bindings are allowed, so validation cannot create local
// connector instances. The caller must not mutate bindings while watching.
func WatchPipelineFile(ctx context.Context, path string, bindings PipelineConnectors, config PipelineWatchConfig, apply func(context.Context, *YAMLPipeline) error) error {
	if apply == nil || path == "" || config.PollInterval < 0 {
		return fmt.Errorf("%w: invalid pipeline watcher configuration", ErrInvalidConfig)
	}
	if len(bindings.Sources)+len(bindings.Sinks)+len(bindings.SourceInstances)+len(bindings.SinkInstances) != 0 {
		return fmt.Errorf("%w: pipeline watcher requires named worker bindings", ErrInvalidConfig)
	}
	interval := config.PollInterval
	if interval == 0 {
		interval = 250 * time.Millisecond
	}
	compile := func(data []byte) (*YAMLPipeline, error) {
		pipeline, err := ParsePipelineYAML(data, bindings)
		if err != nil {
			return nil, err
		}
		// Validate the complete deployment graph, including limits that the parser
		// alone cannot prove. Export has no network or connector side effects.
		if _, err := pipeline.ExportSubmission(); err != nil {
			return nil, err
		}
		return pipeline, nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	initial, err := readPipelineFile(path)
	if err != nil {
		return err
	}
	pipeline, err := compile(initial)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := apply(ctx, pipeline); err != nil {
		return err
	}
	accepted := initial
	var pending, rejected []byte
	var readError string
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		data, err := readPipelineFile(path)
		if err != nil {
			pending = nil
			if config.OnRejected != nil && err.Error() != readError {
				config.OnRejected(err)
			}
			readError = err.Error()
			continue
		}
		readError = ""
		if bytes.Equal(data, accepted) {
			pending = nil
			rejected = nil
			continue
		}
		if bytes.Equal(data, rejected) {
			continue
		}
		if !bytes.Equal(data, pending) {
			pending = data
			continue
		}
		candidate, err := compile(data)
		if err != nil {
			rejected = data
			pending = nil
			if config.OnRejected != nil {
				config.OnRejected(err)
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := apply(ctx, candidate); err != nil {
			return err
		}
		accepted = data
		pending, rejected = nil, nil
	}
}

func readPipelineFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: pipeline file must be regular", ErrInvalidConfig)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPipelineFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPipelineFileBytes {
		return nil, fmt.Errorf("%w: pipeline file exceeds 4 MiB", ErrInvalidConfig)
	}
	return data, nil
}
