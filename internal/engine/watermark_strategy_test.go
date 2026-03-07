package engine

import (
	"sync"
	"testing"
	"time"
)

func TestBoundedOutOfOrderness_Basic(t *testing.T) {
	s := NewBoundedOutOfOrdernessStrategy(5 * time.Second)

	// No events observed — watermark should be 0.
	if wm := s.GenerateWatermark(); wm != 0 {
		t.Errorf("initial watermark: got %d, want 0", wm)
	}

	// Observe event at 10000ms.
	s.ObserveEventTime(10000)
	// Watermark = 10000 - 5000 = 5000.
	if wm := s.GenerateWatermark(); wm != 5000 {
		t.Errorf("watermark after 10000ms event: got %d, want 5000", wm)
	}

	// Observe later event at 20000ms.
	s.ObserveEventTime(20000)
	// Watermark = 20000 - 5000 = 15000.
	if wm := s.GenerateWatermark(); wm != 15000 {
		t.Errorf("watermark after 20000ms event: got %d, want 15000", wm)
	}
}

func TestBoundedOutOfOrderness_NegativeAvoidance(t *testing.T) {
	s := NewBoundedOutOfOrdernessStrategy(10 * time.Second)

	// Observe event at 3000ms — watermark would be 3000-10000 = -7000, clamped to 0.
	s.ObserveEventTime(3000)
	if wm := s.GenerateWatermark(); wm != 0 {
		t.Errorf("watermark should not be negative: got %d, want 0", wm)
	}
}

func TestBoundedOutOfOrderness_DoesNotRegress(t *testing.T) {
	s := NewBoundedOutOfOrdernessStrategy(1 * time.Second)

	s.ObserveEventTime(5000)
	wm1 := s.GenerateWatermark() // 4000

	// Observe an earlier event — watermark should NOT regress.
	s.ObserveEventTime(3000)
	wm2 := s.GenerateWatermark()
	if wm2 < wm1 {
		t.Errorf("watermark regressed: %d < %d", wm2, wm1)
	}
}

func TestBoundedOutOfOrderness_DefaultMaxOOO(t *testing.T) {
	// Zero duration should use default (5s).
	s := NewBoundedOutOfOrdernessStrategy(0)
	s.ObserveEventTime(10000)
	// 10000 - 5000 = 5000
	if wm := s.GenerateWatermark(); wm != 5000 {
		t.Errorf("watermark with default MaxOOO: got %d, want 5000", wm)
	}
}

func TestBoundedOutOfOrderness_Concurrent(t *testing.T) {
	s := NewBoundedOutOfOrdernessStrategy(1 * time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for j := int64(0); j < 100; j++ {
				s.ObserveEventTime(base + j)
				_ = s.GenerateWatermark()
			}
		}(int64(i * 1000))
	}
	wg.Wait()

	// maxObserved should be at least 9099 (goroutine i=9, j=99).
	wm := s.GenerateWatermark()
	// Watermark = maxObserved - 1000, should be >= 8099
	if wm < 8099 {
		t.Errorf("concurrent watermark too low: got %d, want >= 8099", wm)
	}
}

func TestMonotonicTimestamps_Basic(t *testing.T) {
	s := NewMonotonicTimestampsStrategy()

	if wm := s.GenerateWatermark(); wm != 0 {
		t.Errorf("initial watermark: got %d, want 0", wm)
	}

	s.ObserveEventTime(1000)
	if wm := s.GenerateWatermark(); wm != 1000 {
		t.Errorf("watermark: got %d, want 1000", wm)
	}

	s.ObserveEventTime(2000)
	if wm := s.GenerateWatermark(); wm != 2000 {
		t.Errorf("watermark: got %d, want 2000", wm)
	}

	// Earlier event should not regress.
	s.ObserveEventTime(500)
	if wm := s.GenerateWatermark(); wm != 2000 {
		t.Errorf("watermark regressed: got %d, want 2000", wm)
	}
}

func TestMonotonicTimestamps_Concurrent(t *testing.T) {
	s := NewMonotonicTimestampsStrategy()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for j := int64(0); j < 100; j++ {
				s.ObserveEventTime(base + j)
				_ = s.GenerateWatermark()
			}
		}(int64(i * 1000))
	}
	wg.Wait()

	// maxObserved should be at least 9099.
	if wm := s.GenerateWatermark(); wm < 9099 {
		t.Errorf("concurrent watermark too low: got %d, want >= 9099", wm)
	}
}

func TestIngestionTime_Basic(t *testing.T) {
	// Use injectable clock for deterministic testing.
	s := &IngestionTimeStrategy{
		clock: func() int64 { return 42000 },
	}

	if wm := s.GenerateWatermark(); wm != 42000 {
		t.Errorf("watermark: got %d, want 42000", wm)
	}

	// ObserveEventTime is a no-op.
	s.ObserveEventTime(99999)
	if wm := s.GenerateWatermark(); wm != 42000 {
		t.Errorf("watermark after observe: got %d, want 42000", wm)
	}
}

func TestIngestionTime_AdvancingClock(t *testing.T) {
	var now int64 = 1000
	s := &IngestionTimeStrategy{
		clock: func() int64 { return now },
	}

	if wm := s.GenerateWatermark(); wm != 1000 {
		t.Errorf("watermark: got %d, want 1000", wm)
	}

	now = 2000
	if wm := s.GenerateWatermark(); wm != 2000 {
		t.Errorf("watermark: got %d, want 2000", wm)
	}
}

func TestLegacySourceStrategy(t *testing.T) {
	source := newMockSource(nil)
	s := newLegacySourceStrategy(source)

	source.SetWatermark(100)
	if wm := s.GenerateWatermark(); wm != 100 {
		t.Errorf("watermark: got %d, want 100", wm)
	}

	source.SetWatermark(200)
	if wm := s.GenerateWatermark(); wm != 200 {
		t.Errorf("watermark: got %d, want 200", wm)
	}

	// ObserveEventTime is a no-op for legacy.
	s.ObserveEventTime(999)
	if wm := s.GenerateWatermark(); wm != 200 {
		t.Errorf("watermark after observe: got %d, want 200", wm)
	}
}
