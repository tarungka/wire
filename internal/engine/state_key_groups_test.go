package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/keygroup"
)

func TestPebbleKeyGroupRangeBoundsAndCancellation(t *testing.T) {
	backend := pebbleBackendForTest(t, t.TempDir()).(*PebbleStateBackend)
	defer backend.Close()
	for _, group := range []uint16{0, 41, 42, 84, 85, 127, 32767} {
		key := make([]byte, 7)
		binary.BigEndian.PutUint16(key, group)
		if err := backend.Put(key, []byte("state")); err != nil {
			t.Fatal(err)
		}
	}
	var got []uint16
	err := backend.VisitKeyGroupRange(context.Background(), keygroup.KeyGroupRange{Start: 42, End: 85}, func(key, value []byte) error {
		got = append(got, binary.BigEndian.Uint16(key))
		return nil
	})
	if err != nil || len(got) != 2 || got[0] != 42 || got[1] != 84 {
		t.Fatalf("range=%v err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = backend.VisitKeyGroupRange(ctx, keygroup.KeyGroupRange{End: 32768}, func(key, value []byte) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	failure := errors.New("transfer failed")
	err = backend.VisitKeyGroupRange(context.Background(), keygroup.KeyGroupRange{Start: 32767, End: 32768}, func(key, value []byte) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("visitor=%v", err)
	}
}
