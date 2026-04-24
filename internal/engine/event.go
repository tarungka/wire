package engine

import (
	"github.com/tarungka/wire/internal/protocol"
)

// Event is the internal representation of a data record flowing through the
// operator chain.
type Event struct {
	Key       []byte
	Value     []byte
	EventTime int64
	Headers   map[string][]byte
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
)

// ControlMsg carries control signals from input readers to the operator chain.
type ControlMsg struct {
	Type         ControlType
	InputIndex   int
	CheckpointID uint64
	EpochID      uint64
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
	Type      OutputType
	Event     Event                          // Valid when Type == OutputData.
	Barrier   *protocol.CheckpointBarrierMsg // Valid when Type == OutputBarrier.
	Watermark *protocol.WatermarkMsg         // Valid when Type == OutputWatermark.
	End       *protocol.EndOfPartitionMsg    // Valid when Type == OutputEnd.
}
