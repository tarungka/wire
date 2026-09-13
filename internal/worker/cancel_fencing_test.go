package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
)

func TestCancelTaskFencesExecution(t *testing.T) {
	for _, mode := range []string{"valid", "old-attempt", "old-epoch", "wrong-job"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := &Worker{log: zerolog.Nop(), tasks: map[string]*taskHandle{"task": {cancel: cancel, jobID: "job", epoch: 5, attemptID: "current"}}}
			command := rpc.WorkerCommand{Type: rpc.CommandTypeCancelTask, JobID: "job", TaskID: "task", EpochID: 5, AttemptID: "current"}
			switch mode {
			case "old-attempt":
				command.AttemptID = "old"
			case "old-epoch":
				command.EpochID--
			case "wrong-job":
				command.JobID = "other"
			}
			w.handleCommands([]rpc.WorkerCommand{command})
			if (ctx.Err() != nil) != (mode == "valid") {
				t.Fatalf("cancellation result: %v", ctx.Err())
			}
			if len(w.tasks) != 1 {
				t.Fatal("cancel removed task before it joined")
			}
		})
	}
}

func TestShutdownJoinsCancelledTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	w := &Worker{log: zerolog.Nop(), tasks: map[string]*taskHandle{"task": {cancel: cancel, done: done}}}
	shutdown := make(chan error, 1)
	go func() { shutdown <- w.Shutdown(context.Background()) }()
	<-ctx.Done()
	select {
	case err := <-shutdown:
		t.Fatalf("shutdown returned before task exited: %v", err)
	default:
	}
	close(done)
	select {
	case err := <-shutdown:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join")
	}
}

func TestShutdownHonorsDeadlineForStuckTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &Worker{log: zerolog.Nop(), tasks: map[string]*taskHandle{"task": {cancel: func() {}, done: make(chan struct{})}}}
	if err := w.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown result: %v", err)
	}
}

func TestContactLossCancelsEveryTask(t *testing.T) {
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	w := &Worker{tasks: map[string]*taskHandle{"first": {cancel: cancelFirst}, "second": {cancel: cancelSecond}}}
	w.cancelTasksOnContactLoss()
	if first.Err() == nil || second.Err() == nil || len(w.tasks) != 2 {
		t.Fatal("contact loss must cancel tasks and retain them until joined")
	}
}
