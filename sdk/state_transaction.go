package sdk

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/tarungka/wire/internal/engine"
)

// invocationState buffers one Process invocation, including due timer callbacks.
// A failed/retried/dropped record must not leave its managed-state mutations behind.
type invocationState struct {
	engine.StateBackend
	changes map[string]engine.StateMutation
}

func newInvocationState(backend engine.StateBackend) *invocationState {
	return &invocationState{StateBackend: backend, changes: make(map[string]engine.StateMutation)}
}
func (s *invocationState) Get(key []byte) ([]byte, error) {
	if change, ok := s.changes[string(key)]; ok {
		if change.Delete {
			return nil, engine.ErrKeyNotFound
		}
		return bytes.Clone(change.Value), nil
	}
	return s.StateBackend.Get(key)
}
func (s *invocationState) Put(key, value []byte) error {
	return s.ApplyBatch([]engine.StateMutation{{Key: key, Value: value}})
}
func (s *invocationState) Delete(key []byte) error {
	return s.ApplyBatch([]engine.StateMutation{{Key: key, Delete: true}})
}
func (s *invocationState) ApplyBatch(changes []engine.StateMutation) error {
	for _, change := range changes {
		change.Key = bytes.Clone(change.Key)
		change.Value = bytes.Clone(change.Value)
		s.changes[string(change.Key)] = change
	}
	return nil
}
func (s *invocationState) commit() error {
	if len(s.changes) == 0 {
		return nil
	}
	backend, ok := s.StateBackend.(engine.BatchedStateBackend)
	if !ok {
		return fmt.Errorf("sdk: managed Process requires atomic state batches")
	}
	changes := make([]engine.StateMutation, 0, len(s.changes))
	for _, change := range s.changes {
		changes = append(changes, change)
	}
	return backend.ApplyBatch(changes)
}
func (s *invocationState) NewIterator(prefix []byte) engine.StateIterator {
	values := make(map[string][]byte)
	it := s.StateBackend.NewIterator(prefix)
	for it.Next() {
		values[string(it.Key())] = bytes.Clone(it.Value())
	}
	it.Close()
	for key, change := range s.changes {
		if !bytes.HasPrefix([]byte(key), prefix) {
			continue
		}
		if change.Delete {
			delete(values, key)
		} else {
			values[key] = bytes.Clone(change.Value)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return &invocationIterator{keys: keys, values: values, index: -1}
}

type invocationIterator struct {
	keys   []string
	values map[string][]byte
	index  int
}

func (it *invocationIterator) Next() bool { it.index++; return it.index < len(it.keys) }
func (it *invocationIterator) Key() []byte {
	if it.index < 0 || it.index >= len(it.keys) {
		return nil
	}
	return []byte(it.keys[it.index])
}
func (it *invocationIterator) Value() []byte {
	if it.index < 0 || it.index >= len(it.keys) {
		return nil
	}
	return it.values[it.keys[it.index]]
}
func (it *invocationIterator) Close() { it.keys = nil; it.values = nil }
