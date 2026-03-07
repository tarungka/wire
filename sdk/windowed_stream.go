package sdk

// WindowedStream represents a keyed stream with a window assigner applied.
type WindowedStream struct {
	env             *StreamExecutionEnvironment
	nodeID          int
	assigner        WindowAssigner
	allowedLateness int64 // millis
}

// Aggregate applies an Aggregator to each window, returning a DataStream.
func (ws *WindowedStream) Aggregate(agg Aggregator) *DataStream {
	node := &StreamNode{
		Type:            NodeWindow,
		Window:          ws.assigner,
		Aggregator:      agg,
		AllowedLateness: ws.allowedLateness,
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
	}
	id := ws.env.graph.addNode(node)
	ws.env.graph.addEdge(ws.nodeID, id, ShuffleForward)
	return &DataStream{env: ws.env, nodeID: id}
}

// AllowedLateness sets the maximum allowed lateness for late-arriving events.
func (ws *WindowedStream) AllowedLateness(millis int64) *WindowedStream {
	ws.allowedLateness = millis
	return ws
}
