package engine

import "testing"

func TestUnconfiguredSourceUsesBoundedWatermarks(t *testing.T) {
	source := newMockSource(nil)
	source.SetWatermark(999999)
	slot := &TaskSlot{Source: source, Config: DefaultTaskSlotConfig()}
	strategy := slot.resolveStrategy()
	strategy.ObserveEventTime(20000)
	strategy.ObserveEventTime(18000)
	if got := strategy.GenerateWatermark(); got != 15000 {
		t.Fatalf("default watermark=%d want=15000", got)
	}
}
