package sdk

import "time"

// WindowedStream represents a keyed stream with a window assigner applied.
type WindowedStream struct {
	env             *StreamExecutionEnvironment
	nodeID          int
	assigner        WindowAssigner
	allowedLateness int64 // millis
	lateOutputTag   string
}

// Aggregate applies an Aggregator to each window, returning a DataStream.
func (ws *WindowedStream) Aggregate(agg Aggregator) *DataStream {
	node := &StreamNode{
		Type:            NodeWindow,
		Window:          ws.assigner,
		Aggregator:      agg,
		AllowedLateness: ws.allowedLateness,
		LateOutputTag:   ws.lateOutputTag,
	}
	id := ws.env.graph.addNode(node)
	ws.env.graph.addEdge(ws.nodeID, id, ShuffleForward)
	return &DataStream{env: ws.env, nodeID: id}
}

// Reduce applies a ReduceFunc to each window, returning a DataStream.
func (ws *WindowedStream) Reduce(fn ReduceFunc) *DataStream {
	node := &StreamNode{
		Type:            NodeReduce,
		ReduceFn:        fn,
		Window:          ws.assigner,
		AllowedLateness: ws.allowedLateness,
		LateOutputTag:   ws.lateOutputTag,
	}
	id := ws.env.graph.addNode(node)
	ws.env.graph.addEdge(ws.nodeID, id, ShuffleForward)
	return &DataStream{env: ws.env, nodeID: id}
}

// Apply applies a WindowFunc to each window, returning a DataStream.
func (ws *WindowedStream) Apply(fn WindowFunc) *DataStream {
	node := &StreamNode{
		Type:            NodeWindow,
		WindowFn:        fn,
		Window:          ws.assigner,
		AllowedLateness: ws.allowedLateness,
		LateOutputTag:   ws.lateOutputTag,
	}
	id := ws.env.graph.addNode(node)
	ws.env.graph.addEdge(ws.nodeID, id, ShuffleForward)
	return &DataStream{env: ws.env, nodeID: id}
}

// AllowedLateness sets the retention period after window end. Pass a
// time.Duration (for example, 30*time.Second). Legacy int/int64 arguments retain
// their millisecond interpretation. Negative or sub-millisecond values panic.
func (ws *WindowedStream) AllowedLateness(value any) *WindowedStream {
	var millis int64
	switch duration := value.(type) {
	case time.Duration:
		if duration < 0 || duration%time.Millisecond != 0 {
			panic("sdk: allowed lateness must be nonnegative whole milliseconds")
		}
		millis = duration.Milliseconds()
	case int64:
		millis = duration
	case int:
		millis = int64(duration)
	default:
		panic("sdk: allowed lateness requires time.Duration or integer milliseconds")
	}
	if millis < 0 {
		panic("sdk: allowed lateness must be nonnegative")
	}
	ws.allowedLateness = millis
	return ws
}

// ApplyNamed selects a worker.RegisterWindow factory in cluster mode. The
// assigner and lateness are transmitted independently of the factory's config;
// the returned window must implement ConfigureWindow (as EventTimeWindowOperator
// does). Factory aggregation/encoder/backend choices are preserved.
func (ws *WindowedStream) ApplyNamed(name, className string, config []byte) *DataStream {
	if name == "" || className == "" {
		panic("sdk: named window requires a name and factory class")
	}
	node := &StreamNode{Type: NodeWindow, Name: name, ClassName: className, Config: append([]byte(nil), config...), Window: ws.assigner, AllowedLateness: ws.allowedLateness, LateOutputTag: ws.lateOutputTag}
	if _, err := windowDefinition(node); err != nil {
		panic(err)
	}
	id := ws.env.graph.addNode(node)
	ws.env.graph.addEdge(ws.nodeID, id, ShuffleForward)
	return &DataStream{env: ws.env, nodeID: id}
}
