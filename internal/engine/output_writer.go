package engine

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/transport"
)

// runOutputWriter reads from outputCh and writes protocol messages to the
// downstream FrameStream. It exits when outputCh is closed or ctx is cancelled.
// Natural backpressure: when downstream Yamux window fills, WriteMessage blocks,
// outputCh fills, and the operator chain blocks on send.
func runOutputWriter(ctx context.Context, stream *transport.FrameStream, outputCh <-chan OutputMsg, log zerolog.Logger) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-outputCh:
			if !ok {
				log.Debug().Msg("output channel closed, writer exiting")
				return nil
			}
			if err := writeOutputMsg(stream, msg); err != nil {
				log.Error().Err(err).Msg("failed to write output message")
				return err
			}
		}
	}
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
