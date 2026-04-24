package coordinator

import "testing"

func TestKeyConstruction(t *testing.T) {
	tests := []struct {
		name     string
		fn       func() []byte
		expected string
	}{
		{
			name:     "JobMetaKey",
			fn:       func() []byte { return JobMetaKey("job-123") },
			expected: "jobs/job-123/meta",
		},
		{
			name:     "JobGraphKey",
			fn:       func() []byte { return JobGraphKey("job-123") },
			expected: "jobs/job-123/graph",
		},
		{
			name:     "JobAssignmentsKey",
			fn:       func() []byte { return JobAssignmentsKey("job-123") },
			expected: "jobs/job-123/assignments",
		},
		{
			name:     "CheckpointKey",
			fn:       func() []byte { return CheckpointKey("job-123", 42) },
			expected: "jobs/job-123/checkpoints/000000000000002a",
		},
		{
			name:     "CheckpointKey zero",
			fn:       func() []byte { return CheckpointKey("job-123", 0) },
			expected: "jobs/job-123/checkpoints/0000000000000000",
		},
		{
			name:     "LatestCheckpointKey",
			fn:       func() []byte { return LatestCheckpointKey("job-123") },
			expected: "jobs/job-123/checkpoints/latest",
		},
		{
			name:     "WorkerMetaKey",
			fn:       func() []byte { return WorkerMetaKey("worker-abc") },
			expected: "workers/worker-abc/meta",
		},
		{
			name:     "WorkerTasksKey",
			fn:       func() []byte { return WorkerTasksKey("worker-abc") },
			expected: "workers/worker-abc/tasks",
		},
		{
			name:     "ClusterConfigKey",
			fn:       func() []byte { return ClusterConfigKey() },
			expected: "cluster/config",
		},
		{
			name:     "ClusterEpochKey",
			fn:       func() []byte { return ClusterEpochKey() },
			expected: "cluster/epoch",
		},
		{
			name:     "ClusterLeaderKey",
			fn:       func() []byte { return ClusterLeaderKey() },
			expected: "cluster/leader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(tt.fn())
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestKeyPrefixes(t *testing.T) {
	if JobsPrefix != "jobs/" {
		t.Errorf("JobsPrefix = %q, want %q", JobsPrefix, "jobs/")
	}
	if WorkersPrefix != "workers/" {
		t.Errorf("WorkersPrefix = %q, want %q", WorkersPrefix, "workers/")
	}
	if ClusterPrefix != "cluster/" {
		t.Errorf("ClusterPrefix = %q, want %q", ClusterPrefix, "cluster/")
	}
}

func TestCheckpointKeySortOrder(t *testing.T) {
	k1 := string(CheckpointKey("job-1", 1))
	k2 := string(CheckpointKey("job-1", 2))
	k3 := string(CheckpointKey("job-1", 100))

	if k1 >= k2 {
		t.Errorf("checkpoint key ordering broken: %q >= %q", k1, k2)
	}
	if k2 >= k3 {
		t.Errorf("checkpoint key ordering broken: %q >= %q", k2, k3)
	}
}
