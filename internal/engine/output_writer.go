package engine

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/transport"
)

// runOutputWriter reads from outputCh and writes protocol messages to the
// downstream FrameStream. It exits when outputCh is closed.
// Natural backpressure: when downstream Yamux window fills, WriteMessage blocks,
// outputCh fills, and the operator chain blocks on send.
//
// This uses a simple `for range` loop instead of a select with ctx.Done() to
// avoid non-determinism: when the context is cancelled while outputCh still has
// pending messages (e.g., a final EndOfPartition), Go's select picks randomly
// between ready channels, so ctx.Done() can win and the writer exits without
// writing remaining messages. The outputCh lifecycle is managed by producerWg
// in task_slot.go — all producers finish before outputCh is closed, so this
// loop naturally terminates after draining all messages.
func runOutputWriter(ctx context.Context, stream *transport.FrameStream, outputCh <-chan OutputMsg, log zerolog.Logger) error {
	for msg := range outputCh {
		if err := writeOutputMsg(stream, msg); err != nil {
			log.Error().Err(err).Msg("failed to write output message")
			return err
		}
	}
	log.Debug().Msg("output channel closed, writer exiting")
	return nil
}

// writeOutputMsg encodes an OutputMsg into the appropriate protocol message
// and writes it to the stream.
func writeOutputMsg(stream *transport.FrameStream, msg OutputMsg) error {
	switch msg.Type {
	case OutputData:
		return stream.WriteMessage(msg.Event.ToProto())
	case OutputBarrier:
		return stream.WriteMessage(msg.Barrier)
	case OutputWatermark:
		return stream.WriteMessage(msg.Watermark)
	case OutputEnd:
		return stream.WriteMessage(msg.End)
	default:
		return nil
	}
}
