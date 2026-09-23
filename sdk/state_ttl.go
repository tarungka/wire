package sdk

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

// TTL is processing-time expiry refreshed by writes, never by reads. Expiry
// metadata is checkpointed alongside values. Reads lazily remove expired state.
func expiryKey(key []byte) []byte { return append([]byte{'x'}, key...) }
func (c *backendProcessContext) nowNanos() int64 {
	if c.clock != nil {
		return c.clock().UnixNano()
	}
	return time.Now().UnixNano()
}
func (c *backendProcessContext) stateExpired(key []byte) bool {
	expiry := c.get(expiryKey(key))
	if len(expiry) == 0 {
		return false
	}
	if len(expiry) != 8 {
		c.fail(fmt.Errorf("corrupt state expiry"))
		return true
	}
	if int64(binary.BigEndian.Uint64(expiry)) <= c.nowNanos() {
		c.removeState(key)
		return true
	}
	return false
}
func (c *backendProcessContext) getState(key []byte) []byte {
	if c.stateExpired(key) {
		return nil
	}
	return c.get(key)
}
func (c *backendProcessContext) putState(key, value []byte, ttl time.Duration) {
	if ttl < 0 {
		c.fail(fmt.Errorf("state TTL must be nonnegative"))
		return
	}
	if c.err != nil {
		return
	}
	expires := engine.StateMutation{Key: expiryKey(key), Delete: true}
	if ttl > 0 {
		now := c.nowNanos()
		if now > math.MaxInt64-int64(ttl) {
			c.fail(fmt.Errorf("state TTL expiry overflows timestamp"))
			return
		}
		expires.Delete = false
		expires.Value = binary.BigEndian.AppendUint64(nil, uint64(now+int64(ttl)))
	}
	c.mutateState([]engine.StateMutation{{Key: key, Value: value}, expires})
}
func (c *backendProcessContext) removeState(key []byte) {
	c.mutateState([]engine.StateMutation{{Key: key, Delete: true}, {Key: expiryKey(key), Delete: true}})
}
func (c *backendProcessContext) mutateState(mutations []engine.StateMutation) {
	if c.err != nil {
		return
	}
	backend, ok := c.backend.(engine.BatchedStateBackend)
	if !ok {
		c.fail(fmt.Errorf("managed state requires atomic batch support"))
		return
	}
	c.fail(backend.ApplyBatch(mutations))
}
func (s *backendValueState) WithTTL(ttl time.Duration) ValueState {
	copy := *s
	copy.ttl = ttl
	if ttl < 0 {
		s.context.fail(fmt.Errorf("state TTL must be nonnegative"))
	}
	return &copy
}
func (s *backendListState) WithTTL(ttl time.Duration) ListState {
	copy := *s
	copy.ttl = ttl
	if ttl < 0 {
		s.context.fail(fmt.Errorf("state TTL must be nonnegative"))
	}
	return &copy
}
func (s *backendMapState) WithTTL(ttl time.Duration) MapState {
	copy := *s
	copy.ttl = ttl
	if ttl < 0 {
		s.context.fail(fmt.Errorf("state TTL must be nonnegative"))
	}
	return &copy
}

func (s *backendValueState) Value() ([]byte, error) { value := s.Get(); return value, s.context.err }
func (s *backendValueState) ValueString() (string, error) {
	value, err := s.Value()
	return string(value), err
}
func (s *backendValueState) SetString(value string) error { s.Set([]byte(value)); return s.context.err }
func (s *backendValueState) ValueInt64() (int64, error) {
	value, err := s.Value()
	if err != nil || len(value) == 0 {
		return 0, err
	}
	if len(value) != 8 {
		s.context.fail(fmt.Errorf("state value is not an encoded int64"))
		return 0, s.context.err
	}
	return int64(binary.BigEndian.Uint64(value)), nil
}
func (s *backendValueState) SetInt64(value int64) error {
	s.Set(binary.BigEndian.AppendUint64(nil, uint64(value)))
	return s.context.err
}
func (s *backendValueState) ValueFloat64() (float64, error) {
	value, err := s.ValueInt64()
	return math.Float64frombits(uint64(value)), err
}
func (s *backendValueState) SetFloat64(value float64) error {
	return s.SetInt64(int64(math.Float64bits(value)))
}
func (s *backendMapState) Entries() (map[string][]byte, error) {
	entries := make(map[string][]byte)
	for _, key := range s.Keys() {
		entries[key] = s.Get(key)
	}
	return entries, s.context.err
}
