package engine

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/keygroup"
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

// runOutputRouter assigns data to dedicated bounded per-stream writers.
// A control-frame fence waits for every writer before dispatching later data,
// preserving barrier/watermark/EOP ordering across all partitions.
func runOutputRouter(ctx context.Context, streams []*transport.FrameStream, outputCh <-chan OutputMsg, log zerolog.Logger, keyGroups ...int) error {
	count := 0
	if len(keyGroups) > 0 {
		count = keyGroups[0]
	}
	if count != 0 {
		if err := (keygroup.Config{NumKeyGroups: count, Parallelism: len(streams)}).Validate(); err != nil {
			return fmt.Errorf("output key routing: %w", err)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	group, writerCtx := errgroup.WithContext(ctx)
	type work struct {
		message   OutputMsg
		completed chan<- struct{}
	}
	queues := make([]chan work, len(streams))
	for i, stream := range streams {
		queue := make(chan work, 1)
		queues[i] = queue
		group.Go(func() error {
			defer taskGoroutineStarted(writerCtx)()
			for {
				select {
				case <-writerCtx.Done():
					return writerCtx.Err()
				case item, ok := <-queue:
					if !ok {
						return nil
					}
					if err := writeOutputMsgContext(writerCtx, stream, item.message); err != nil {
						return err
					}
					if item.completed != nil {
						select {
						case item.completed <- struct{}{}:
						case <-writerCtx.Done():
							return writerCtx.Err()
						}
					}
				}
			}
		})
	}
	dispatch := func() error {
		next := 0
		for {
			select {
			case <-writerCtx.Done():
				return writerCtx.Err()
			case message, ok := <-outputCh:
				if !ok {
					return nil
				}
				if len(queues) == 0 {
					continue
				}
				if message.Type == OutputData {
					target := next
					if count != 0 {
						target = keygroup.AssignedTask(keygroup.KeyGroup(message.Event.Key, count), count, len(queues))
					}
					select {
					case queues[target] <- work{message: message}:
					case <-writerCtx.Done():
						return writerCtx.Err()
					}
					next = (next + 1) % len(queues)
					continue
				}
				completed := make(chan struct{}, len(queues))
				for _, queue := range queues {
					select {
					case queue <- work{message: message, completed: completed}:
					case <-writerCtx.Done():
						return writerCtx.Err()
					}
				}
				for range queues {
					select {
					case <-completed:
					case <-writerCtx.Done():
						return writerCtx.Err()
					}
				}
			}
		}
	}
	err := dispatch()
	if err != nil {
		cancel()
	}
	for _, queue := range queues {
		close(queue)
	}
	if writeErr := group.Wait(); writeErr != nil {
		return writeErr
	}
	return err
}
