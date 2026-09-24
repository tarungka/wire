package engine

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestStateBackendAcceptance(t *testing.T) {
	for _, kind := range []StateBackendType{StateBackendHashMap, StateBackendPebble} {
		t.Run(string(kind), func(t *testing.T) {
			factory := func(t *testing.T) StateBackend {
				t.Helper()
				b, err := NewStateBackend(StateBackendConfig{Type: kind, PebbleDataDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := b.Close(); err != nil {
						t.Error(err)
					}
				})
				return b
			}
			t.Run("TenThousandEntryRoundtrip", func(t *testing.T) {
				backend := factory(t)
				entries := make([]StateMutation, 10000)
				for i := range entries {
					entries[i] = StateMutation{Key: []byte(fmt.Sprintf("key/%05d", i)), Value: []byte(fmt.Sprintf("value/%05d", i))}
				}
				if err := backend.(BatchedStateBackend).ApplyBatch(entries); err != nil {
					t.Fatal(err)
				}
				snapshot, err := backend.Checkpoint(1)
				if err != nil {
					t.Fatal(err)
				}
				restored := factory(t)
				if err := restored.Restore(snapshot); err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					got, err := restored.Get(entry.Key)
					if err != nil || !bytes.Equal(got, entry.Value) {
						t.Fatalf("restored %q: %q, %v", entry.Key, got, err)
					}
				}
			})
			t.Run("EmptyAndTenMiBValue", func(t *testing.T) {
				backend := factory(t)
				empty, err := backend.Checkpoint(1)
				if err != nil {
					t.Fatal(err)
				}
				value := bytes.Repeat([]byte{0x00, 0xff, 0x12, 0x34}, (10<<20)/4)
				if err := backend.Put([]byte("large"), value); err != nil {
					t.Fatal(err)
				}
				large, err := backend.Checkpoint(2)
				if err != nil {
					t.Fatal(err)
				}
				if err := backend.Restore(empty); err != nil {
					t.Fatal(err)
				}
				if _, err := backend.Get([]byte("large")); !errors.Is(err, ErrKeyNotFound) {
					t.Fatalf("empty restore: %v", err)
				}
				if err := backend.Restore(large); err != nil {
					t.Fatal(err)
				}
				got, err := backend.Get([]byte("large"))
				if err != nil || !bytes.Equal(got, value) {
					t.Fatalf("large value restore: length %d, error %v", len(got), err)
				}
			})
			t.Run("BinaryKeyGroupPrefix", func(t *testing.T) {
				backend := factory(t)
				var entries []StateMutation
				for group := 127; group >= 0; group-- {
					for record := 9; record >= 0; record-- {
						key := make([]byte, 4)
						binary.BigEndian.PutUint16(key, uint16(group))
						binary.BigEndian.PutUint16(key[2:], uint16(record))
						entries = append(entries, StateMutation{Key: key, Value: []byte{byte(record)}})
					}
				}
				if err := backend.(BatchedStateBackend).ApplyBatch(entries); err != nil {
					t.Fatal(err)
				}
				it := backend.NewIterator([]byte{0, 0x20})
				defer it.Close()
				count := 0
				for it.Next() {
					want := []byte{0, 0x20, 0, byte(count)}
					if !bytes.Equal(it.Key(), want) || !bytes.Equal(it.Value(), []byte{byte(count)}) {
						t.Fatalf("unexpected entry %x: %x", it.Key(), it.Value())
					}
					count++
				}
				if count != 10 {
					t.Fatalf("matched %d records", count)
				}
			})
			t.Run("ConcurrentCheckpointAtomicity", func(t *testing.T) {
				backend := factory(t)
				put := func(value byte) error {
					return backend.(BatchedStateBackend).ApplyBatch([]StateMutation{{Key: []byte("a"), Value: []byte{value}}, {Key: []byte("b"), Value: []byte{value}}})
				}
				if err := put(0); err != nil {
					t.Fatal(err)
				}
				start := make(chan struct{})
				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					for i := 1; i <= 100; i++ {
						if err := put(byte(i)); err != nil {
							t.Error(err)
							return
						}
						if _, err := backend.Get([]byte("a")); err != nil {
							t.Error(err)
							return
						}
					}
				}()
				// Join the writer even if checkpoint validation fails.
				defer wg.Wait()
				close(start)
				for i := 1; i <= 10; i++ {
					snapshot, err := backend.Checkpoint(uint64(i))
					if err != nil {
						t.Fatal(err)
					}
					restored := factory(t)
					if err := restored.Restore(snapshot); err != nil {
						t.Fatal(err)
					}
					a, err := restored.Get([]byte("a"))
					if err != nil {
						t.Fatal(err)
					}
					b, err := restored.Get([]byte("b"))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(a, b) {
						t.Fatalf("torn atomic batch: a=%x b=%x", a, b)
					}
				}
			})
		})
	}
}
