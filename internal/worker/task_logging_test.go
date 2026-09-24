package worker

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/secretconfig"
)

func TestTaskRuntimeUsesProvidedCredentialFilteredLogger(t *testing.T) {
	var output bytes.Buffer
	const secret = "task-private-token"
	log := zerolog.New(secretconfig.NewRedactor([]string{secret}).LogWriter(&output)).With().Str("task_id", "task").Logger()
	running := &atomic.Bool{}
	source := &lifecycleSource{running: running, readErr: errors.New("source rejected " + secret)}
	reg, desc := lifecyclePipeline(source, &lifecycleMap{}, &lifecycleSink{})
	err := newTaskExecutor(reg).run(t.Context(), "job", "task", desc, log, func() { running.Store(true) })
	if err == nil {
		t.Fatal("expected source failure")
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("runtime log leaked credential")
	}
	if !strings.Contains(output.String(), "source rejected [REDACTED]") || !strings.Contains(output.String(), `"task_id":"task"`) {
		t.Fatalf("runtime bypassed task logger: %s", output.String())
	}
}
