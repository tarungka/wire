// sources/api.go
package sources

import (
	"context"
	"time"
)

// Source represents a data source connector
type Source interface {
	// Core lifecycle methods
	Open(ctx context.Context) error
	Read(ctx context.Context) (*Record, error)
	Close() error

	// Offset management for exactly-once
	GetOffset() (Offset, error)
	CommitOffset(offset Offset) error
	SeekToOffset(offset Offset) error

	// Schema discovery
	DiscoverSchema() (*Schema, error)

	// Metrics and monitoring
	GetMetrics() SourceMetrics
	HealthCheck() error
}

// Record represents a single data record
type Record struct {
	Key       []byte
	Value     []byte
	Timestamp time.Time
	Headers   map[string][]byte
	Offset    Offset
	Partition int32
	Source    string
}

// Offset represents position in source
type Offset interface {
	Serialize() ([]byte, error)
	Compare(other Offset) int
}

// Schema represents data schema
type Schema struct {
	Fields []Field
	Format DataFormat
}

// Field represents a single field in a schema
type Field struct {
	Name string
	Type string // Consider using a more specific type like DataType
	// Add other properties like Nullable, DefaultValue, etc.
}

// DataFormat represents the format of the data (e.g., JSON, Avro, Protobuf)
type DataFormat string

const (
	JSON    DataFormat = "json"
	Avro    DataFormat = "avro"
	Protobuf DataFormat = "protobuf"
	CSV     DataFormat = "csv"
	// Add other formats as needed
)

// SourceMetrics holds metrics for a source
// This is a placeholder and should be expanded based on actual metrics needed.
type SourceMetrics struct {
	RecordsRead       Counter
	BytesRead         Counter
	ReadErrors        Counter
	ConnectionsActive Gauge
	// Add other relevant metrics
}

// Counter is a placeholder for a metric counter type
type Counter interface {
	Inc()
	Add(float64)
}

// Gauge is a placeholder for a metric gauge type
type Gauge interface {
	Inc()
	Dec()
	Set(float64)
}

// Placeholder implementation for Counter and Gauge
// In a real scenario, these would integrate with a metrics library (e.g., Prometheus client)
type NopCounter struct{}
func (c *NopCounter) Inc() {}
func (c *NopCounter) Add(f float64) {}

type NopGauge struct{}
func (g *NopGauge) Inc() {}
func (g *NopGauge) Dec() {}
func (g *NopGauge) Set(f float64) {}


// NewSourceMetrics initializes a new SourceMetrics struct
// This is a placeholder and should be adapted for actual metrics implementation
func NewSourceMetrics(sourceType string) *SourceMetrics {
	// In a real implementation, you would register metrics with appropriate labels
	return &SourceMetrics{
		RecordsRead:       &NopCounter{},
		BytesRead:         &NopCounter{},
		ReadErrors:        &NopCounter{},
		ConnectionsActive: &NopGauge{},
	}
}


// SourceConfig base configuration
type SourceConfig struct {
	Name              string
	Type              string
	ParallelismHint   int
	StartOffset       StartOffsetStrategy
	MaxBatchSize      int
	PollTimeout       time.Duration
	RetryPolicy       RetryPolicy
	Properties        map[string]interface{}
}

// StartOffsetStrategy defines where to start reading from in a source
type StartOffsetStrategy string

const (
	Earliest StartOffsetStrategy = "earliest"
	Latest   StartOffsetStrategy = "latest"
	// Add other strategies like "specific_offset", "timestamp"
)

// RetryPolicy defines the retry behavior for transient errors
type RetryPolicy struct {
	MaxRetries    int
	Backoff       time.Duration
	MaxBackoff    time.Duration
	RetryableErrs []string // List of error messages or types considered retryable
}

// ErrNoData is returned by Read when no new data is available.
// This is not necessarily an error, but a signal that the source is currently empty.
var ErrNoData = &noDataError{}

type noDataError struct{}

func (e *noDataError) Error() string {
	return "no data available"
}
