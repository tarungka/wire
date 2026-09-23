package sdk

import (
	"reflect"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

func TestManagedStateTTLCheckpointRestore(t *testing.T) {
	for _, kind := range []string{"hashmap", "pebble"} {
		t.Run(kind, func(t *testing.T) {
			cfg := NewHashMapStateBackend(0)
			if kind == "pebble" {
				cfg = NewPebbleStateBackend(t.TempDir())
			}
			backend, cleanup, err := cfg.open(0, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			defer func() { _ = backend.Close() }()
			now := time.Unix(10, 0)
			c := &backendProcessContext{key: []byte("key"), backend: backend, clock: func() time.Time { return now }}
			value := c.GetValueState("v").WithTTL(2 * time.Second)
			if err := value.SetInt64(42); err != nil {
				t.Fatal(err)
			}
			list := c.GetListState("l").WithTTL(2 * time.Second)
			list.Add([]byte("item"))
			m := c.GetMapState("m").WithTTL(2 * time.Second)
			m.Put("x", []byte("entry"))
			m.Put("nil", nil)
			if c.err != nil {
				t.Fatal(c.err)
			}
			snapshot, err := backend.Checkpoint(1)
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			if v, err := value.ValueInt64(); err != nil || v != 42 {
				t.Fatalf("value=%d err=%v", v, err)
			}
			if keys := m.Keys(); !reflect.DeepEqual(keys, []string{"nil", "x"}) {
				t.Fatalf("keys=%v", keys)
			}
			now = now.Add(time.Second)
			if value.Get() != nil || list.Get() != nil || len(m.Keys()) != 0 {
				t.Fatal("reads refreshed TTL or expiry was ignored")
			}
			if c.err != nil {
				t.Fatal(c.err)
			}
			if err := backend.Restore(snapshot); err != nil {
				t.Fatal(err)
			}
			// Expiry is absolute and persisted: restoring cannot extend retention.
			if value.Get() != nil || list.Get() != nil || len(m.Keys()) != 0 {
				t.Fatal("restore revived expired state")
			}
			if err := value.WithTTL(0).SetString("retained"); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Hour)
			if got, err := value.ValueString(); err != nil || got != "retained" {
				t.Fatalf("TTL disable: %q %v", got, err)
			}
		})
	}
}

func TestStateTypedValuesAndInvalidEncoding(t *testing.T) {
	backend := engine.NewHashMapStateBackend(0)
	defer func() { _ = backend.Close() }()
	c := &backendProcessContext{backend: backend}
	s := c.GetValueState("typed")
	if err := s.SetFloat64(-1.25); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ValueFloat64(); err != nil || got != -1.25 {
		t.Fatalf("float=%v %v", got, err)
	}
	s.Set([]byte("bad"))
	if _, err := s.ValueInt64(); err == nil {
		t.Fatal("bad typed state silently accepted")
	}
}
