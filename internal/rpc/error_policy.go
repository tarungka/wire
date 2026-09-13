package rpc

// ErrorPolicy is the serializable per-operator retry policy. Durations are
// milliseconds. An omitted policy preserves fail-on-error behavior.
type ErrorPolicy struct {
	MaxRetries     int     `codec:"retries"`
	Backoff        string  `codec:"backoff,omitempty"`
	InitialDelayMS int64   `codec:"initial_ms,omitempty"`
	MaxDelayMS     int64   `codec:"max_ms,omitempty"`
	Multiplier     float64 `codec:"multiplier,omitempty"`
	OnExhausted    string  `codec:"exhausted,omitempty"`
}

// DLQSinkDescriptor selects a registered sink factory for one operator's DLQ.
type DLQSinkDescriptor struct {
	ClassName string `codec:"class"`
	Config    []byte `codec:"config,omitempty"`
}
