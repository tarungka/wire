package worker

import (
	"context"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

func (w *Worker) confirmCoordinatorContact() { w.confirmCoordinatorContactAt(time.Now()) }

func (w *Worker) confirmCoordinatorContactAt(sent time.Time) {
	w.mu.Lock()
	w.lastCoordinatorContact = sent
	w.mu.Unlock()
}

// This watchdog spans failed dials, registrations and session reconnects.
// A heartbeat sender alone cannot enforce a process lifetime contact deadline.
func (w *Worker) watchCoordinatorContact(ctx context.Context, cancel context.CancelCauseFunc) {
	timeout := w.contactTimeout()
	ticker := time.NewTicker(max(time.Millisecond, min(timeout/10, 100*time.Millisecond)))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.mu.Lock()
			expired := time.Since(w.lastCoordinatorContact) >= timeout
			if expired {
				w.stopping = true
			}
			w.mu.Unlock()
			if expired {
				cancel(ErrCoordinatorContactLost)
				w.fenceForContactLoss()
				return
			}
		}
	}
}

// Stop new admissions and close all data/RPC sessions before joining user work.
// Cooperative task cleanup may take its drain budget; no live data connection
// may keep forwarding the old attempt after coordinator authority expires.
func (w *Worker) fenceForContactLoss() {
	w.mu.Lock()
	w.stopping = true
	for _, h := range w.tasks {
		h.cancel()
	}
	data, session, closeReplica := w.data, w.session, w.closeReplica
	w.mu.Unlock()
	if data != nil {
		_ = data.Close()
	}
	if session != nil {
		_ = session.Close()
	}
	if closeReplica != nil {
		closeReplica()
	}
}

func (w *Worker) contactTimeout() time.Duration {
	if w.cfg.HeartbeatTimeout > 0 {
		return w.cfg.HeartbeatTimeout
	}
	return rpc.DefaultCoordinatorContactTimeout
}
