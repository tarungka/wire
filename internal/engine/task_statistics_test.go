package engine

import (
	"context"
	"testing"
	"time"
)

func TestTaskStatisticsCountSuccessfulOutputsAndBlockedTime(t *testing.T) {
	stats := &TaskStatistics{}
	ctx := WithTaskStatistics(context.Background(), stats)
	output := make(chan OutputMsg, 1)
	cc := &chainContext{ctx: ctx, outputCh: output}
	event := Event{Key: []byte("key"), Value: []byte("value")}
	if err := processEvent(cc, event); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	cc.ctx = blocked
	if err := processEvent(cc, event); err == nil {
		t.Fatal("expected blocked output to cancel")
	}
	s := stats.Snapshot()
	if s.RecordsIn != 2 || s.RecordsOut != 1 || s.BytesIn != 16 || s.BytesOut != 8 || s.BackpressureMs < 1 {
		t.Fatalf("statistics: %+v", s)
	}
}
