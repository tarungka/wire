package worker

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/shirou/gopsutil/v4/mem"

	"github.com/tarungka/wire/internal/rpc"
)

// taskStateMemory sums finite managed HashMap limits, not current usage or RSS.
// An explicit zero limit remains unlimited and cannot reserve a finite budget.
func taskStateMemory(desc rpc.TaskDescriptor) (uint64, error) {
	var total uint64
	for _, operator := range desc.OperatorChain {
		backend := operator.StateBackend
		if backend == nil || backend.Type != "hashmap" {
			continue
		}
		if backend.MaxMemoryBytes < 0 {
			return 0, fmt.Errorf("negative HashMap memory limit")
		}
		limit := uint64(backend.MaxMemoryBytes)
		if limit > math.MaxUint64-total {
			return 0, fmt.Errorf("HashMap memory budget overflow")
		}
		total += limit
	}
	return total, nil
}

func availableStateMemory(ctx context.Context) (uint64, error) {
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return 0, err
	}
	return memory.Available, nil
}

// sampleStateMemory runs OS calls outside the worker ownership lock. The result
// is a conservative point-in-time admission bound, not an OS memory reservation.
func (w *Worker) sampleStateMemory(ctx context.Context, tasks []rpc.TaskDescriptor) (uint64, error) {
	var required uint64
	for _, task := range tasks {
		n, err := taskStateMemory(task)
		if err != nil {
			return 0, err
		}
		if n > math.MaxUint64-required {
			return 0, fmt.Errorf("HashMap memory budget overflow")
		}
		required += n
	}
	if required == 0 {
		return 0, nil
	}
	sample := w.stateMemoryAvailable
	if sample == nil {
		sample = availableStateMemory
	}
	sampleCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	available, err := sample(sampleCtx)
	if err != nil {
		return 0, fmt.Errorf("sample available memory: %w", err)
	}
	return available, nil
}

// checkStateMemoryLocked accounts for tasks until teardown removes their handle.
// Compare the entire finite budget with currently available host memory. This
// deliberately does not credit allocations already resident in the process.
func (w *Worker) checkStateMemoryLocked(tasks []rpc.TaskDescriptor, available uint64) error {
	var required uint64
	for _, task := range tasks {
		n, err := taskStateMemory(task)
		if err != nil {
			return err
		}
		if n > math.MaxUint64-required {
			return fmt.Errorf("HashMap memory budget overflow")
		}
		required += n
	}
	if required == 0 {
		return nil
	}
	for _, task := range w.tasks {
		if task.stateMemoryBytes > math.MaxUint64-required {
			return fmt.Errorf("HashMap memory budget overflow")
		}
		required += task.stateMemoryBytes
	}
	if required > available {
		return fmt.Errorf("HashMap memory budget %d exceeds available system memory %d", required, available)
	}
	return nil
}
