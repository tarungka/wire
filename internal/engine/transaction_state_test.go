package engine

import "testing"

func TestTxnState_String(t *testing.T) {
	tests := []struct {
		state TxnState
		want  string
	}{
		{TxnActive, "ACTIVE"},
		{TxnPreCommitted, "PRE_COMMITTED"},
		{TxnCommitted, "COMMITTED"},
		{TxnState(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("TxnState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestSinkTaskTxnState_Defaults(t *testing.T) {
	var s SinkTaskTxnState
	if s.TaskID != "" {
		t.Errorf("TaskID zero value: got %q, want empty", s.TaskID)
	}
	if s.CurrentCheckpoint != 0 {
		t.Errorf("CurrentCheckpoint zero value: got %d, want 0", s.CurrentCheckpoint)
	}
	if s.LastCommittedCheckpoint != 0 {
		t.Errorf("LastCommittedCheckpoint zero value: got %d, want 0", s.LastCommittedCheckpoint)
	}
	if s.State != TxnActive {
		t.Errorf("State zero value: got %v, want TxnActive", s.State)
	}
}
