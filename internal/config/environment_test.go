package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func TestEnvironmentOverridePrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.yaml")
	if err := os.WriteFile(path, []byte("worker:\n  task_slots: 2\nheartbeat:\n  interval: 3s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRE_WORKER_TASK_SLOTS", "6")
	t.Setenv("WIRE_HEARTBEAT_INTERVAL", "7s")
	t.Setenv("WIRE_NODE_DEBUG", "true")
	t.Setenv("WIRE_WORKER_COORDINATOR_SEEDS", `["host1:4002","host2:4002"]`)
	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.TaskSlots != 6 || cfg.Heartbeat.Interval.Duration != 7*time.Second || !cfg.Node.Debug || len(cfg.Worker.CoordinatorSeeds) != 2 {
		t.Fatalf("environment not applied: %+v", cfg)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("task-slots", 4, "")
	if err := ApplyFlags(&cfg, flags); err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.TaskSlots != 6 {
		t.Fatal("unchanged flag overrides environment")
	}
	if err := flags.Set("task-slots", "9"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFlags(&cfg, flags); err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.TaskSlots != 9 {
		t.Fatal("explicit flag must take precedence")
	}
}

func TestInvalidEnvironmentOverrides(t *testing.T) {
	for name, value := range map[string]string{"WIRE_WORKER_TASK_SLOTS": "many", "WIRE_NODE_DEBUG": "perhaps", "WIRE_HEARTBEAT_INTERVAL": "5", "WIRE_WORKER_COORDINATOR_SEEDS": "host:4002"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if _, err := Load(nil); err == nil {
				t.Fatalf("accepted invalid %s", name)
			}
		})
	}
}
