package sdk

import (
	"testing"
	"time"
)

func TestFixedDelay(t *testing.T) {
	s := FixedDelay(3, 5*time.Second)
	if s.Type != RestartFixedDelay {
		t.Errorf("expected RestartFixedDelay, got %v", s.Type)
	}
	if s.MaxAttempts != 3 {
		t.Errorf("expected MaxAttempts=3, got %d", s.MaxAttempts)
	}
	if s.Delay != 5*time.Second {
		t.Errorf("expected Delay=5s, got %v", s.Delay)
	}
}

func TestExponentialBackoff(t *testing.T) {
	s := ExponentialBackoff(5, 100*time.Millisecond, 10*time.Second, 2.0)
	if s.Type != RestartExponentialBackoff {
		t.Errorf("expected RestartExponentialBackoff, got %v", s.Type)
	}
	if s.MaxAttempts != 5 {
		t.Errorf("expected MaxAttempts=5, got %d", s.MaxAttempts)
	}
	if s.Delay != 100*time.Millisecond {
		t.Errorf("expected Delay=100ms, got %v", s.Delay)
	}
	if s.MaxDelay != 10*time.Second {
		t.Errorf("expected MaxDelay=10s, got %v", s.MaxDelay)
	}
	if s.BackoffMultiplier != 2.0 {
		t.Errorf("expected BackoffMultiplier=2.0, got %f", s.BackoffMultiplier)
	}
}

func TestNoRestart(t *testing.T) {
	s := NoRestart()
	if s.Type != RestartNone {
		t.Errorf("expected RestartNone, got %v", s.Type)
	}
}

func TestOutputTag(t *testing.T) {
	tag := NewOutputTag("late-events")
	if tag.Name != "late-events" {
		t.Errorf("expected name 'late-events', got %q", tag.Name)
	}
}
