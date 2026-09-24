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
	groups := []OutputGroup{{KeyGroups: count}}
	for i := range streams {
		groups[0].Streams = append(groups[0].Streams, i)
	}
	return runGroupedOutputRouter(ctx, streams, outputCh, log, groups)
}

// OutputGroup isolates routing decisions for one graph edge. Data goes only to
// matching tags; checkpoint/watermark/end fences still visit every stream.
type OutputGroup struct {
	Broadcast  bool
	SideOutput string
	Streams    []int
	KeyGroups  int
}

func runGroupedOutputRouter(ctx context.Context, streams []*transport.FrameStream, outputCh <-chan OutputMsg, log zerolog.Logger, groups []OutputGroup) error {
	used := make(map[int]bool)
	for _, group := range groups {
		if len(group.Streams) == 0 && len(streams) > 0 {
			return fmt.Errorf("empty output group")
		}
		if group.Broadcast && group.KeyGroups != 0 {
			return fmt.Errorf("broadcast output cannot also use keyed routing")
		}
		if group.KeyGroups != 0 {
			if err := (keygroup.Config{NumKeyGroups: group.KeyGroups, Parallelism: len(group.Streams)}).Validate(); err != nil {
				return fmt.Errorf("output key routing: %w", err)
			}
		}
		for _, index := range group.Streams {
			if index < 0 || index >= len(streams) || used[index] {
				return fmt.Errorf("invalid or repeated output stream %d", index)
			}
			used[index] = true
		}
	}
	if len(used) != len(streams) {
		return fmt.Errorf("ungrouped output stream")
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
		next := make([]int, len(groups))
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
					for gi, group := range groups {
						if group.SideOutput != message.SideOutput || len(group.Streams) == 0 {
							continue
						}
						if group.Broadcast {
							for _, target := range group.Streams {
								select {
								case queues[target] <- work{message: message}:
								case <-writerCtx.Done():
									return writerCtx.Err()
								}
							}
							continue
						}
						target := next[gi]
						if group.KeyGroups != 0 {
							target = keygroup.AssignedTask(keygroup.KeyGroup(message.Event.Key, group.KeyGroups), group.KeyGroups, len(group.Streams))
						}
						select {
						case queues[group.Streams[target]] <- work{message: message}:
						case <-writerCtx.Done():
							return writerCtx.Err()
						}
						next[gi] = (next[gi] + 1) % len(group.Streams)
					}
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
