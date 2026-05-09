package engine

import (
	"fmt"
	"testing"
	"time"
)

// makeBenchCheckpoint builds a CheckpointMetadata of given task count for
// the metadata-codec benches. Realistic shape: one operator per task, two
// state files per task, simple key-group range partitioning.
func makeBenchCheckpoint(numTasks int) *CheckpointMetadata {
	ops := make([]OperatorMeta, numTasks)
	for i := 0; i < numTasks; i++ {
		ops[i] = OperatorMeta{
			OperatorID:  fmt.Sprintf("op-%d", i),
			Type:        "map",
			Parallelism: 1,
		}
	}
	tasks := make([]TaskMeta, numTasks)
	for i := 0; i < numTasks; i++ {
		tasks[i] = TaskMeta{
			TaskID:         fmt.Sprintf("task-%d", i),
			OperatorID:     fmt.Sprintf("op-%d", i),
			SubtaskIndex:   0,
			KeyGroupRange:  KeyGroupRangeMeta{Start: i, End: i + 1},
			StatePath:      fmt.Sprintf("state/task-%d", i),
			StateSizeBytes: 1024,
			StateFiles:     []string{"file-a.sst", "file-b.sst"},
		}
	}
	return &CheckpointMetadata{
		SchemaVersion:  CurrentSchemaVersion,
		Type:           CheckpointType,
		CheckpointID:   42,
		JobID:          "job-bench",
		JobName:        "bench-job",
		TriggerTime:    time.Unix(1708819200, 0),
		CompletionTime: time.Unix(1708819203, 0),
		DurationMs:     3000,
		JobGraph: JobGraphMeta{
			NumKeyGroups: numTasks,
			Operators:    ops,
		},
		Tasks: tasks,
	}
}

// BenchmarkMarshalCheckpointMetadata measures the JSON encode cost. This
// is on the path of every checkpoint completion that writes metadata.json.
func BenchmarkMarshalCheckpointMetadata(b *testing.B) {
	for _, n := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("tasks=%d", n), func(b *testing.B) {
			meta := makeBenchCheckpoint(n)
			out, err := MarshalCheckpointMetadata(meta)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := MarshalCheckpointMetadata(meta); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkUnmarshalCheckpointMetadata is the recovery-path JSON decode
// when restoring from metadata.json on disk.
func BenchmarkUnmarshalCheckpointMetadata(b *testing.B) {
	for _, n := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("tasks=%d", n), func(b *testing.B) {
			meta := makeBenchCheckpoint(n)
			data, err := MarshalCheckpointMetadata(meta)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if _, err := UnmarshalCheckpointMetadata(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkValidateCheckpointMetadata exercises the structural validator,
// which builds two maps and walks operators+tasks. Called once per
// metadata read on the recovery path.
func BenchmarkValidateCheckpointMetadata(b *testing.B) {
	meta := makeBenchCheckpoint(64)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := meta.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCheckpointPath measures the per-call cost of the path-builder
// helpers. These are called every checkpoint trigger / restore cycle.
func BenchmarkCheckpointPath(b *testing.B) {
	b.Run("CheckpointPath", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = CheckpointPath("job-12345-67890", int64(i))
		}
	})
	b.Run("SavepointPath", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = SavepointPath("job-12345-67890", int64(i))
		}
	})
	b.Run("CheckpointDir", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = CheckpointDir("job-12345-67890", int64(i))
		}
	})
	b.Run("TaskStatePath", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = TaskStatePath(i)
		}
	})
}
