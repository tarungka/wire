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
		n, err := r.stream.raw.Read(p)
		r.stream.readOffset += uint64(n)
		return n, err
	}
	n, err := r.stream.raw.Read(p[:1])
	r.stream.readOffset += uint64(n)
	if n > 0 {
		r.started = true
		timeout := r.stream.cfg.FrameReadTimeout
		if timeout <= 0 {
			timeout = DefaultConnectionWriteTimeout
		}
		_ = r.stream.raw.SetReadDeadline(time.Now().Add(timeout))
		// Close may race the first byte and its completion deadline. Preserve
		// cancellation rather than replacing Close's immediate deadline.
		select {
		case <-r.stream.done:
			_ = r.stream.raw.SetReadDeadline(time.Now())
		default:
		}
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
