package observability

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func TestTaskPrometheusExportAndCleanup(t *testing.T) {
	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		t.Fatal(err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	m := provider.Meter("wire")
	closeChannels, err := observeTaskChannels(m, "task", func() (int, int) { return 2, 3 }, func() int64 { return 19 })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeChannels() }()
	closeGoroutines, err := observeTaskGoroutines(m, "task", func() int64 { return 5 })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeGoroutines() }()
	backpressure, err := taskBackpressureRecorder(m, "task")
	if err != nil {
		t.Fatal(err)
	}
	backpressure(2500 * time.Microsecond)
	upload, err := checkpointUploadRecorder(m)
	if err != nil {
		t.Fatal(err)
	}
	upload(context.Background(), "task", 50*time.Millisecond)
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	scrape := func() string {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
		if response.Code != 200 {
			t.Fatalf("scrape: %d %s", response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	text := scrape()
	for _, name := range []string{"wire_task_input_channel_usage", "wire_task_output_channel_usage", "wire_task_alignment_buffer_bytes", "wire_task_goroutine_count", "wire_task_backpressure_time_ms_total", "wire_task_checkpoint_upload_duration_ms_count"} {
		if !strings.Contains(text, name+"{") {
			t.Fatalf("missing %s:\n%s", name, text)
		}
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]float64{"wire_task_input_channel_usage": 2, "wire_task_output_channel_usage": 3, "wire_task_alignment_buffer_bytes": 19, "wire_task_goroutine_count": 5}
	for _, family := range families {
		if want, ok := wants[family.GetName()]; ok {
			if len(family.Metric) != 1 || family.Metric[0].GetGauge().GetValue() != want {
				t.Fatalf("bad gauge %s: %v", family.GetName(), family.Metric)
			}
		}
		if family.GetName() == "wire_task_backpressure_time_ms_total" && family.Metric[0].GetCounter().GetValue() != 2.5 {
			t.Fatal("counter unit conversion")
		}
		if family.GetName() == "wire_task_checkpoint_upload_duration_ms" && family.Metric[0].GetHistogram().GetSampleSum() != 50 {
			t.Fatal("histogram unit conversion")
		}
	}
	if err := closeChannels(); err != nil {
		t.Fatal(err)
	}
	if err := closeGoroutines(); err != nil {
		t.Fatal(err)
	}
	text = scrape()
	for name := range wants {
		if strings.Contains(text, name+"{") {
			t.Fatalf("finished task gauge retained: %s", name)
		}
	}
}
