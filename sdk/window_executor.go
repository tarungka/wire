package sdk

import (
	"context"
	"fmt"
	"math"
	"time"
)

// runWindows keeps records and watermarks in one ordered execution loop. The
// asynchronous embedded router cannot yet preserve that ordering across inputs.
func (ex *embeddedExecutor) runWindows(ctx context.Context, nodes []*StreamNode, jobName string) (result *JobResult, err error) {
	start := time.Now()
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("sdk: window pipeline panic: %v", p)
		}
		result = &JobResult{JobID: jobName, Err: err, Metrics: JobMetrics{Duration: time.Since(start)}}
	}()
	if ex.env.parallelism != 1 || ex.env.checkpointInterval != 0 || ex.env.restartStrategy.Type != RestartNone {
		return nil, fmt.Errorf("sdk: window execution currently requires parallelism 1, no checkpoints, and no restart strategy")
	}
	if len(nodes) < 2 || nodes[0].Type != NodeSource {
		return nil, fmt.Errorf("sdk: window pipeline requires one source")
	}
	windows := make(map[int]*windowRuntime)
	// Validate the entire graph before opening connectors.
	for i, node := range nodes {
		if node.Parallelism > 1 {
			return nil, fmt.Errorf("sdk: parallel window operators are not yet supported")
		}
		if i > 0 {
			incoming := 0
			for _, edge := range ex.env.graph.edges {
				if edge.TargetID == node.ID {
					incoming++
					if edge.SourceID != nodes[i-1].ID {
						return nil, fmt.Errorf("sdk: window execution requires a linear graph")
					}
				}
			}
			if incoming != 1 {
				return nil, fmt.Errorf("sdk: window execution requires a linear graph")
			}
		}
		switch node.Type {
		case NodeSource:
			if i != 0 || node.Source == nil {
				return nil, fmt.Errorf("sdk: window pipeline requires one source")
			}
		case NodeMap, NodeFlatMap, NodeFilter, NodeKeyBy:
		case NodeWindow, NodeReduce:
			w, e := newWindowRuntime(node)
			if e != nil {
				return nil, e
			}
			windows[i] = w
		case NodeSink:
			if node.Sink == nil || i != len(nodes)-1 {
				return nil, fmt.Errorf("sdk: window pipeline requires a terminal sink")
			}
		default:
			return nil, fmt.Errorf("sdk: unsupported node %d in window pipeline", node.Type)
		}
	}
	if nodes[len(nodes)-1].Type != NodeSink {
		return nil, fmt.Errorf("sdk: window pipeline requires a terminal sink")
	}
	source := nodes[0].Source
	sink := nodes[len(nodes)-1].Sink
	if err = sink.Open(ctx); err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := sink.Close(); err == nil {
			err = closeErr
		}
	}()
	if err = source.Open(ctx); err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := source.Close(); err == nil {
			err = closeErr
		}
	}()
	var deliver func(int, Event) error
	deliver = func(i int, event Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		node := nodes[i]
		var output []Event
		switch node.Type {
		case NodeMap:
			e, err := node.MapFn(event)
			if err != nil {
				return err
			}
			output = []Event{e}
		case NodeFilter:
			keep, err := node.FilterFn(event)
			if err != nil {
				return err
			}
			if keep {
				output = []Event{event}
			}
		case NodeFlatMap:
			events, err := node.FlatMapFn(event)
			if err != nil {
				return err
			}
			output = events
		case NodeKeyBy:
			key, err := node.KeyByFn(event)
			if err != nil {
				return err
			}
			event.Key = append([]byte(nil), key...)
			output = []Event{event}
		case NodeWindow, NodeReduce:
			events, err := windows[i].process(event)
			if err != nil {
				return err
			}
			output = events
		case NodeSink:
			return sink.Write(ctx, event)
		}
		for _, e := range output {
			if err := deliver(i+1, e); err != nil {
				return err
			}
		}
		return nil
	}
	advance := func(watermark int64) error {
		// Upstream windows emit their results before downstream windows see the
		// same watermark, preserving event-time order for cascaded windows.
		for i := 1; i < len(nodes)-1; i++ {
			if w := windows[i]; w != nil {
				events, err := w.watermark(watermark)
				if err != nil {
					return err
				}
				for _, event := range events {
					if err := deliver(i+1, event); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		batch, e := source.ReadBatch(ctx)
		if e != nil {
			return nil, e
		}
		if batch == nil {
			return nil, advance(math.MaxInt64)
		}
		for _, event := range batch {
			if extractor := nodes[0].TimestampExtractor; extractor != nil {
				event.EventTime = extractor(event)
			}
			if err = deliver(1, event); err != nil {
				return nil, err
			}
		}
		if err = advance(source.GenerateWatermark()); err != nil {
			return nil, err
		}
	}
}
