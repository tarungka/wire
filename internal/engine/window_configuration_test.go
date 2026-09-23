package engine

import (
	"context"
	"testing"
)

func TestConfigureWindowPreservesFactoryAndRejectsUsedState(t *testing.T) {
	build := func() *EventTimeWindowOperator {
		op, err := NewEventTimeWindowOperator(WindowConfig{Kind: "tumbling", Size: 1, AggregationID: "count-v3", MaxStateBytes: 2048}, windowCount{}, func(r WindowResult) Event { return Event{Value: r.Value} })
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	op := build()
	if err := op.ConfigureWindow("session", 0, 0, 10, 30); err != nil {
		t.Fatal(err)
	}
	cfg := op.processor.config
	if cfg.Kind != "session" || cfg.Gap != 10 || cfg.AllowedLateness != 30 || cfg.AggregationID != "count-v3" || cfg.MaxStateBytes != 2048 {
		t.Fatalf("configuration lost: %+v", cfg)
	}
	for _, stage := range []string{"record", "watermark", "open", "restored"} {
		t.Run(stage, func(t *testing.T) {
			used := build()
			switch stage {
			case "record":
				if err := used.FlatMap(context.Background(), Event{EventTime: 0}, func(Event) {}); err != nil {
					t.Fatal(err)
				}
			case "watermark":
				if _, err := used.OnWatermark(context.Background(), 100); err != nil {
					t.Fatal(err)
				}
			case "open":
				if err := used.Open(context.Background()); err != nil {
					t.Fatal(err)
				}
				defer used.Close()
			case "restored":
				_, _ = used.OnWatermark(context.Background(), 100)
				snapshot, err := used.Checkpoint(1)
				if err != nil {
					t.Fatal(err)
				}
				used = build()
				if err := used.RestoreCheckpoint(snapshot); err != nil {
					t.Fatal(err)
				}
			}
			if err := used.ConfigureWindow("tumbling", 20, 0, 0, 0); err == nil {
				t.Fatal("reconfigured live state")
			}
		})
	}
	if err := build().ConfigureWindow("invalid", 0, 0, 0, 0); err == nil {
		t.Fatal("accepted invalid definition")
	}
}
