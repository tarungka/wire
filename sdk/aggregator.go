package sdk

import (
	"encoding/binary"
	"math"
)

// Aggregator incrementally aggregates events within a window.
type Aggregator interface {
	// CreateAccumulator returns a new zero-value accumulator.
	CreateAccumulator() []byte
	// Add incorporates an event into the accumulator.
	Add(accumulator []byte, event Event) []byte
	// GetResult extracts the final result from the accumulator.
	GetResult(accumulator []byte) []byte
	// Merge combines two accumulators (for session window merging).
	Merge(a, b []byte) []byte
}

// CountAggregator counts the number of events.
type CountAggregator struct{}

func (CountAggregator) CreateAccumulator() []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, 0)
	return buf
}

func (CountAggregator) Add(acc []byte, _ Event) []byte {
	v := binary.BigEndian.Uint64(acc) + 1
	binary.BigEndian.PutUint64(acc, v)
	return acc
}

func (CountAggregator) GetResult(acc []byte) []byte {
	return acc
}

func (CountAggregator) Merge(a, b []byte) []byte {
	va := binary.BigEndian.Uint64(a)
	vb := binary.BigEndian.Uint64(b)
	binary.BigEndian.PutUint64(a, va+vb)
	return a
}

// SumAggregator sums event values interpreted as big-endian float64.
type SumAggregator struct{}

func (SumAggregator) CreateAccumulator() []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(0))
	return buf
}

func (SumAggregator) Add(acc []byte, event Event) []byte {
	sum := math.Float64frombits(binary.BigEndian.Uint64(acc))
	val := math.Float64frombits(binary.BigEndian.Uint64(event.Value))
	binary.BigEndian.PutUint64(acc, math.Float64bits(sum+val))
	return acc
}

func (SumAggregator) GetResult(acc []byte) []byte {
	return acc
}

func (SumAggregator) Merge(a, b []byte) []byte {
	va := math.Float64frombits(binary.BigEndian.Uint64(a))
	vb := math.Float64frombits(binary.BigEndian.Uint64(b))
	binary.BigEndian.PutUint64(a, math.Float64bits(va+vb))
	return a
}

// MinAggregator tracks the minimum event value interpreted as big-endian float64.
type MinAggregator struct{}

func (MinAggregator) CreateAccumulator() []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(math.MaxFloat64))
	return buf
}

func (MinAggregator) Add(acc []byte, event Event) []byte {
	cur := math.Float64frombits(binary.BigEndian.Uint64(acc))
	val := math.Float64frombits(binary.BigEndian.Uint64(event.Value))
	if val < cur {
		binary.BigEndian.PutUint64(acc, math.Float64bits(val))
	}
	return acc
}

func (MinAggregator) GetResult(acc []byte) []byte {
	return acc
}

func (MinAggregator) Merge(a, b []byte) []byte {
	va := math.Float64frombits(binary.BigEndian.Uint64(a))
	vb := math.Float64frombits(binary.BigEndian.Uint64(b))
	if vb < va {
		binary.BigEndian.PutUint64(a, math.Float64bits(vb))
	}
	return a
}

// MaxAggregator tracks the maximum event value interpreted as big-endian float64.
type MaxAggregator struct{}

func (MaxAggregator) CreateAccumulator() []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(-math.MaxFloat64))
	return buf
}

func (MaxAggregator) Add(acc []byte, event Event) []byte {
	cur := math.Float64frombits(binary.BigEndian.Uint64(acc))
	val := math.Float64frombits(binary.BigEndian.Uint64(event.Value))
	if val > cur {
		binary.BigEndian.PutUint64(acc, math.Float64bits(val))
	}
	return acc
}

func (MaxAggregator) GetResult(acc []byte) []byte {
	return acc
}

func (MaxAggregator) Merge(a, b []byte) []byte {
	va := math.Float64frombits(binary.BigEndian.Uint64(a))
	vb := math.Float64frombits(binary.BigEndian.Uint64(b))
	if vb > va {
		binary.BigEndian.PutUint64(a, math.Float64bits(vb))
	}
	return a
}

// AvgAggregator computes the average of event values (big-endian float64).
// Accumulator layout: [8 bytes sum][8 bytes count].
type AvgAggregator struct{}

func (AvgAggregator) CreateAccumulator() []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[0:8], math.Float64bits(0))
	binary.BigEndian.PutUint64(buf[8:16], 0)
	return buf
}

func (AvgAggregator) Add(acc []byte, event Event) []byte {
	sum := math.Float64frombits(binary.BigEndian.Uint64(acc[0:8]))
	count := binary.BigEndian.Uint64(acc[8:16])
	val := math.Float64frombits(binary.BigEndian.Uint64(event.Value))
	binary.BigEndian.PutUint64(acc[0:8], math.Float64bits(sum+val))
	binary.BigEndian.PutUint64(acc[8:16], count+1)
	return acc
}

func (AvgAggregator) GetResult(acc []byte) []byte {
	sum := math.Float64frombits(binary.BigEndian.Uint64(acc[0:8]))
	count := binary.BigEndian.Uint64(acc[8:16])
	if count == 0 {
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, math.Float64bits(0))
		return buf
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(sum/float64(count)))
	return buf
}

func (AvgAggregator) Merge(a, b []byte) []byte {
	sumA := math.Float64frombits(binary.BigEndian.Uint64(a[0:8]))
	countA := binary.BigEndian.Uint64(a[8:16])
	sumB := math.Float64frombits(binary.BigEndian.Uint64(b[0:8]))
	countB := binary.BigEndian.Uint64(b[8:16])
	binary.BigEndian.PutUint64(a[0:8], math.Float64bits(sumA+sumB))
	binary.BigEndian.PutUint64(a[8:16], countA+countB)
	return a
}
