package engine

import "testing"

func completeManifest() *CheckpointMetadata {
	return &CheckpointMetadata{SchemaVersion: 1, Type: CheckpointType, CheckpointID: 1, JobID: "job", JobGraph: JobGraphMeta{NumKeyGroups: 4, Operators: []OperatorMeta{{OperatorID: "source", Type: "source", Parallelism: 2}}}, Tasks: []TaskMeta{
		{TaskID: "source-0", OperatorID: "source", SubtaskIndex: 0, KeyGroupRange: KeyGroupRangeMeta{Start: 0, End: 2}, StatePath: "task-0"},
		{TaskID: "source-1", OperatorID: "source", SubtaskIndex: 1, KeyGroupRange: KeyGroupRangeMeta{Start: 2, End: 4}, StatePath: "task-1"},
	}}
}

func TestCompleteCheckpointCoverage(t *testing.T) {
	if err := completeManifest().ValidateComplete(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CheckpointMetadata){
		"missing task":     func(m *CheckpointMetadata) { m.Tasks = m.Tasks[:1] },
		"overlap":          func(m *CheckpointMetadata) { m.Tasks[1].KeyGroupRange.Start = 1 },
		"gap":              func(m *CheckpointMetadata) { m.Tasks[1].KeyGroupRange.Start = 3 },
		"incomplete end":   func(m *CheckpointMetadata) { m.Tasks[1].KeyGroupRange.End = 3 },
		"directory alias":  func(m *CheckpointMetadata) { m.Tasks[1].StatePath = m.Tasks[0].StatePath + "/" },
		"shared directory": func(m *CheckpointMetadata) { m.Tasks[1].StatePath = m.Tasks[0].StatePath },
		"chain cycle":      func(m *CheckpointMetadata) { id := "source"; m.JobGraph.Operators[0].ChainedTo = &id },
	} {
		t.Run(name, func(t *testing.T) {
			m := completeManifest()
			mutate(m)
			if err := m.ValidateComplete(); err == nil {
				t.Fatal("invalid complete manifest accepted")
			}
		})
	}
}

func TestCompleteCheckpointChainedOperator(t *testing.T) {
	m := completeManifest()
	head := "source"
	m.JobGraph.Operators = append(m.JobGraph.Operators, OperatorMeta{OperatorID: "map", Type: "map", Parallelism: 2, ChainedTo: &head})
	if err := m.ValidateComplete(); err != nil {
		t.Fatal(err)
	}
	m.JobGraph.Operators[1].Parallelism = 3
	if err := m.ValidateComplete(); err == nil {
		t.Fatal("incompatible chain accepted")
	}
}
