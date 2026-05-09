package observability

import (
	"sync"

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
