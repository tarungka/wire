package sdk

import (
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/errorpolicy"
	"github.com/tarungka/wire/internal/rpc"
)

// ErrorHandler configures retries for one operator. Delay fields use milliseconds.
// OnExhausted accepts "fail" (default), "drop", or "dlq". The "dlq" action
// requires a destination before execution/submission. Transactional sinks require
// "fail" with zero retries; record retries may stage duplicate writes.
// Backoff accepts "none", "fixed", or
// "exponential". Source read retries are not supported by this API.
type ErrorHandler = rpc.ErrorPolicy

// WithErrorHandler attaches a validated policy to this transformation or sink.
// It follows the SDK builder convention of panicking on invalid configuration.
func (ds *DataStream) WithErrorHandler(policy ErrorHandler) *DataStream {
	node := ds.env.graph.nodes[ds.nodeID]
	switch node.Type {
	case NodeMap, NodeFlatMap, NodeFilter, NodeProcess, NodeSink:
	default:
		panic("sdk: error handling requires an executable transformation or sink")
	}
	if _, err := errorpolicy.Compile(&policy, node.Name); err != nil {
		panic(err)
	}
	if (node.DLQSink != nil || node.NamedDLQ != nil) && policy.OnExhausted != "dlq" {
		panic("sdk: configured DLQ requires dlq error policy")
	}
	node.ErrorPolicy = &policy
	return ds
}

// WithDLQSink sends this operator's failed records to sink as JSON envelopes.
// Configure OnExhausted="dlq" with WithErrorHandler. Delivery is synchronous,
// best effort, and does not participate in checkpoint transactions. Embedded
// parallel instances share the sink, so it must support concurrent calls.
func (ds *DataStream) WithDLQSink(sink Sink) *DataStream {
	if sink == nil {
		panic("sdk: nil DLQ sink")
	}
	if _, ok := sink.(engine.TransactionalSink); ok {
		panic("sdk: transactional sinks cannot be used as DLQ destinations")
	}
	node := ds.env.graph.nodes[ds.nodeID]
	if node.ErrorPolicy == nil || node.ErrorPolicy.OnExhausted != "dlq" {
		panic("sdk: DLQ sink requires a dlq error policy")
	}
	if node.NamedDLQ != nil {
		panic("sdk: cannot combine inline and named DLQ sinks")
	}
	node.DLQSink = sink
	return ds
}

// WithDLQSinkNamed selects a registered worker sink factory for cluster mode.
func (ds *DataStream) WithDLQSinkNamed(className string, config []byte) *DataStream {
	node := ds.env.graph.nodes[ds.nodeID]
	if className == "" || node.ErrorPolicy == nil || node.ErrorPolicy.OnExhausted != "dlq" {
		panic("sdk: named DLQ requires a class and dlq error policy")
	}
	if node.DLQSink != nil {
		panic("sdk: cannot combine inline and named DLQ sinks")
	}
	node.NamedDLQ = &rpc.DLQSinkDescriptor{ClassName: className, Config: append([]byte(nil), config...)}
	return ds
}
