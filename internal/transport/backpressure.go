package transport

import (
	"fmt"
	"math"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// runControl consumes the single negotiated control stream independently of
// data reads, so a full data window cannot prevent a resume from arriving.
func (s *Session) runControl(cfg Config) {
	control := NewFrameStream(s.control, cfg)
	defer s.Close()
	for {
		msg, err := control.ReadMessage()
		if err != nil {
			return
		}
		bp, ok := msg.(*protocol.BackpressureMsg)
		if !ok || bp.State > protocol.BackpressurePause || math.IsNaN(float64(bp.BufferUsage)) || bp.BufferUsage < 0 || bp.BufferUsage > 1 {
			return
		}
		s.mu.Lock()
		stream := s.outputs[bp.StreamID]
		s.mu.Unlock()
		// Late signals for streams already closed have no effect.
		if stream != nil {
			stream.setPaused(bp.State == protocol.BackpressurePause)
		}
	}
}

func (fs *FrameStream) setPaused(paused bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if paused && fs.resume == nil {
		fs.resume = make(chan struct{})
	}
	if !paused && fs.resume != nil {
		close(fs.resume)
		fs.resume = nil
	}
}

// ReportBufferUsage applies the WIP-01 high/low watermarks to this input's
// downstream buffer. Call after enqueue and dequeue; intermediate occupancy
// preserves the current state to avoid oscillating pause/resume messages.
func (fs *FrameStream) ReportBufferUsage(used, capacity int) error {
	if capacity <= 0 || used < 0 || used > capacity {
		return fmt.Errorf("transport: invalid buffer occupancy")
	}
	if fs.session == nil || fs.sender {
		return fmt.Errorf("transport: backpressure requires a negotiated input stream")
	}
	fs.reportMu.Lock()
	defer fs.reportMu.Unlock()
	usage := float32(used) / float32(capacity)
	paused := fs.reportedPause
	if usage >= .8 {
		paused = true
	} else if usage <= .2 {
		paused = false
	}
	if paused == fs.reportedPause {
		return nil
	}
	state := protocol.BackpressureResume
	if paused {
		state = protocol.BackpressurePause
	}
	fs.session.controlWriteMu.Lock()
	timeout := fs.cfg.ConnectionWriteTimeout
	if timeout <= 0 {
		timeout = DefaultConnectionWriteTimeout
	}
	_ = fs.session.control.SetWriteDeadline(time.Now().Add(timeout))
	err := protocol.EncodeAndWriteFrame(fs.session.control, &protocol.BackpressureMsg{StreamID: fs.StreamID(), State: state, BufferUsage: usage})
	_ = fs.session.control.SetWriteDeadline(time.Time{})
	fs.session.controlWriteMu.Unlock()
	if err == nil {
		fs.reportedPause = paused
	}
	return err
}
