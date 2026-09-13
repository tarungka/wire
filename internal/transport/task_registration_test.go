package transport

import (
	"context"
	"testing"
	"time"
)

func TestTaskRegistrationWaitLifecycle(t *testing.T) {
	for _, mode := range []string{"register", "timeout", "cancel", "session-close", "mux-close", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			_, session := negotiationPair(t)
			cfg := DefaultConfig()
			cfg.TaskRegistrationTimeout = 40 * time.Millisecond
			if mode == "disabled" {
				cfg.TaskRegistrationTimeout = 0
			}
			mux := NewMux(cfg)
			defer mux.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan *taskQueue, 1)
			go func() { done <- mux.waitForTask(ctx, session, "task") }()
			switch mode {
			case "register":
				if err := mux.RegisterTask("task"); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			case "session-close":
				_ = session.Close()
			case "mux-close":
				_ = mux.Close()
			}
			select {
			case queue := <-done:
				if (queue != nil) != (mode == "register") {
					t.Fatalf("unexpected admission: %v", queue)
				}
			case <-time.After(time.Second):
				t.Fatal("registration wait leaked")
			}
		})
	}
}
