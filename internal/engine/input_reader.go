package engine

import (
	"context"
	"io"
	"sync"
	"sync/atomic"

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
	// Keep a bounded read-ahead queue. Count the producer's in-flight send
	// as an occupied slot; serialized reports sample the latest count, preventing
	// stale pause signals from arriving after the queue has drained.
	readerCtx, cancelReader := context.WithCancel(ctx)
	defer cancelReader()
	const queueCapacity = 4
	msgCh := make(chan readResult, queueCapacity)
	// A channel receive frees buffer capacity before the consumer decrements
	// occupancy. Reserve explicit slots so a producer cannot reuse that capacity
	// until the old message has also left the reported count.
	slots := make(chan struct{}, queueCapacity+1)
	var occupancy atomic.Int32
	var reportMu sync.Mutex
	report := func() error {
		reportMu.Lock()
		defer reportMu.Unlock()
		return stream.ReportBufferUsage(int(occupancy.Load()), queueCapacity+1)
	}
	readerDone := make(chan struct{})
	defer func() { cancelReader(); _ = stream.Close(); <-readerDone }()
	go func() {
		defer close(readerDone)
		for {
			select {
			case slots <- struct{}{}:
			case <-readerCtx.Done():
				return
			}
			msg, err := stream.ReadMessage()
			occupancy.Add(1)
			if reportErr := report(); err == nil {
				err = reportErr
			}
			select {
			case msgCh <- readResult{msg, err}:
			case <-readerCtx.Done():
				return
			}
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
			occupancy.Add(-1)
			<-slots
			if err := report(); err != nil && result.err == nil {
				return err
			}
		}

		if result.err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if result.err == io.EOF {
				return io.ErrUnexpectedEOF
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
					// Spin-wait with context check when buffer is full.
					for {
						if ctx.Err() != nil {
							return ctx.Err()
						}
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
			if stream.IsCheckpointCompleted(m.CheckpointID) {
				continue
			}
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
