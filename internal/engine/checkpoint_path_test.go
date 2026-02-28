package engine

import "testing"

func TestCheckpointPath(t *testing.T) {
	tests := []struct {
		name         string
		jobID        string
		checkpointID int64
		want         string
	}{
		{"basic", "job-001", 1, "jobs/job-001/checkpoints/chk-1/metadata.json"},
		{"large ID", "my-job", 999999, "jobs/my-job/checkpoints/chk-999999/metadata.json"},
		{"zero ID", "j", 0, "jobs/j/checkpoints/chk-0/metadata.json"},
		{"empty job ID", "", 1, "jobs//checkpoints/chk-1/metadata.json"},
		{"negative ID", "job-001", -1, "jobs/job-001/checkpoints/chk--1/metadata.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckpointPath(tt.jobID, tt.checkpointID); got != tt.want {
				t.Errorf("CheckpointPath(%q, %d) = %q, want %q", tt.jobID, tt.checkpointID, got, tt.want)
			}
		})
	}
}

func TestSavepointPath(t *testing.T) {
	tests := []struct {
		name        string
		jobID       string
		savepointID int64
		want        string
	}{
		{"basic", "job-001", 1, "jobs/job-001/savepoints/sp-1/metadata.json"},
		{"large ID", "my-job", 42, "jobs/my-job/savepoints/sp-42/metadata.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SavepointPath(tt.jobID, tt.savepointID); got != tt.want {
				t.Errorf("SavepointPath(%q, %d) = %q, want %q", tt.jobID, tt.savepointID, got, tt.want)
			}
		})
	}
}

func TestCheckpointDir(t *testing.T) {
	tests := []struct {
		name         string
		jobID        string
		checkpointID int64
		want         string
	}{
		{"basic", "job-001", 5, "jobs/job-001/checkpoints/chk-5/"},
		{"zero", "j", 0, "jobs/j/checkpoints/chk-0/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckpointDir(tt.jobID, tt.checkpointID); got != tt.want {
				t.Errorf("CheckpointDir(%q, %d) = %q, want %q", tt.jobID, tt.checkpointID, got, tt.want)
			}
		})
	}
}

func TestSavepointDir(t *testing.T) {
	tests := []struct {
		name        string
		jobID       string
		savepointID int64
		want        string
	}{
		{"basic", "job-001", 10, "jobs/job-001/savepoints/sp-10/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SavepointDir(tt.jobID, tt.savepointID); got != tt.want {
				t.Errorf("SavepointDir(%q, %d) = %q, want %q", tt.jobID, tt.savepointID, got, tt.want)
			}
		})
	}
}

func TestTaskStatePath(t *testing.T) {
	tests := []struct {
		name         string
		subtaskIndex int
		want         string
	}{
		{"zero", 0, "task-0-state/"},
		{"positive", 3, "task-3-state/"},
		{"large", 127, "task-127-state/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TaskStatePath(tt.subtaskIndex); got != tt.want {
				t.Errorf("TaskStatePath(%d) = %q, want %q", tt.subtaskIndex, got, tt.want)
			}
		})
	}
}
