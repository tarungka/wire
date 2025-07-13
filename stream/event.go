package stream

// Event represents a data record or a control event in the stream.
type Event interface{}

// Barrier is a special event that signals a checkpoint.
type Barrier struct {
	CheckpointID int64
}
