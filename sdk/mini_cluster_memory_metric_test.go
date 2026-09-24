package sdk

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMiniClusterHashMapMemoryAttributionAndCleanup(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(context.Background()) }()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 2})
	defer cluster.Shutdown()
	env := cluster.GetExecutionEnvironment()
	sink := &collectSink{}
	gate := make(chan struct{})
	close(gate)
	env.AddSourceFactory("source", func(InstanceContext) (Source, error) { return &miniRescaleSource{gate: gate}, nil }).SetParallelism(1).
		KeyByWithName("key", func(e Event) ([]byte, error) { return e.Key, nil }).ProcessWithName("state", func(c ProcessContext, e Event) ([]Event, error) {
		c.GetValueState("value").Set(e.Value)
		return []Event{e}, nil
	}).
		AddSinkFactory("sink", func(InstanceContext) (Sink, error) { return sink, nil })
	done := make(chan error, 1)
	go func() { _, err := env.Execute(ctx); done <- err }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	lifecycleWait(t, ctx, func() bool { return len(sink.Events()) == 32 })
	collect := func() int {
		t.Helper()
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != "wire_state_backend_memory_bytes" {
					continue
				}
				for _, point := range metric.Data.(metricdata.Gauge[int64]).DataPoints {
					task, _ := point.Attributes.Value("task_id")
					operator, _ := point.Attributes.Value("operator")
					backend, _ := point.Attributes.Value("backend")
					if !strings.Contains(task.AsString(), "/state/") || operator.AsString() != "state" || backend.AsString() != "hashmap" || point.Value <= 0 {
						t.Fatalf("invalid live backend metric: %+v", point)
					}
					count++
				}
			}
		}
		return count
	}
	if got := collect(); got != 2 {
		t.Fatalf("managed instances=%d want 2", got)
	}
	cancel()
	<-done
	joined = true
	if got := collect(); got != 0 {
		t.Fatalf("closed backend callbacks=%d", got)
	}
}
