package checkpointpolicy

import "testing"

func TestRollingWindow(t *testing.T) {
	var outcomes []bool
	for i := 0; i < 99; i++ {
		outcomes = Record(outcomes, true)
	}
	if Exceeded(outcomes, .01) {
		t.Fatal("rate enforced before warm-up")
	}
	outcomes = Record(outcomes, true)
	if !Exceeded(outcomes, .01) {
		t.Fatal("full failure window accepted")
	}
	previous := outcomes
	for i := 0; i < 100; i++ {
		outcomes = Record(outcomes, false)
	}
	if Exceeded(outcomes, .01) || len(outcomes) != WindowSize {
		t.Fatal("old failures did not age out")
	}
	if !Exceeded(previous, .01) {
		t.Fatal("record mutated prior metadata")
	}
	if Exceeded(previous, 0) || Exceeded(previous, 1) {
		t.Fatal("disabled/all-tolerated threshold exceeded")
	}
}
