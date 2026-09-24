package sdk

import (
	"context"
	"testing"
	"time"
)

func TestEmbeddedDefaultWatermarkIsBounded(t *testing.T) {
	strategy, interval := embeddedWatermark([]*StreamNode{{Type: NodeSource}})
	strategy.ObserveEventTime(20000)
	strategy.ObserveEventTime(18000)
	if got := strategy.GenerateWatermark(); got != 15000 || interval != 200*time.Millisecond {
		t.Fatalf("default watermark=%d interval=%s", got, interval)
	}
}

func TestExplicitZeroWatermarkTolerance(t *testing.T) {
	configured := BoundedOutOfOrderness(0)
	strategy, _ := embeddedWatermark([]*StreamNode{{Type: NodeSource, Watermark: &configured.config}})
	strategy.ObserveEventTime(10000)
	if got := strategy.GenerateWatermark(); got != 10000 {
		t.Fatalf("zero tolerance used default: %d", got)
	}
}

func TestSourceWatermarkStrategyGraphConversion(t *testing.T) {
	env := New()
	strategy := BoundedOutOfOrderness(5 * time.Second).WithEmitInterval(time.Second).WithIdleTimeout(2 * time.Minute)
	source := env.AddSourceNamed("events", "source", nil).SetWatermarkStrategy(strategy)
	source.AddSinkNamed("out", "sink", nil)
	if err := env.graph.validate(); err != nil {
		t.Fatal(err)
	}
	graph := env.graph.toJobGraph(1)
	cfg := graph.Operators[0].Watermark
	if cfg == nil || cfg.Strategy != "bounded-ooo" || cfg.MaxOOO == nil || *cfg.MaxOOO != 5*time.Second || cfg.EmitInterval != time.Second || cfg.IdleTimeout != 2*time.Minute {
		t.Fatalf("lost watermark configuration: %+v", cfg)
	}
	strategy = strategy.WithEmitInterval(3 * time.Second)
	if cfg.EmitInterval == strategy.config.EmitInterval {
		t.Fatal("strategy mutation changed configured source")
	}
	source.SetWatermarkStrategy(BoundedOutOfOrderness(-time.Second))
	if env.graph.validate() == nil {
		t.Fatal("negative tolerance accepted")
	}
}

func TestEmbeddedIngestionTimeReplacesProducerTimestamp(t *testing.T) {
	env := New()
	sink := &collectSink{}
	before := time.Now().UnixMilli()
	env.AddSource(&sliceSource{events: []Event{{EventTime: 1, Value: []byte("record")}}}).SetWatermarkStrategy(IngestionTime()).AddSink(sink)
	if _, err := env.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixMilli()
	events := sink.Events()
	if len(events) != 1 || events[0].EventTime < before || events[0].EventTime > after {
		t.Fatalf("ingestion timestamp not applied: %+v", events)
	}
}
