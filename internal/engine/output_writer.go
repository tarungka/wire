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
// TaskSlot keeps this output context alive on successful chain completion so
// terminal frames drain. External cancellation permits a bounded drain;
// failures cancel writes immediately.
func runOutputWriter(ctx context.Context, stream *transport.FrameStream, outputCh <-chan OutputMsg, log zerolog.Logger) error {
	for msg := range outputCh {
		if err := writeOutputMsgContext(ctx, stream, msg); err != nil {
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
	return writeOutputMsgContext(context.Background(), stream, msg)
}

func writeOutputMsgContext(ctx context.Context, stream *transport.FrameStream, msg OutputMsg) error {
	switch msg.Type {
	case OutputData:
		return stream.WriteMessageContext(ctx, msg.Event.ToProto())
	case OutputBarrier:
		return stream.WriteMessageContext(ctx, msg.Barrier)
	case OutputWatermark:
		return stream.WriteMessageContext(ctx, msg.Watermark)
	case OutputEnd:
		return stream.WriteMessageContext(ctx, msg.End)
	default:
		return nil
	}
}

// runOutputRouter gives one goroutine ownership of partition ordering. Data
// records are distributed round-robin; barriers, watermarks, and termination
// are broadcast after all preceding records have been written. Sharing a
// receive channel among writers would deliver each control frame to only one
// partition and could reorder it relative to another writer's pending record.
func runOutputRouter(ctx context.Context, streams []*transport.FrameStream, outputCh <-chan OutputMsg, log zerolog.Logger) error {
	next := 0
	for msg := range outputCh {
		if len(streams) == 0 {
			continue
		}
		if msg.Type == OutputData {
			if err := writeOutputMsgContext(ctx, streams[next], msg); err != nil {
				return err
			}
			next = (next + 1) % len(streams)
			continue
		}
		for index, stream := range streams {
			if err := writeOutputMsgContext(ctx, stream, msg); err != nil {
				log.Error().Err(err).Int("output", index).Msg("failed to broadcast output control message")
				return err
			}
		}
	}
	return nil
}
