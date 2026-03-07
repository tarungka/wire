package sdk

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnvironmentDefaults(t *testing.T) {
	env := New()
	if env.parallelism != 1 {
		t.Errorf("expected default parallelism=1, got %d", env.parallelism)
	}
	if env.mode != Embedded {
		t.Errorf("expected default mode=Embedded, got %v", env.mode)
	}
	if env.checkpointTimeout != 10*time.Minute {
		t.Errorf("expected default checkpoint timeout=10m, got %v", env.checkpointTimeout)
	}
}

func TestEnvironmentSetters(t *testing.T) {
	env := New()
	env.SetParallelism(4).
		SetCheckpointInterval(30 * time.Second).
		SetCheckpointTimeout(5 * time.Minute).
		SetRestartStrategy(FixedDelay(3, time.Second)).
		SetMode(Cluster)

	if env.parallelism != 4 {
		t.Errorf("expected parallelism=4, got %d", env.parallelism)
	}
	if env.checkpointInterval != 30*time.Second {
		t.Errorf("expected checkpoint interval=30s, got %v", env.checkpointInterval)
	}
	if env.checkpointTimeout != 5*time.Minute {
		t.Errorf("expected checkpoint timeout=5m, got %v", env.checkpointTimeout)
	}
	if env.restartStrategy.Type != RestartFixedDelay {
		t.Errorf("expected FixedDelay restart, got %v", env.restartStrategy.Type)
	}
	if env.mode != Cluster {
		t.Errorf("expected Cluster mode, got %v", env.mode)
	}
}

func TestExecuteEmptyGraph(t *testing.T) {
	env := New()
	_, err := env.Execute(context.Background())
	if !errors.Is(err, ErrEmptyPipeline) {
		t.Errorf("expected ErrEmptyPipeline, got %v", err)
	}
}

func TestExecuteNoSinks(t *testing.T) {
	env := New()
	env.AddSource(&memorySource{})
	_, err := env.Execute(context.Background())
	if !errors.Is(err, ErrNoSinks) {
		t.Errorf("expected ErrNoSinks, got %v", err)
	}
}

func TestExecuteDoubleExecute(t *testing.T) {
	env := New()
	env.AddSource(&memorySource{events: []Event{{Value: []byte("x")}}}).
		AddSink(&memorySink{})

	_, err := env.Execute(context.Background())
	if err != nil {
		t.Fatalf("first execute failed: %v", err)
	}

	_, err = env.Execute(context.Background())
	if !errors.Is(err, ErrAlreadyExecuted) {
		t.Errorf("expected ErrAlreadyExecuted, got %v", err)
	}
}
