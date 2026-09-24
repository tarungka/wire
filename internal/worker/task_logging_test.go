package worker

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
)

func TestTaskRuntimeUsesProvidedCredentialFilteredLogger(t *testing.T) {
	var output bytes.Buffer
	const secret = "task-private-token"
	log := zerolog.New(&output).With().Str("task_id", "task").Logger()
	running := &atomic.Bool{}
	source := &lifecycleSource{running: running, readErr: errors.New("source rejected " + secret)}
	reg, desc := lifecyclePipeline(source, &lifecycleMap{}, &lifecycleSink{})
	desc.SecretValues = []string{secret}
	reg.RegisterSource("logging-source", func(_ context.Context, _ []byte, tc TaskContext) (engine.SourceOperator, error) {
		tc.Log.Info().Str("factory_credential", secret).Msg("factory diagnostic")
		return source, nil
	})
	desc.OperatorChain[0].ClassName = "logging-source"
	err := newTaskExecutor(reg).run(t.Context(), "job", "task", desc, log, func() { running.Store(true) })
	if err == nil {
		t.Fatal("expected source failure")
	}
	if !strings.Contains(output.String(), `"factory_credential":"[REDACTED]"`) {
		t.Fatal("factory did not receive filtered logger")
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("runtime log leaked credential")
	}
	if !strings.Contains(output.String(), "source rejected [REDACTED]") || !strings.Contains(output.String(), `"task_id":"task"`) {
		t.Fatalf("runtime bypassed task logger: %s", output.String())
	}
}
