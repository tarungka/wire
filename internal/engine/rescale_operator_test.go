package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tarungka/wire/internal/keygroup"
)

type rescaleLifecycleSource struct {
	opened, restored, closed, read bool
	restoreErr                     error
	panicRestore                   bool
}

func (s *rescaleLifecycleSource) Open(context.Context) error      { s.opened = true; return nil }
func (s *rescaleLifecycleSource) Close() error                    { s.closed = true; return nil }
func (*rescaleLifecycleSource) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (*rescaleLifecycleSource) GenerateWatermark() int64          { return 0 }
func (s *rescaleLifecycleSource) ReadBatch(context.Context) ([]Event, error) {
	s.read = true
	if !s.restored {
		return nil, fmt.Errorf("read before restore")
	}
	return nil, nil
}
func (s *rescaleLifecycleSource) RestoreKeyGroupState(context.Context, keygroup.KeyGroupRange, []KeyGroupSnapshot) error {
	if !s.opened {
		return fmt.Errorf("restore before open")
	}
	if s.panicRestore {
		panic("rescale failed")
	}
	if s.restoreErr != nil {
		return s.restoreErr
	}
	s.restored = true
	return nil
}

func TestTaskSlotRescaleRestoreLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic", "duplicate", "invalid-index"} {
		t.Run(mode, func(t *testing.T) {
			failure := errors.New("restore failed")
			source := &rescaleLifecycleSource{}
			if mode == "error" {
				source.restoreErr = failure
			}
			if mode == "panic" {
				source.panicRestore = true
			}
			slot := NewTaskSlot(DefaultTaskSlotConfig(), nil, nil, nil, source)
			slot.TaskID = "rescaled"
			state := OperatorRescaleState{OperatorIndex: -1, Assigned: keygroup.KeyGroupRange{End: 128}}
			slot.RescaleState = []OperatorRescaleState{state}
			if mode == "duplicate" {
				slot.RescaleState = append(slot.RescaleState, state)
			}
			if mode == "invalid-index" {
				slot.RescaleState[0].OperatorIndex = 2
			}
			running := false
			slot.OnRunning = func() {
				running = true
				if !source.restored {
					t.Error("RUNNING before restoration")
				}
			}
			err := slot.Run(context.Background())
			if !source.opened || !source.closed {
				t.Fatalf("lifecycle=%+v", source)
			}
			if mode == "success" {
				if err != nil || !running || !source.read {
					t.Fatalf("success err=%v running=%v read=%v", err, running, source.read)
				}
			} else {
				if err == nil || running || source.read {
					t.Fatalf("failure err=%v running=%v read=%v", err, running, source.read)
				}
				if mode == "error" && !errors.Is(err, failure) {
					t.Fatalf("lost restore error: %v", err)
				}
			}
		})
	}
}
