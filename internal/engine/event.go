package engine

import (
	"bytes"

	"github.com/tarungka/wire/internal/protocol"
)

// Event is the internal representation of a data record flowing through the
// operator chain.
type Event struct {
	sideOutput     string // Internal routing tag, consumed before network serialization.
	inputActivity  *inputActivity
	inputWatermark *inputWatermarkBoundary
	watermark      *int64 // Internal ordered boundary; never exposed as a data record.
	Key            []byte
	Value          []byte
	EventTime      int64
	Headers        map[string][]byte
}

// cloneEventPayload gives an operator attempt its own mutable payload. Internal
// queue/activity markers retain their identity and are never exposed to users.
func cloneEventPayload(e Event) Event {
	e.Key = bytes.Clone(e.Key)
	e.Value = bytes.Clone(e.Value)
	if e.Headers != nil {
		headers := make(map[string][]byte, len(e.Headers))
		for key, value := range e.Headers {
			headers[key] = bytes.Clone(value)
		}
		e.Headers = headers
	}
	return e
}

type inputActivity struct {
	tracker *InputWatermarkTracker
	input   int
}

// EventFromProto converts a protocol.DataRecordMsg into an Event.
func EventFromProto(msg *protocol.DataRecordMsg) Event {
	return Event{
		Key:       msg.Key,
		Value:     msg.Value,
		EventTime: msg.EventTime,
		Headers:   msg.Headers,
	}
}

// ToProto converts an Event into a protocol.DataRecordMsg.
func (e Event) ToProto() *protocol.DataRecordMsg {
	return &protocol.DataRecordMsg{
		Key:       e.Key,
		Value:     e.Value,
		EventTime: e.EventTime,
		Headers:   e.Headers,
	}
}

// ControlType identifies the kind of control message.
type ControlType uint8

const (
	CtrlBarrierReceived  ControlType = iota // A checkpoint barrier arrived on an input.
	CtrlAbortCheckpoint                     // Abort the current checkpoint.
	CtrlShutdown                            // Graceful shutdown requested.
	CtrlEndOfPartition                      // An input has reached end of partition.
	CtrlCommitCheckpoint                    // Coordinator confirms global checkpoint completion; sink should Commit.
	CtrlAbortTransaction                    // Coordinator instructs sink to abort in-flight transaction.
	CtrlDrainInputs                         // Intake is stopping; release alignment before final shutdown.
)

// ControlMsg carries control signals from input readers to the operator chain.
type ControlMsg struct {
	sourceBoundary *sourceCheckpointBoundary
	Type           ControlType
	InputIndex     int
	CheckpointID   uint64
	EpochID        uint64
}

// OutputType identifies the kind of output message.
type OutputType uint8

const (
	OutputData      OutputType = iota // A data event to write downstream.
	OutputBarrier                     // A checkpoint barrier to forward downstream.
	OutputWatermark                   // A watermark to forward downstream.
	OutputEnd                         // An end-of-partition to forward downstream.
)

// OutputMsg carries messages from the operator chain to output writers.
type OutputMsg struct {
	SideOutput string // Empty selects the main output.
	Type       OutputType
	Event      Event                          // Valid when Type == OutputData.
	Barrier    *protocol.CheckpointBarrierMsg // Valid when Type == OutputBarrier.
	Watermark  *protocol.WatermarkMsg         // Valid when Type == OutputWatermark.
	End        *protocol.EndOfPartitionMsg    // Valid when Type == OutputEnd.
}

// WithSideOutput routes an operator's result to a named output, bypassing the
// remaining operators in its main-output chain. The tag is consumed by routing.
func WithSideOutput(event Event, tag string) Event { event.sideOutput = tag; return event }

// SideOutputTag reports an operator result's routing tag before it is consumed.
func (e Event) SideOutputTag() string { return e.sideOutput }
