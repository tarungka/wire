// Command stateful runs a keyed SDK pipeline through a local Wire cluster.
// Use -recover to fail a source after a completed checkpoint and verify replay.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tarungka/wire/sdk"
)

type replaySource struct {
	firstAttempt bool
	offset       uint64
	snapshots    atomic.Int32
}

func (*replaySource) Open(context.Context) error { return nil }
func (*replaySource) Close() error               { return nil }
func (*replaySource) GenerateWatermark() int64   { return 0 }
func (s *replaySource) ReadBatch(ctx context.Context) ([]sdk.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.firstAttempt && s.offset == 1 {
		// A second trigger proves the preceding checkpoint completed: Wire
		// permits only one active checkpoint. Fail before reading record two.
		if s.snapshots.Load() >= 2 {
			return nil, errors.New("example: injected source failure")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
			return []sdk.Event{}, nil
		}
	}
	if s.offset == 2 {
		return nil, nil
	}
	s.offset++
	return []sdk.Event{{Key: []byte("customer-a"), Value: []byte("purchase"), EventTime: int64(s.offset)}}, nil
}
func (s *replaySource) Checkpoint(uint64) ([]byte, error) {
	s.snapshots.Add(1)
	return binary.BigEndian.AppendUint64(nil, s.offset), nil
}
func (s *replaySource) RestoreOffset(ctx context.Context, state []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(state) != 8 {
		return errors.New("example: invalid source offset")
	}
	offset := binary.BigEndian.Uint64(state)
	if offset > 2 {
		return errors.New("example: source offset outside input")
	}
	s.offset = offset
	return nil
}

type collectingSink struct {
	mu     sync.Mutex
	values []string
}

func (*collectingSink) Open(context.Context) error { return nil }
func (*collectingSink) Close() error               { return nil }
func (s *collectingSink) Write(_ context.Context, event sdk.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = append(s.values, string(event.Value))
	return nil
}
func (s *collectingSink) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.values...)
}

type output struct {
	Main           []string `json:"main"`
	Timers         []string `json:"timers"`
	SourceAttempts int32    `json:"source_attempts"`
}

func run(ctx context.Context, recoverSource bool) (output, error) {
	cluster := sdk.NewMiniCluster(sdk.MiniClusterConfig{NumTaskSlots: 2})
	defer cluster.Shutdown()
	env := cluster.GetExecutionEnvironment().
		SetCheckpointInterval(100 * time.Millisecond).
		SetCheckpointTimeout(10 * time.Second).
		SetMinPauseBetweenCheckpoints(20 * time.Millisecond).
		SetRestartStrategy(sdk.FixedDelay(2, 10*time.Millisecond)).
		SetStateBackend(sdk.NewHashMapStateBackend(8))
	var attempts atomic.Int32
	mainSink, timerSink := &collectingSink{}, &collectingSink{}
	timerTag := sdk.NewOutputTag("timer-results")
	stream := env.AddSourceFactory("purchases", func(sdk.InstanceContext) (sdk.Source, error) {
		attempt := attempts.Add(1)
		return &replaySource{firstAttempt: recoverSource && attempt == 1}, nil
	}).SetParallelism(1).KeyBy(func(e sdk.Event) ([]byte, error) { return e.Key, nil }).ProcessWithTimers(func(c sdk.ProcessContext, e sdk.Event) ([]sdk.Event, error) {
		state := c.GetState("purchase-count")
		count, err := state.ValueInt64()
		if err != nil {
			return nil, err
		}
		if err := state.SetInt64(count + 1); err != nil {
			return nil, err
		}
		if count == 0 {
			c.RegisterEventTimeTimer(10)
		}
		e.Value = []byte(fmt.Sprintf("%s: count=%d", c.CurrentKey(), count+1))
		return []sdk.Event{e}, nil
	}, func(c sdk.ProcessContext, timestamp int64) ([]sdk.Event, error) {
		count, err := c.GetState("purchase-count").ValueInt64()
		if err != nil {
			return nil, err
		}
		c.EmitToSideOutput(timerTag, sdk.Event{Key: c.CurrentKey(), Value: []byte(fmt.Sprintf("%s: timer=%d count=%d", c.CurrentKey(), timestamp, count)), EventTime: timestamp})
		return nil, nil
	}).WithSideOutputs(timerTag)
	stream.AddSink(mainSink)
	stream.GetSideOutput(timerTag).AddSink(timerSink)
	if _, err := env.ExecuteWithName(ctx, "stateful-sdk-example"); err != nil {
		return output{}, err
	}
	result := output{Main: mainSink.snapshot(), Timers: timerSink.snapshot(), SourceAttempts: attempts.Load()}
	sort.Strings(result.Main)
	sort.Strings(result.Timers)
	return result, nil
}

func main() {
	recoverSource := flag.Bool("recover", false, "inject a source failure after a completed checkpoint")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := run(ctx, *recoverSource)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
