package engine

import (
	"context"
	"errors"
)

// CheckpointTrigger requests a source boundary. The coordinator owns identity
// allocation and must fence requests to the current task execution epoch.
type CheckpointTrigger struct{ CheckpointID, EpochID uint64 }

type sourceCheckpointBoundary struct {
	state []byte
	done  chan struct{}
}

type sourceCheckpointInput struct {
	requests <-chan CheckpointTrigger
	source   SourceOperator
	aligner  *BarrierAligner
	control  chan<- ControlMsg
	last     checkpointIdentity // Highest accepted trigger; owned by source reader.
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
		var state []byte
		if err := invokeOperator(func() error {
			data, err := s.source.Checkpoint(request.CheckpointID)
			state = append([]byte(nil), data...)
			return err
		}); err != nil {
			return err
		}
		boundary := &sourceCheckpointBoundary{state: state, done: make(chan struct{})}
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
