package sdk

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
)

func TestManagedProcessRescaleState(t *testing.T) {
	for _, kind := range []string{"hashmap", "pebble"} {
		for _, sizes := range [][2]int{{4, 8}, {8, 4}, {4, 3}} {
			t.Run(fmt.Sprintf("%s/%d-to-%d", kind, sizes[0], sizes[1]), func(t *testing.T) {
				ctx := context.Background()
				config := NewHashMapStateBackend(0)
				if kind == "pebble" {
					config = NewPebbleStateBackend(t.TempDir())
				}
				create := func(index int) *ProcessOperator {
					op := NewProcessOperator(func(c ProcessContext, e Event) ([]Event, error) {
						c.GetValueState("v").WithTTL(time.Hour).Set(e.Value)
						c.GetListState("l").Add(e.Value)
						c.GetMapState("m").Put("field", e.Value)
						c.RegisterEventTimeTimer(100)
						return nil, nil
					}, func(c ProcessContext, _ int64) ([]Event, error) {
						return []Event{{Key: c.Key(), Value: c.GetValueState("v").Get()}}, nil
					}, config)
					op.instance = index
					op.SetKeyGroupCount(128)
					op.clock = func() time.Time { return time.Unix(1, 0) }
					if err := op.Open(ctx); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := op.Close(); err != nil {
							t.Error(err)
						}
					})
					return op
				}
				old := make([]*ProcessOperator, sizes[0])
				for i := range old {
					old[i] = create(i)
					if _, err := old[i].OnWatermark(ctx, int64(10+i)); err != nil {
						t.Fatal(err)
					}
				}
				const records = 128
				for i := 0; i < records; i++ {
					key := []byte(fmt.Sprintf("key-%d", i))
					owner := keygroup.AssignedTask(keygroup.KeyGroup(key, 128), 128, sizes[0])
					if err := old[owner].FlatMap(ctx, Event{Key: key, Value: []byte(fmt.Sprint(i))}, func(Event) {}); err != nil {
						t.Fatal(err)
					}
				}
				handles := make([]engine.SnapshotHandle, len(old))
				for i, op := range old {
					var err error
					handles[i], err = op.CheckpointState(7)
					if err != nil {
						t.Fatal(err)
					}
				}
				seen := make(map[string]bool)
				for i := 0; i < sizes[1]; i++ {
					assigned, err := keygroup.TaskKeyGroupRange(i, 128, sizes[1])
					if err != nil {
						t.Fatal(err)
					}
					var parts []engine.KeyGroupSnapshot
					minimum := int64(1000)
					for j := range old {
						previous, err := keygroup.TaskKeyGroupRange(j, 128, sizes[0])
						if err != nil {
							t.Fatal(err)
						}
						start, end := max(previous.Start, assigned.Start), min(previous.End, assigned.End)
						if start < end {
							parts = append(parts, engine.KeyGroupSnapshot{Groups: keygroup.KeyGroupRange{Start: start, End: end}, Snapshot: handles[j]})
							minimum = min(minimum, int64(10+j))
						}
					}
					op := create(100 + i)
					if err := op.RestoreKeyGroupState(ctx, assigned, parts); err != nil {
						t.Fatal(err)
					}
					if op.watermark != minimum {
						t.Fatalf("watermark %d, want %d", op.watermark, minimum)
					}
					for j := 0; j < records; j++ {
						key := []byte(fmt.Sprintf("key-%d", j))
						c := op.processContext(key, 0)
						want := ""
						if assigned.Contains(keygroup.KeyGroup(key, 128)) {
							want = fmt.Sprint(j)
						}
						if got := string(c.GetValueState("v").Get()); got != want {
							t.Fatalf("value %s: %q want %q", key, got, want)
						}
						list := c.GetListState("l").Get()
						if want == "" && len(list) != 0 || want != "" && (len(list) != 1 || string(list[0]) != want) {
							t.Fatalf("list %s: %v", key, list)
						}
						if got := string(c.GetMapState("m").Get("field")); got != want {
							t.Fatalf("map %s: %q", key, got)
						}
						if c.err != nil {
							t.Fatal(c.err)
						}
					}
					events, err := op.OnWatermark(ctx, 100)
					if err != nil {
						t.Fatal(err)
					}
					for _, e := range events {
						if seen[string(e.Key)] || !assigned.Contains(keygroup.KeyGroup(e.Key, 128)) {
							t.Fatalf("duplicate/misplaced timer %s", e.Key)
						}
						seen[string(e.Key)] = true
					}
					// Expiry metadata must follow the same user key as its value.
					op.clock = func() time.Time { return time.Unix(1, 0).Add(2 * time.Hour) }
					for j := 0; j < records; j++ {
						if got := op.processContext([]byte(fmt.Sprintf("key-%d", j)), 0).GetValueState("v").Get(); got != nil {
							t.Fatal("TTL metadata lost during rescale")
						}
					}
				}
				if len(seen) != records {
					t.Fatalf("timers restored = %d", len(seen))
				}
			})
		}
	}
}

func TestManagedProcessRescaleRejectsMalformedStateAtomically(t *testing.T) {
	ctx := context.Background()
	for _, key := range [][]byte{[]byte("unknown"), {'v', 255, 255, 255, 255, 255, 255, 255, 255}, []byte("w")} {
		source := engine.NewHashMapStateBackend(0)
		if err := source.Put(key, []byte("bad")); err != nil {
			t.Fatal(err)
		}
		snapshot, err := source.Checkpoint(1)
		if err != nil {
			t.Fatal(err)
		}
		op := NewProcessOperator(nil, nil, NewHashMapStateBackend(0))
		if err := op.Open(ctx); err != nil {
			t.Fatal(err)
		}
		if err := op.backend.Put([]byte("original"), []byte("kept")); err != nil {
			t.Fatal(err)
		}
		assigned := keygroup.KeyGroupRange{End: 128}
		if err := op.RestoreKeyGroupState(ctx, assigned, []engine.KeyGroupSnapshot{{Groups: assigned, Snapshot: snapshot}}); err == nil {
			t.Fatalf("accepted malformed state %x", key)
		}
		got, err := op.backend.Get([]byte("original"))
		if err != nil || string(got) != "kept" {
			t.Fatal("failed rescale changed state")
		}
		if err := op.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
