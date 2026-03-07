package keygroup

// KeyGroupRange is a half-open range [Start, End) of key groups.
type KeyGroupRange struct {
	Start uint16
	End   uint16
}

// Contains reports whether keyGroup falls within the range [Start, End).
func (r KeyGroupRange) Contains(keyGroup uint16) bool {
	return keyGroup >= r.Start && keyGroup < r.End
}

// Size returns the number of key groups in the range.
func (r KeyGroupRange) Size() int {
	return int(r.End) - int(r.Start)
}

// AssignedTask returns which task owns the given key group.
// Consistent with TaskKeyGroupRange: task t owns [t*N/P, (t+1)*N/P).
func AssignedTask(keyGroup uint16, numKeyGroups, parallelism int) int {
	g := int(keyGroup)
	t := g * parallelism / numKeyGroups
	// Adjust for integer division rounding at range boundaries.
	// The initial estimate can be off by at most 1, so a single if suffices.
	if t+1 < parallelism && (t+1)*numKeyGroups/parallelism <= g {
		t++
	}
	return t
}

// TaskKeyGroupRange returns the key group range owned by the given task.
// Formula per WIP-03 Section 3.3:
//
//	Start = taskIndex * numKeyGroups / parallelism
//	End   = (taskIndex + 1) * numKeyGroups / parallelism
func TaskKeyGroupRange(taskIndex, numKeyGroups, parallelism int) (KeyGroupRange, error) {
	if taskIndex < 0 || taskIndex >= parallelism {
		return KeyGroupRange{}, ErrInvalidTaskIndex
	}
	start := taskIndex * numKeyGroups / parallelism
	end := (taskIndex + 1) * numKeyGroups / parallelism
	return KeyGroupRange{
		Start: uint16(start),
		End:   uint16(end),
	}, nil
}

// AllTaskRanges returns the key group ranges for all tasks.
func AllTaskRanges(numKeyGroups, parallelism int) ([]KeyGroupRange, error) {
	ranges := make([]KeyGroupRange, parallelism)
	for i := range parallelism {
		r, err := TaskKeyGroupRange(i, numKeyGroups, parallelism)
		if err != nil {
			return nil, err
		}
		ranges[i] = r
	}
	return ranges, nil
}

// RescaleMapping computes which key group ranges each new task needs when
// parallelism changes. Returns map[newTaskIndex][]KeyGroupRange representing
// the ranges from old tasks that the new task must acquire.
// Calculation only — actual state transfer depends on WIP-06.
func RescaleMapping(numKeyGroups, oldParallelism, newParallelism int) (map[int][]KeyGroupRange, error) {
	oldRanges, err := AllTaskRanges(numKeyGroups, oldParallelism)
	if err != nil {
		return nil, err
	}
	newRanges, err := AllTaskRanges(numKeyGroups, newParallelism)
	if err != nil {
		return nil, err
	}

	result := make(map[int][]KeyGroupRange, newParallelism)
	for newIdx, nr := range newRanges {
		for _, or := range oldRanges {
			// Compute the intersection of the old and new ranges.
			start := max(nr.Start, or.Start)
			end := min(nr.End, or.End)
			if start < end {
				result[newIdx] = append(result[newIdx], KeyGroupRange{Start: start, End: end})
			}
		}
	}
	return result, nil
}
