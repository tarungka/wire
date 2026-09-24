package sdk

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/tarungka/wire/internal/engine"
)

// TimerFunc runs on the operator goroutine when its event-time watermark reaches
// a registered timestamp. State is scoped to the key that registered the timer.
type TimerFunc func(ProcessContext, int64) ([]Event, error)

func (ks *KeyedStream) ProcessWithTimers(fn ProcessFunc, onTimer TimerFunc) *DataStream {
	stream := ks.Process(fn)
	ks.env.graph.nodes[stream.nodeID].TimerFn = onTimer
	return stream
}

func timerKey(key []byte, timestamp int64) []byte {
	encoded := binary.BigEndian.AppendUint64([]byte{'t'}, uint64(timestamp)^(uint64(1)<<63))
	return append(encoded, key...)
}

var processWatermarkKey = []byte("w")

func (c *backendProcessContext) CurrentKey() []byte              { return c.Key() }
func (c *backendProcessContext) GetState(name string) ValueState { return c.GetValueState(name) }
func (c *backendProcessContext) CurrentEventTime() int64         { return c.eventTime }
func (c *backendProcessContext) CurrentWatermark() int64         { return c.watermark }
func (c *backendProcessContext) RegisterEventTimeTimer(timestamp int64) {
	if !c.timersEnabled {
		c.fail(fmt.Errorf("RegisterEventTimeTimer requires ProcessWithTimers"))
		return
	}
	if timestamp <= c.watermark {
		c.hasDueTimer = true
	}
	key := timerKey(c.key, timestamp)
	c.put(key, []byte{1})
	c.registeredTimers = append(c.registeredTimers, key)
}
func (c *backendProcessContext) DeleteEventTimeTimer(timestamp int64) {
	c.remove(timerKey(c.key, timestamp))
}
func (c *backendProcessContext) EmitToSideOutput(tag OutputTag, event Event) {
	if tag.Name == "" || !c.sideTags[tag.Name] {
		c.fail(fmt.Errorf("undeclared Process side output %q", tag.Name))
		return
	}
	c.sideEvents = append(c.sideEvents, engine.WithSideOutput(cloneEvent(event), tag.Name))
}

func (a *processAdapter) loadWatermark() error {
	a.watermark = math.MinInt64
	value, err := a.backend.Get(processWatermarkKey)
	if errors.Is(err, engine.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(value) != 8 {
		return fmt.Errorf("sdk: corrupt Process watermark")
	}
	a.watermark = int64(binary.BigEndian.Uint64(value))
	return nil
}
func (a *processAdapter) processContext(key []byte, eventTime int64) *backendProcessContext {
	tags := make(map[string]bool, len(a.sideTags))
	for _, tag := range a.sideTags {
		tags[tag] = true
	}
	return &backendProcessContext{key: append([]byte(nil), key...), backend: a.backend, clock: a.clock, eventTime: eventTime, watermark: a.watermark, timersEnabled: a.onTimer != nil, sideTags: tags}
}

// Timer chains must terminate. Bound callback work per boundary so a callback
// that continually reschedules an already-due timer cannot wedge the task.
const maxTimerCallbacksPerBoundary = 100000

func (a *processAdapter) OnWatermark(ctx context.Context, timestamp int64) ([]Event, error) {
	if timestamp <= a.watermark {
		return nil, nil
	}
	if err := a.backend.Put(processWatermarkKey, binary.BigEndian.AppendUint64(nil, uint64(timestamp))); err != nil {
		return nil, err
	}
	a.watermark = timestamp
	return a.fireDueTimers(ctx)
}
func (a *processAdapter) fireDueTimers(ctx context.Context) ([]Event, error) {
	var due timerQueue
	it := a.backend.NewIterator([]byte{'t'})
	for it.Next() {
		key := it.Key()
		if len(key) < 9 {
			it.Close()
			return nil, fmt.Errorf("sdk: corrupt event-time timer")
		}
		if timerTimestamp(key) > a.watermark {
			break
		}
		due = append(due, bytes.Clone(key))
		if len(due) > maxTimerCallbacksPerBoundary {
			it.Close()
			return nil, fmt.Errorf("sdk: timer callback limit exceeded at one watermark")
		}
	}
	it.Close()
	if len(due) > 0 && a.onTimer == nil {
		return nil, fmt.Errorf("sdk: restored timers require a TimerFunc")
	}
	heap.Init(&due)
	var output []Event
	fired := 0
	for len(due) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := heap.Pop(&due).([]byte)
		if _, err := a.backend.Get(key); errors.Is(err, engine.ErrKeyNotFound) {
			continue
		} else if err != nil {
			return nil, err
		}
		if fired >= maxTimerCallbacksPerBoundary {
			return nil, fmt.Errorf("sdk: timer callback limit exceeded at one watermark")
		}
		if err := a.backend.Delete(key); err != nil {
			return nil, err
		}
		deadline := timerTimestamp(key)
		pctx := a.processContext(key[9:], deadline)
		events, err := a.onTimer(pctx, deadline)
		if err = errors.Join(err, pctx.err); err != nil {
			return nil, err
		}
		output = append(output, pctx.sideEvents...)
		output = append(output, events...)
		fired++
		for _, registered := range pctx.registeredTimers {
			if timerTimestamp(registered) <= a.watermark {
				heap.Push(&due, registered)
			}
		}
	}
	return output, nil
}
func timerTimestamp(key []byte) int64 {
	return int64(binary.BigEndian.Uint64(key[1:9]) ^ (uint64(1) << 63))
}

type timerQueue [][]byte

func (q timerQueue) Len() int           { return len(q) }
func (q timerQueue) Less(i, j int) bool { return bytes.Compare(q[i], q[j]) < 0 }
func (q timerQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *timerQueue) Push(value any)    { *q = append(*q, value.([]byte)) }
func (q *timerQueue) Pop() any {
	old := *q
	value := old[len(old)-1]
	*q = old[:len(old)-1]
	return value
}
