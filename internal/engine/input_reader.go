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
	return runInputReaderWithReport(ctx, inputIndex, stream, eventCh, controlCh, aligner, tracker, log, stream.ReportBufferUsage)
}

func runInputReaderWithReport(
	ctx context.Context,
	inputIndex int,
	stream *transport.FrameStream,
	eventCh chan<- Event,
	controlCh chan<- ControlMsg,
	aligner *BarrierAligner,
	tracker *InputWatermarkTracker,
	log zerolog.Logger,
	reportUsage func(int, int) error,
) error {
	return runInputReaderWithContexts(ctx, ctx, inputIndex, stream, eventCh, controlCh, aligner, tracker, log, reportUsage)
}

// runInputReaderWithContexts stops network intake independently of dispatching
// already-read messages. The dispatch context bounds graceful draining.
func runInputReaderWithContexts(
	intakeCtx context.Context,
	ctx context.Context,
	inputIndex int,
	stream *transport.FrameStream,
	eventCh chan<- Event,
	controlCh chan<- ControlMsg,
	aligner *BarrierAligner,
	tracker *InputWatermarkTracker,
	log zerolog.Logger,
	reportUsage func(int, int) error,
) error {
	ctx, cancelDispatch := context.WithCancel(ctx)
	defer cancelDispatch()
	// Keep a bounded read-ahead queue. Count the producer's in-flight send
	// as an occupied slot; serialized reports sample the latest count, preventing
	// stale pause signals from arriving after the queue has drained.
	readerCtx, cancelReader := context.WithCancel(intakeCtx)
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
		return reportUsage(int(occupancy.Load()), queueCapacity+1)
	}
	readerDone := make(chan struct{})
	closeDone := make(chan struct{})
	stopClose := context.AfterFunc(readerCtx, func() { defer taskGoroutineStarted(ctx)(); _ = stream.Close(); close(closeDone) })
	defer func() {
		if !stopClose() {
			<-closeDone
		}
	}()
	defer func() { cancelReader(); cancelDispatch(); _ = stream.Close(); <-readerDone }()
	go func() {
		defer taskGoroutineStarted(ctx)()
		defer close(readerDone)
		defer close(msgCh)
		for {
			select {
			case slots <- struct{}{}:
			case <-readerCtx.Done():
				return
			}
			msg, err := stream.ReadMessage()
			if err != nil && readerCtx.Err() != nil {
				return
			}
			occupancy.Add(1)
			if _, ok := msg.(*protocol.DataRecordMsg); ok && err == nil && tracker.ordered {
				tracker.recordQueued(inputIndex)
			}
			if reportErr := report(); reportErr != nil {
				log.Warn().Err(reportErr).Int("input", inputIndex).Msg("input flow-control report failed")
			}
			select {
			case msgCh <- readResult{msg, err}:
			case <-ctx.Done():
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
		case received, ok := <-msgCh:
			if !ok {
				return nil
			}
			result = received
			occupancy.Add(-1)
			<-slots
			if err := report(); err != nil {
				log.Warn().Err(err).Int("input", inputIndex).Msg("input flow-control report failed")
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
			if tracker.ordered {
				event.inputActivity = &inputActivity{tracker: tracker, input: inputIndex}
			} else {
				tracker.RecordActivity(inputIndex)
			}
			buffered, err := aligner.BufferAlignedEvent(ctx, inputIndex, event)
			if err != nil {
				return err
			}
			if !buffered {
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
			if err := aligner.WaitForPriorAlignment(ctx, inputIndex, m.CheckpointID); err != nil {
				return err
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
			if !tracker.ordered {
				tracker.AdvanceWatermark(inputIndex, m.Timestamp)
				continue
			}
			event := Event{inputWatermark: &inputWatermarkBoundary{tracker: tracker, input: inputIndex, timestamp: m.Timestamp}}
			buffered, err := aligner.BufferAlignedEvent(ctx, inputIndex, event)
			if err != nil {
				return err
			}
			if !buffered {
				select {
				case eventCh <- event:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

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
