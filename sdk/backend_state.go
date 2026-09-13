package sdk

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tarungka/wire/internal/engine"
)

// State APIs have no error return. Preserve the first storage error and fail
// the Process invocation before emitting its results instead of losing it.
type backendProcessContext struct {
	key     []byte
	backend engine.StateBackend
	err     error
}

func (c *backendProcessContext) Key() []byte { return append([]byte(nil), c.key...) }
func (c *backendProcessContext) stateKey(kind byte, name string) []byte {
	key := []byte{kind}
	key = binary.BigEndian.AppendUint64(key, uint64(len(c.key)))
	key = append(key, c.key...)
	key = binary.BigEndian.AppendUint64(key, uint64(len(name)))
	return append(key, name...)
}
func (c *backendProcessContext) fail(err error) {
	if err != nil && c.err == nil {
		c.err = fmt.Errorf("sdk state: %w", err)
	}
}
func (c *backendProcessContext) get(key []byte) []byte {
	if c.err != nil {
		return nil
	}
	value, err := c.backend.Get(key)
	if errors.Is(err, engine.ErrKeyNotFound) {
		return nil
	}
	c.fail(err)
	return value
}
func (c *backendProcessContext) put(key, value []byte) {
	if c.err == nil {
		c.fail(c.backend.Put(key, value))
	}
}
func (c *backendProcessContext) remove(key []byte) {
	if c.err != nil {
		return
	}
	err := c.backend.Delete(key)
	if !errors.Is(err, engine.ErrKeyNotFound) {
		c.fail(err)
	}
}
func (c *backendProcessContext) GetValueState(name string) ValueState {
	return &backendValueState{context: c, key: c.stateKey('v', name)}
}
func (c *backendProcessContext) GetListState(name string) ListState {
	return &backendListState{context: c, key: c.stateKey('l', name)}
}
func (c *backendProcessContext) GetMapState(name string) MapState {
	return &backendMapState{context: c, prefix: c.stateKey('m', name)}
}

type backendValueState struct {
	context *backendProcessContext
	key     []byte
}

func (s *backendValueState) Get() []byte      { return s.context.get(s.key) }
func (s *backendValueState) Set(value []byte) { s.context.put(s.key, value) }
func (s *backendValueState) Clear()           { s.context.remove(s.key) }

type backendListState struct {
	context *backendProcessContext
	key     []byte
}

func (s *backendListState) Get() [][]byte {
	data := s.context.get(s.key)
	if data == nil {
		return nil
	}
	var values [][]byte
	s.context.fail(json.Unmarshal(data, &values))
	return values
}
func (s *backendListState) Add(value []byte) {
	values := append(s.Get(), value)
	data, err := json.Marshal(values)
	s.context.fail(err)
	s.context.put(s.key, data)
}
func (s *backendListState) Clear() { s.context.remove(s.key) }

type backendMapState struct {
	context *backendProcessContext
	prefix  []byte
}

func (s *backendMapState) key(key string) []byte {
	return append(append([]byte(nil), s.prefix...), key...)
}
func (s *backendMapState) Get(key string) []byte        { return s.context.get(s.key(key)) }
func (s *backendMapState) Put(key string, value []byte) { s.context.put(s.key(key), value) }
func (s *backendMapState) Delete(key string)            { s.context.remove(s.key(key)) }
func (s *backendMapState) Keys() []string {
	if s.context.err != nil {
		return nil
	}
	it := s.context.backend.NewIterator(s.prefix)
	defer it.Close()
	var keys []string
	for it.Next() {
		keys = append(keys, string(it.Key()[len(s.prefix):]))
	}
	return keys
}
func (s *backendMapState) Clear() {
	for _, key := range s.Keys() {
		s.Delete(key)
	}
}
