package transport

import (
	"errors"
	"fmt"
	"io"
)

var ErrTargetTaskRejected = errors.New("transport: target task rejected stream")

// watchRejection owns the reverse direction of a mux-managed sender. Successful
// data streams have no reverse messages; their normal EOF is handled by the
// output owner, so it must not race that owner's final EOP write.
func (fs *FrameStream) watchRejection() {
	// Reuse framing, corruption thresholds and unknown-message handling. A
	// successful sender receives no reverse frame, only eventual EOF.
	message, err := fs.readMessage(false)
	fs.mu.Lock()
	fs.senderMessage = message
	fs.senderError = err
	fs.mu.Unlock()
	close(fs.senderReadDone)
}

func (fs *FrameStream) closedError() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.failure != nil {
		return fs.failure
	}
	return fmt.Errorf("transport: stream closed")
}

func (fs *FrameStream) readSenderResult() (any, error) {
	<-fs.senderReadDone
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.senderReadDelivered {
		return nil, io.EOF
	}
	fs.senderReadDelivered = true
	return fs.senderMessage, fs.senderError
}
