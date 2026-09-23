package engine

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestWindowRuntimeMetricsRestorePurgeAndDefaultDrop(t *testing.T) {
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		for _, lateness := range []int64{0, 30} {
			t.Run(kind+fmtLateness(lateness), func(t *testing.T) {
				ctx := context.Background()
				reader := sdkmetric.NewManualReader()
				provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
				previous := otel.GetMeterProvider()
				otel.SetMeterProvider(provider)
				defer func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(ctx) }()
				build := func() *EventTimeWindowOperator {
					op, err := NewEventTimeWindowOperator(WindowConfig{Kind: kind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: lateness, AggregationID: "count-v1"}, windowCount{}, func(r WindowResult) Event { return Event{Key: r.Key, Value: r.Value} })
					if err != nil {
						t.Fatal(err)
					}
					op.SetMetricIdentity("window", "task-1")
					if err := op.Open(ctx); err != nil {
						t.Fatal(err)
					}
					return op
				}
				op := build()
				defer func() { _ = op.Close() }()
				add := func() {
					t.Helper()
					if err := op.FlatMap(ctx, Event{Key: []byte("k"), EventTime: 1}, func(Event) {}); err != nil {
						t.Fatal(err)
					}
				}
				mark := func(w int64) {
					t.Helper()
					if _, err := op.OnWatermark(ctx, w); err != nil {
						t.Fatal(err)
					}
				}
				collect := func() map[string]int64 {
					t.Helper()
					var data metricdata.ResourceMetrics
					if err := reader.Collect(ctx, &data); err != nil {
						t.Fatal(err)
					}
					values := map[string]int64{}
					for _, scope := range data.ScopeMetrics {
						for _, m := range scope.Metrics {
							if !strings.HasPrefix(m.Name, "wire_late_events_") && m.Name != "wire_window_state_retention_bytes" {
								continue
							}
							check := func(p metricdata.DataPoint[int64]) {
								task, _ := p.Attributes.Value("task_id")
								operator, _ := p.Attributes.Value("operator")
								if task.AsString() != "task-1" || operator.AsString() != "window" {
									t.Fatalf("wrong attributes %v", p.Attributes)
								}
								values[m.Name] += p.Value
							}
							switch points := m.Data.(type) {
							case metricdata.Sum[int64]:
								for _, p := range points.DataPoints {
									check(p)
								}
							case metricdata.Gauge[int64]:
								for _, p := range points.DataPoints {
									check(p)
								}
							}
						}
					}
					return values
				}
				add()
				mark(11)
				wantRetention := int64(9)
				if kind == "sliding" {
					wantRetention = 18
				}
				if lateness == 0 {
					wantRetention = 0
				}
				if got := collect()["wire_window_state_retention_bytes"]; got != wantRetention {
					t.Fatalf("retention=%d want=%d", got, wantRetention)
				}
				add()
				before := collect()
				if before["wire_late_events_total"] != 1 {
					t.Fatal(before)
				}
				if lateness == 0 {
					if before["wire_late_events_dropped_total"] != 1 {
						t.Fatal(before)
					}
				} else if before["wire_late_events_allowed_total"] != 1 {
					t.Fatal(before)
				}
				snapshot, err := op.Checkpoint(1)
				if err != nil {
					t.Fatal(err)
				}
				mark(100)
				if got := collect()["wire_window_state_retention_bytes"]; got != 0 {
					t.Fatalf("purge gauge=%d", got)
				}
				if err := op.Close(); err != nil {
					t.Fatal(err)
				}
				if _, ok := collect()["wire_window_state_retention_bytes"]; ok {
					t.Fatal("closed gauge still registered")
				}
				op = build()
				if err := op.RestoreCheckpoint(snapshot); err != nil {
					t.Fatal(err)
				}
				after := collect()
				if after["wire_window_state_retention_bytes"] != wantRetention || after["wire_late_events_total"] != before["wire_late_events_total"] {
					t.Fatalf("restore duplicated counters or lost gauge: %v", after)
				}
				mark(100)
				add()
				after = collect()
				if after["wire_late_events_total"] != 2 || after["wire_window_state_retention_bytes"] != 0 {
					t.Fatal(after)
				}
				wantDropped := int64(1)
				if lateness == 0 {
					wantDropped = 2
				}
				if after["wire_late_events_dropped_total"] != wantDropped {
					t.Fatal(after)
				}
			})
		}
	}
}
func fmtLateness(lateness int64) string {
	if lateness == 0 {
		return "/zero"
	}
	return "/retained"
}
