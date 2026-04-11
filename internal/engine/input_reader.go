package engine

import (
	"context"
	"io"
	"runtime"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/transport"
)

// readResult holds the result of a single ReadMessage call.
type readResult struct {
	msg any
	err error
}

// runInputReader reads messages from an upstream FrameStream and dispatches
// them to the appropriate channels. One goroutine runs per input stream.
//
// Data records go to eventCh (or are side-buffered if the barrier aligner
// indicates this input is aligning). Checkpoint barriers go through the
// aligner then to controlCh. Watermarks advance the shared atomic watermark.
// EndOfPartition sends a control message and returns.
func runInputReader(
	ctx context.Context,
	inputIndex int,
	stream *transport.FrameStream,
	eventCh chan<- Event,
	controlCh chan<- ControlMsg,
	aligner *BarrierAligner,
	tracker *InputWatermarkTracker,
	log zerolog.Logger,
) error {
	// Use a goroutine to read from the stream, enabling context cancellation
	// even when ReadMessage blocks on network I/O.
	msgCh := make(chan readResult, 1)
	go func() {
		for {
			msg, err := stream.ReadMessage()
			msgCh <- readResult{msg, err}
			if err != nil {
				return
			}
		}
	}()

	for {
		var result readResult
		select {
		case <-ctx.Done():
			return nil
		case result = <-msgCh:
		}

		if result.err != nil {
			if result.err == io.EOF || ctx.Err() != nil {
				return nil
			}
			log.Error().Err(result.err).Int("input", inputIndex).Msg("input reader error")
			return result.err
		}

		switch m := result.msg.(type) {
		case *protocol.DataRecordMsg:
			event := EventFromProto(m)
			tracker.RecordActivity(inputIndex)
			if aligner.IsAligning(inputIndex) {
				if err := aligner.BufferEvent(ctx, inputIndex, event); err != nil {
					log.Warn().Err(err).Int("input", inputIndex).Msg("side buffer full, blocking")
					// Yield and retry when buffer is full.
					for {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						runtime.Gosched()
						if err := aligner.BufferEvent(ctx, inputIndex, event); err == nil {
							break
						}
					}
				}
			} else {
				select {
				case eventCh <- event:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

		case *protocol.CheckpointBarrierMsg:
			aligner.OnBarrier(inputIndex, m.CheckpointID, m.EpochID)
			ctrl := ControlMsg{
				Type:         CtrlBarrierReceived,
				InputIndex:   inputIndex,
				CheckpointID: m.CheckpointID,
				EpochID:      m.EpochID,
			}
			select {
			case controlCh <- ctrl:
			case <-ctx.Done():
				return ctx.Err()
			}

		case *protocol.WatermarkMsg:
			tracker.AdvanceWatermark(inputIndex, m.Timestamp)

		case *protocol.EndOfPartitionMsg:
			ctrl := ControlMsg{
				Type:       CtrlEndOfPartition,
				InputIndex: inputIndex,
			}
			select {
			case controlCh <- ctrl:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}
	}
}
