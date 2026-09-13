package transport

import (
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// frameReader allows an idle stream indefinitely, but once a frame starts its
// entire remainder must arrive within FrameReadTimeout. This bounds truncated
// frames without confusing quiet inputs with failed peers.
type frameReader struct {
	stream  *FrameStream
	started bool
}

func (r *frameReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.started {
		return r.stream.raw.Read(p)
	}
	n, err := r.stream.raw.Read(p[:1])
	if n > 0 {
		r.started = true
		timeout := r.stream.cfg.FrameReadTimeout
		if timeout <= 0 {
			timeout = DefaultConnectionWriteTimeout
		}
		_ = r.stream.raw.SetReadDeadline(time.Now().Add(timeout))
	}
	return n, err
}

func (fs *FrameStream) readFrame() (protocol.Frame, error) {
	defer func() {
		select {
		case <-fs.done:
		default:
			_ = fs.raw.SetReadDeadline(time.Time{})
		}
	}()
	return protocol.ReadFrame(&frameReader{stream: fs}, fs.cfg.MaxFrameSize)
}
