package engine

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/tarungka/wire/internal/keygroup"
)

func TestHashMapRescaleMergesOnlyAssignedRanges(t *testing.T) {
	var parts []KeyGroupSnapshot
	for i := 0; i < 2; i++ {
		source := NewHashMapStateBackend(0)
		defer source.Close()
		for group := 0; group < 128; group++ {
			key := make([]byte, 7)
			binary.BigEndian.PutUint16(key, uint16(group))
			if err := source.Put(key, []byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		snapshot, err := source.Checkpoint(7)
		if err != nil {
			t.Fatal(err)
		}
		groups := keygroup.KeyGroupRange{Start: 42, End: 64}
		if i == 1 {
			groups = keygroup.KeyGroupRange{Start: 64, End: 85}
		}
		parts = append(parts, KeyGroupSnapshot{Groups: groups, Snapshot: snapshot})
	}
	target := NewHashMapStateBackend(0)
	defer target.Close()
	if err := target.RestoreKeyGroupRanges(context.Background(), keygroup.KeyGroupRange{Start: 42, End: 85}, parts); err != nil {
		t.Fatal(err)
	}
	iter := target.NewIterator(nil)
	defer iter.Close()
	count := 0
	for iter.Next() {
		group := binary.BigEndian.Uint16(iter.Key())
		if group < 42 || group >= 85 {
			t.Fatalf("unassigned group %d", group)
		}
		want := byte(0)
		if group >= 64 {
			want = 1
		}
		if len(iter.Value()) != 1 || iter.Value()[0] != want {
			t.Fatalf("wrong source for group %d", group)
		}
		count++
	}
	if count != 43 {
		t.Fatalf("count=%d", count)
	}
}

func TestHashMapRescaleFailurePreservesPublishedState(t *testing.T) {
	source := NewHashMapStateBackend(0)
	defer source.Close()
	if err := source.Put([]byte{0, 0, 1}, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Checkpoint(9)
	if err != nil {
		t.Fatal(err)
	}
	assigned := keygroup.KeyGroupRange{End: 2}
	corrupt := snapshot
	corrupt.Data = []byte("invalid manifest")
	mixed := snapshot
	mixed.CheckpointID = 10
	cases := []struct {
		name     string
		parts    []KeyGroupSnapshot
		canceled bool
	}{
		{name: "corrupt-second-part", parts: []KeyGroupSnapshot{{Groups: keygroup.KeyGroupRange{End: 1}, Snapshot: snapshot}, {Groups: keygroup.KeyGroupRange{Start: 1, End: 2}, Snapshot: corrupt}}},
		{name: "gap", parts: []KeyGroupSnapshot{{Groups: keygroup.KeyGroupRange{Start: 1, End: 2}, Snapshot: snapshot}}},
		{name: "overlap", parts: []KeyGroupSnapshot{{Groups: assigned, Snapshot: snapshot}, {Groups: assigned, Snapshot: snapshot}}},
		{name: "mixed-checkpoints", parts: []KeyGroupSnapshot{{Groups: keygroup.KeyGroupRange{End: 1}, Snapshot: snapshot}, {Groups: keygroup.KeyGroupRange{Start: 1, End: 2}, Snapshot: mixed}}},
		{name: "canceled", parts: []KeyGroupSnapshot{{Groups: assigned, Snapshot: snapshot}}, canceled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := NewHashMapStateBackend(0)
			if err := target.Put([]byte("original"), []byte("retained")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			if err := target.RestoreKeyGroupRanges(ctx, assigned, tc.parts); err == nil {
				t.Fatal("accepted invalid rescale")
			}
			if value, err := target.Get([]byte("original")); err != nil || string(value) != "retained" {
				t.Fatalf("live state changed: %q %v", value, err)
			}
			if err := target.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHashMapRescaleMemoryLimitPreservesState(t *testing.T) {
	source := NewHashMapStateBackend(0)
	if err := source.Put([]byte{0, 0, 1}, []byte("too-large-for-target")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	target := NewHashMapStateBackend(4)
	if err := target.Put([]byte("a"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	groups := keygroup.KeyGroupRange{End: 1}
	if err := target.RestoreKeyGroupRanges(context.Background(), groups, []KeyGroupSnapshot{{Groups: groups, Snapshot: snapshot}}); err != ErrMemoryLimitExceeded {
		t.Fatalf("restore = %v", err)
	}
	value, err := target.Get([]byte("a"))
	if err != nil || string(value) != "old" || target.MemUsage() != 4 {
		t.Fatal("memory rejection changed state")
	}
}
