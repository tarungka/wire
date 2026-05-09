package observability

import (
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// statusRecorder wraps http.ResponseWriter to capture the status code
// written via WriteHeader for the metric's status_class label. Default
// 200 if WriteHeader is never called.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// HTTPMiddleware wraps an http.Handler so every request is recorded into
// wire.http.request.duration (Histogram, seconds) and
// wire.http.requests.total (Counter). Labels: method, route, status_class.
//
// route comes from r.Pattern (Go 1.22+) so it carries the registered
// pattern (e.g. "GET /api/v1/jobs/{job_id}") rather than the concrete
// path — that bounds cardinality. Falls back to "unmatched" for routes
// not registered on the mux.
func HTTPMiddleware(next http.Handler) http.Handler {
	hist, count := HTTPRequestInstruments()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		statusClass := strconv.Itoa(rec.status/100) + "xx"

		attrs := metric.WithAttributes(
			attribute.String("method", r.Method),
			attribute.String("route", route),
			attribute.String("status_class", statusClass),
		)
		hist.Record(r.Context(), time.Since(start).Seconds(), attrs)
		count.Add(r.Context(), 1, attrs)
	})
}
