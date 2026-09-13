package rpc

import (
	"context"

	"github.com/hashicorp/yamux"
)

// openStreamContext bounds pending opens to one per client. Cancellation never
// closes the shared session: a late-opened stream is closed by its owner.
func (c *Client) openStreamContext(ctx context.Context) (*yamux.Stream, error) {
	c.openOnce.Do(func() { c.openGate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case c.openGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	type result struct {
		stream *yamux.Stream
		err    error
	}
	results := make(chan result)
	go func() {
		defer func() { <-c.openGate }()
		stream, err := c.session.OpenStream()
		select {
		case results <- result{stream, err}:
		case <-ctx.Done():
			if stream != nil {
				_ = stream.Close()
			}
		}
	}()
	select {
	case result := <-results:
		if err := ctx.Err(); err != nil {
			if result.stream != nil {
				_ = result.stream.Close()
			}
			return nil, err
		}
		return result.stream, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
