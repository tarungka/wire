package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskSlotConfigurationLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wire.yaml")
	if err := os.WriteFile(path, []byte("task_slot:\n  input_buffer_size: 8\n  output_buffer_size: 16\n  alignment_buffer_size: 32\n  checkpoint_upload_concurrency: 2\n  drain_timeout: 750ms\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.TaskSlot
	if got.InputBufferSize != 8 || got.OutputBufferSize != 16 || got.AlignmentBufferSize != 32 || got.CheckpointUploadConcurrency != 2 || got.DrainTimeout.Duration != 750*time.Millisecond {
		t.Fatalf("task settings: %+v", got)
	}
	for _, field := range []string{"input_buffer_size", "output_buffer_size", "alignment_buffer_size", "checkpoint_upload_concurrency"} {
		if err := os.WriteFile(path, []byte("task_slot:\n  "+field+": -1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load([]string{path})
		if err == nil {
			err = cfg.Validate()
		}
		if err == nil {
			t.Fatalf("negative %s accepted", field)
		}
	}
}
