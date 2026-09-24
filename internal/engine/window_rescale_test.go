package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"reflect"
	"sort"
	"testing"

	"github.com/tarungka/wire/internal/keygroup"
)

func TestWindowRescaleMatchesOriginalPartitions(t *testing.T) {
	for _, kind := range []StateBackendType{StateBackendHashMap, StateBackendPebble} {
		for _, windowKind := range []string{"tumbling", "sliding", "session"} {
			for _, sizes := range [][2]int{{4, 8}, {8, 4}, {4, 3}} {
				t.Run(fmt.Sprintf("%s/%s/%d-to-%d", kind, windowKind, sizes[0], sizes[1]), func(t *testing.T) {
					ctx := context.Background()
					create := func() *EventTimeWindowOperator {
						op, err := NewEventTimeWindowOperator(WindowConfig{Kind: windowKind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: 5, AggregationID: "count"}, windowCount{}, func(r WindowResult) Event { return Event{Key: r.Key, Value: r.Value} })
						if err != nil {
							t.Fatal(err)
						}
						op.SetKeyGroupCount(128)
						dir := t.TempDir()
						op.SetStateBackendFactory(func() (StateBackend, func(), error) {
							b, err := NewStateBackend(StateBackendConfig{Type: kind, PebbleDataDir: dir})
							return b, nil, err
						})
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
					old := make([]*EventTimeWindowOperator, sizes[0])
					for i := range old {
						old[i] = create()
					}
					const records = 32
					for i := 0; i < records; i++ {
						key := []byte(fmt.Sprintf("key-%d", i))
						owner := keygroup.AssignedTask(keygroup.KeyGroup(key, 128), 128, sizes[0])
						if _, _, err := old[owner].processor.Process(Event{Key: key, EventTime: 1}); err != nil {
							t.Fatal(err)
						}
					}
					snapshots := make([]SnapshotHandle, len(old))
					for i, op := range old {
						watermark := []int64{5, 12, 20}[i%3]
						if _, err := op.OnWatermark(ctx, watermark); err != nil {
							t.Fatal(err)
						}
						var err error
						snapshots[i], err = op.CheckpointState(7)
						if err != nil {
							t.Fatal(err)
						}
					}
					restored := make([]*EventTimeWindowOperator, sizes[1])
					for i := range restored {
						assigned, err := keygroup.TaskKeyGroupRange(i, 128, sizes[1])
						if err != nil {
							t.Fatal(err)
						}
						var parts []KeyGroupSnapshot
						for j := range old {
							previous, err := keygroup.TaskKeyGroupRange(j, 128, sizes[0])
							if err != nil {
								t.Fatal(err)
							}
							start, end := max(previous.Start, assigned.Start), min(previous.End, assigned.End)
							if start < end {
								parts = append(parts, KeyGroupSnapshot{Groups: keygroup.KeyGroupRange{Start: start, End: end}, Snapshot: snapshots[j]})
							}
						}
						restored[i] = create()
						if err := restored[i].RestoreKeyGroupState(ctx, assigned, parts); err != nil {
							t.Fatal(err)
						}
						for key := range restored[i].processor.windows {
							if !assigned.Contains(keygroup.KeyGroup([]byte(key), 128)) {
								t.Fatalf("unassigned key %s", key)
							}
						}
						// Progress must also survive an ordinary checkpoint after redistribution.
						snapshot, err := restored[i].CheckpointState(8)
						if err != nil {
							t.Fatal(err)
						}
						if err := restored[i].RestoreState(snapshot); err != nil {
							t.Fatal(err)
						}
					}
					for i := 0; i < records; i++ {
						key := []byte(fmt.Sprintf("key-%d", i))
						group := keygroup.KeyGroup(key, 128)
						before := old[keygroup.AssignedTask(group, 128, sizes[0])]
						after := restored[keygroup.AssignedTask(group, 128, sizes[1])]
						event := Event{Key: key, EventTime: 1}
						want, wantLate, err := before.processor.Process(event)
						if err != nil {
							t.Fatal(err)
						}
						got, gotLate, err := after.processor.Process(event)
						if err != nil {
							t.Fatal(err)
						}
						if wantLate != gotLate || !reflect.DeepEqual(want, got) {
							t.Fatalf("changed late/firing behavior for %s: got %v/%t want %v/%t", key, got, gotLate, want, wantLate)
						}
					}
					collect := func(operators []*EventTimeWindowOperator) []WindowResult {
						var results []WindowResult
						for _, op := range operators {
							events, err := op.processor.AdvanceWatermarkChecked(30)
							if err != nil {
								t.Fatal(err)
							}
							results = append(results, events...)
						}
						sort.Slice(results, func(i, j int) bool {
							if string(results[i].Key) != string(results[j].Key) {
								return string(results[i].Key) < string(results[j].Key)
							}
							return results[i].WindowStart < results[j].WindowStart
						})
						return results
					}
					if want, got := collect(old), collect(restored); !reflect.DeepEqual(want, got) {
						t.Fatalf("rescale changed final outputs: got %+v want %+v", got, want)
					}
				})
			}
		}
	}
}

func TestWindowRestoreRejectsInvalidGroupProgress(t *testing.T) {
	p := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 10, AllowedLateness: 5})
	addWindowEvent(t, p, 1)
	original, err := p.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot windowSnapshot
	if err := json.Unmarshal(original[:len(original)-4], &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name            string
		version, groups int
		floors          map[uint16]int64
	}{
		{"old-version-with-new-progress", 1, 128, map[uint16]int64{0: 12}},
		{"changed-hash-space", 2, 64, map[uint16]int64{0: 12}},
		{"invalid-group", 2, 128, map[uint16]int64{128: 12}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := snapshot
			changed.Version = test.version
			changed.NumKeyGroups = test.groups
			changed.GroupWatermarks = test.floors
			body, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			err = p.Restore(binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body)))
			if !errors.Is(err, ErrSnapshotCorrupt) {
				t.Fatalf("restore: %v", err)
			}
			after, err := p.Checkpoint(1)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, original) {
				t.Fatal("rejected progress changed live windows")
			}
		})
	}
}
