package observability

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Pre-defined OTel instruments. These are looked up lazily so subsystems
// can call them in init() before observability.Init() has been invoked
// (the first call after Init() materializes the real instrument; calls
// before Init() resolve to the global no-op meter).
//
// Instrument naming: wire.<subsystem>.<name> with units in seconds for
// duration histograms (OTel convention). Keep label cardinality bounded
// — every label value comes from a fixed enum, never from user input.

var (
	instrumentsMu sync.Mutex

	// HTTP API.
	httpRequestDuration metric.Float64Histogram
	httpRequestCount    metric.Int64Counter

	// PebbleStore.
	pebbleOpDuration metric.Float64Histogram
	pebbleOpCount    metric.Int64Counter
	pebbleOpErrors   metric.Int64Counter

	// RPC server.
	rpcDuration metric.Float64Histogram
	rpcCount    metric.Int64Counter
	rpcErrors   metric.Int64Counter

	// Engine — operator chain throughput. Per-record counters are
	// hot, so we coarsen to per-task (one Add per drained batch).
	engineRecordsProcessed metric.Int64Counter

	// Coordinator job lifecycle. Duration is recorded once per job
	// when it reaches a terminal state; the by-status gauge is
	// observed each scrape via a registered callback.
	jobDuration metric.Float64Histogram
)

// HTTPRequestInstruments lazy-initialises and returns the HTTP histograms.
// Call sites use this once and cache the result.
func HTTPRequestInstruments() (metric.Float64Histogram, metric.Int64Counter) {
	instrumentsMu.Lock()
	defer instrumentsMu.Unlock()
	if httpRequestDuration == nil {
		m := Meter()
		httpRequestDuration, _ = m.Float64Histogram(
			"wire.http.request.duration",
			metric.WithUnit("s"),
			metric.WithDescription("HTTP API request duration in seconds"),
		)
		httpRequestCount, _ = m.Int64Counter(
			"wire.http.requests.total",
			metric.WithDescription("Total HTTP API requests"),
		)
	}
	return httpRequestDuration, httpRequestCount
}

// PebbleInstruments returns duration/count/error instruments for the
// metadata store. Call sites attribute by op name (set/get/delete/...).
func PebbleInstruments() (metric.Float64Histogram, metric.Int64Counter, metric.Int64Counter) {
	instrumentsMu.Lock()
	defer instrumentsMu.Unlock()
	if pebbleOpDuration == nil {
		m := Meter()
		pebbleOpDuration, _ = m.Float64Histogram(
			"wire.pebble.op.duration",
			metric.WithUnit("s"),
			metric.WithDescription("PebbleDB metadata store operation duration in seconds"),
		)
		pebbleOpCount, _ = m.Int64Counter(
			"wire.pebble.ops.total",
			metric.WithDescription("Total PebbleDB metadata store operations"),
		)
		pebbleOpErrors, _ = m.Int64Counter(
			"wire.pebble.errors.total",
			metric.WithDescription("Total PebbleDB metadata store operations that returned an error"),
		)
	}
	return pebbleOpDuration, pebbleOpCount, pebbleOpErrors
}

// RPCInstruments returns the RPC server-side histograms attributed by
// method name.
func RPCInstruments() (metric.Float64Histogram, metric.Int64Counter, metric.Int64Counter) {
	instrumentsMu.Lock()
	defer instrumentsMu.Unlock()
	if rpcDuration == nil {
		m := Meter()
		rpcDuration, _ = m.Float64Histogram(
			"wire.rpc.server.duration",
			metric.WithUnit("s"),
			metric.WithDescription("RPC server-side handler duration in seconds"),
		)
		rpcCount, _ = m.Int64Counter(
			"wire.rpc.server.requests.total",
			metric.WithDescription("Total RPC requests served"),
		)
		rpcErrors, _ = m.Int64Counter(
			"wire.rpc.server.errors.total",
			metric.WithDescription("Total RPC requests that returned an error"),
		)
	}
	return rpcDuration, rpcCount, rpcErrors
}

// JobDurationHistogram returns the end-to-end job lifecycle histogram.
// Recorded once per job when it transitions to a terminal state, with
// attribute terminal_status ∈ {FINISHED, FAILED, CANCELED}.
func JobDurationHistogram() metric.Float64Histogram {
	instrumentsMu.Lock()
	defer instrumentsMu.Unlock()
	if jobDuration == nil {
		m := Meter()
		jobDuration, _ = m.Float64Histogram(
			"wire.coordinator.job.duration",
			metric.WithUnit("s"),
			metric.WithDescription("End-to-end job duration in seconds, from CreatedAt to terminal state"),
		)
	}
	return jobDuration
}

// RegisterJobActiveGauge registers an observable gauge that emits the
// number of jobs currently in each lifecycle status. The observe
// callback is invoked once per metric scrape; it must return a
// status -> count map (status name from JobStatus.String(), bounded
// enum). Caller is responsible for synchronising access to coordinator
// state inside the callback.
//
// Returns the registered callback handle (for cleanup) or an error if
// instrument creation fails. When observability is disabled the global
// no-op meter swallows everything and this returns nil.
func RegisterJobActiveGauge(observe func() map[string]int64) (metric.Registration, error) {
	m := Meter()
	gauge, err := m.Int64ObservableGauge(
		"wire.coordinator.jobs.by_status",
		metric.WithDescription("Number of jobs in each lifecycle status (CREATED, DEPLOYING, RUNNING, FINISHED, ...)"),
	)
	if err != nil {
		return nil, err
	}
	return m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for status, count := range observe() {
			o.ObserveInt64(gauge, count, metric.WithAttributes(attribute.String("status", status)))
		}
		return nil
	}, gauge)
}

// EngineRecordsCounter returns a counter that subsystems should Add into
// once per batch (NOT once per record — per-record incurs ~3-5 ns even
// with the no-op fast path).
func EngineRecordsCounter() metric.Int64Counter {
	instrumentsMu.Lock()
	defer instrumentsMu.Unlock()
	if engineRecordsProcessed == nil {
		m := Meter()
		engineRecordsProcessed, _ = m.Int64Counter(
			"wire.engine.records.processed.total",
			metric.WithDescription("Total records processed by operator chains"),
		)
	}
	return engineRecordsProcessed
}
