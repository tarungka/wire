package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tarungka/wire/internal/errorpolicy"
	"github.com/tarungka/wire/internal/rpc"
)

// YAML durations are converted only when representable by the wire policy.
// Reject sub-millisecond values instead of silently disabling a requested delay.
type pipelineErrorPolicy struct {
	MaxRetries   int           `yaml:"max_retries"`
	Backoff      string        `yaml:"backoff"`
	InitialDelay time.Duration `yaml:"initial_delay"`
	MaxDelay     time.Duration `yaml:"max_delay"`
	Multiplier   float64       `yaml:"multiplier"`
	OnExhausted  string        `yaml:"on_exhausted"`
}

func (p *pipelineErrorPolicy) compile(node *StreamNode) error {
	if p == nil {
		return nil
	}
	switch node.Type {
	case NodeMap, NodeFlatMap, NodeFilter, NodeProcess, NodeSink:
	default:
		return fmt.Errorf("error handling requires an executable transformation or sink")
	}
	if p.InitialDelay < 0 || p.MaxDelay < 0 || p.InitialDelay%time.Millisecond != 0 || p.MaxDelay%time.Millisecond != 0 {
		return fmt.Errorf("retry delays must be nonnegative whole milliseconds")
	}
	policy := &rpc.ErrorPolicy{MaxRetries: p.MaxRetries, Backoff: p.Backoff, InitialDelayMS: p.InitialDelay.Milliseconds(), MaxDelayMS: p.MaxDelay.Milliseconds(), Multiplier: p.Multiplier, OnExhausted: p.OnExhausted}
	if _, err := errorpolicy.Compile(policy, node.Name); err != nil {
		return err
	}
	node.ErrorPolicy = policy
	return nil
}

// YAML operators share one destination, owned by the embedded executor.
// Serialize writes so the configured connector need not support concurrency.
type sharedPipelineDLQSink struct {
	mu   sync.Mutex
	sink Sink
}

func (s *sharedPipelineDLQSink) Open(ctx context.Context) error { return s.sink.Open(ctx) }
func (s *sharedPipelineDLQSink) Close() error                   { return s.sink.Close() }
func (s *sharedPipelineDLQSink) Write(ctx context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.sink.Write(ctx, event)
}
