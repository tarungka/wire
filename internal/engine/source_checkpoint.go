package engine

import (
	"context"
	"errors"
)

// CheckpointTrigger requests a source boundary. The coordinator owns identity
// allocation and must fence requests to the current task execution epoch.
type CheckpointTrigger struct{ CheckpointID, EpochID uint64 }

type sourceCheckpointBoundary struct {
	state       []byte
	stateHandle bool
	done        chan struct{}
}

type sourceCheckpointInput struct {
	watermarks *sourceWatermarkQueue
	requests   <-chan CheckpointTrigger
	source     SourceOperator
	aligner    *BarrierAligner
	control    chan<- ControlMsg
	last       checkpointIdentity // Highest accepted trigger; owned by source reader.
}

// atBoundary runs only between fully dispatched batches. Until the chain
// releases done, no subsequent ReadBatch may advance the captured source state.
func (s *sourceCheckpointInput) atBoundary(intake, processing context.Context) error {
	select {
	case request, ok := <-s.requests:
		if !ok {
			s.requests = nil
			return nil
		}
		if request.CheckpointID == 0 {
			return errors.New("source checkpoint ID must be nonzero")
		}
		// WatchCommands and heartbeat fallback can redeliver a command. Never
		// capture newer source state under an already-used snapshot identity.
		if request.EpochID < s.last.epoch || (request.EpochID == s.last.epoch && request.CheckpointID <= s.last.id) {
			return nil
		}
		s.last = checkpointIdentity{request.CheckpointID, request.EpochID}
		if s.aligner.IsRetired(request.CheckpointID, request.EpochID) {
			return nil
		}
		if s.watermarks != nil {
			// Freeze periodic boundaries with source offsets until the chain
			// snapshots its operators and forwards the checkpoint barrier.
			s.watermarks.mu.Lock()
			defer s.watermarks.mu.Unlock()
		}
		var state []byte
		var typed bool
		if err := invokeOperator(func() error {
			data, isTyped, err := captureOperatorCheckpoint(s.source, request.CheckpointID)
			typed = isTyped
			state = append([]byte(nil), data...)
			return err
		}); err != nil {
			return err
		}
		boundary := &sourceCheckpointBoundary{state: state, stateHandle: typed, done: make(chan struct{})}
		s.aligner.OnBarrier(0, request.CheckpointID, request.EpochID)
		select {
		case s.control <- ControlMsg{Type: CtrlBarrierReceived, InputIndex: 0, CheckpointID: request.CheckpointID, EpochID: request.EpochID, sourceBoundary: boundary}:
		case <-processing.Done():
			return processing.Err()
		case <-intake.Done():
			return intake.Err()
		}
		select {
		case <-boundary.done:
			return nil
		case <-processing.Done():
			return processing.Err()
		case <-intake.Done():
			return intake.Err()
		}
	default:
		return nil
	}
}
