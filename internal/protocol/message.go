package protocol

// Message type discriminators per WIP-01 Section 3.2.
const (
	MsgTypeHandshake         uint8 = 0x00
	MsgTypeDataRecord        uint8 = 0x01
	MsgTypeCheckpointBarrier uint8 = 0x02
	MsgTypeWatermark         uint8 = 0x03
	MsgTypeEndOfPartition    uint8 = 0x04
	MsgTypeBackpressure      uint8 = 0x05
	MsgTypeRecordBatch       uint8 = 0x06 // Reserved for future use.
)

// Backpressure states per WIP-01 Section 3.7.
const (
	BackpressureResume uint8 = 0x00
	BackpressurePause  uint8 = 0x01
)

// EndOfPartition reason codes per WIP-01 Section 3.6.
const (
	EndReasonExhausted uint8 = 0x00
	EndReasonCanceling uint8 = 0x01
	EndReasonError     uint8 = 0x02
)

// Feature flags for Handshake negotiation per WIP-01 Section 3.8.
const (
	FeatureCRC32C      uint32 = 1 << 0
	FeatureCompression uint32 = 1 << 1
)

// Protocol version constants.
const (
	CurrentProtocolVersion uint16 = 1
	MinProtocolVersion     uint16 = 1
)

// HandshakeSourceID is the SourceID used in EndOfPartition messages
// sent during handshake rejection.
const HandshakeSourceID = "handshake"

// HandshakeMsg is sent as the first frame on every new stream for version and feature negotiation.
type HandshakeMsg struct {
	ProtocolVersion uint16 `codec:"v"`
	MinVersion      uint16 `codec:"min_v"`
	Features        uint32 `codec:"f,omitempty"`
}

// DataRecordMsg carries a single event through the stream processing pipeline.
type DataRecordMsg struct {
	Key       []byte            `codec:"k"`
	Value     []byte            `codec:"v"`
	EventTime int64             `codec:"t"`
	Headers   map[string][]byte `codec:"h,omitempty"`
}

// CheckpointBarrierMsg divides the stream into epochs for checkpoint/recovery.
type CheckpointBarrierMsg struct {
	CheckpointID uint64 `codec:"c"`
	EpochID      uint64 `codec:"e"`
	Timestamp    int64  `codec:"ts"`
}

// WatermarkMsg declares that no future events with EventTime < Timestamp will arrive.
type WatermarkMsg struct {
	Timestamp int64  `codec:"t"`
	SourceID  string `codec:"s"`
}

// EndOfPartitionMsg signals that the upstream partition is exhausted.
type EndOfPartitionMsg struct {
	SourceID string `codec:"s"`
	Reason   uint8  `codec:"r"`
}

// BackpressureMsg is an explicit flow control signal from downstream to upstream.
type BackpressureMsg struct {
	StreamID    uint32  `codec:"id"`
	State       uint8   `codec:"st"`
	BufferUsage float32 `codec:"bu,omitempty"`
}

// MsgTypeName returns a human-readable name for the given message type.
func MsgTypeName(mt uint8) string {
	switch mt {
	case MsgTypeHandshake:
		return "Handshake"
	case MsgTypeDataRecord:
		return "DataRecord"
	case MsgTypeCheckpointBarrier:
		return "CheckpointBarrier"
	case MsgTypeWatermark:
		return "Watermark"
	case MsgTypeEndOfPartition:
		return "EndOfPartition"
	case MsgTypeBackpressure:
		return "Backpressure"
	case MsgTypeRecordBatch:
		return "RecordBatch"
	default:
		return "Unknown"
	}
}
