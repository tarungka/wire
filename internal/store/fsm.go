package store

import (
	"io"
	"log"
	// "time"

	"github.com/hashicorp/raft"
)

// FSM is a wrapper around the Store which implements raft.FSM.
type FSM struct {
	s *Store
}

// NewFSM returns a new FSM.
func NewFSM(s *Store) *FSM {
	// TODO: Implementation truncated
	return nil
}

// Apply applies a Raft log entry to the Store.
func (f *FSM) Apply(l *raft.Log) interface{} {
	// TODO: Implementation truncated
	return nil
}

// Snapshot returns a Snapshot of the Store
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// Restore restores the Store from a snapshot.
func (f *FSM) Restore(rc io.ReadCloser) error {
	// TODO: Implementation truncated
	return nil
}

// FSMSnapshot is a wrapper around raft.FSMSnapshot which adds an optional
// Finalizer, instrumentation, and logging.
type FSMSnapshot struct {
	Finalizer func() error
	OnFailure func()

	raft.FSMSnapshot
	persistSucceeded bool
	// TODO: change this to zerolog
	logger *log.Logger
}

// Persist writes the snapshot to the given sink.
func (f *FSMSnapshot) Persist(sink raft.SnapshotSink) (retError error) {
	// TODO: Implementation truncated
	return nil
}

// Release performs any final cleanup once the Snapshot has been persisted.
func (f *FSMSnapshot) Release() {
	// TODO: Implementation truncated
}
