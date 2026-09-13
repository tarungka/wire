package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/rs/zerolog"
)

const (
	sigChSize = 32
)

// signalContext returns a child context that is canceled when any of sigs is
// received. The returned cancel function also unregisters the signal handler.
func signalContext(parent context.Context, log zerolog.Logger, sigs ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	sigCh := make(chan os.Signal, sigChSize)
	signal.Notify(sigCh, sigs...)

	go func() {
		select {
		case sig := <-sigCh:
			log.Info().Str("signal", sig.String()).Msg("received signal")
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, func() {
		signal.Stop(sigCh)
		cancel()
	}
}
