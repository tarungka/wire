package sdk

// KeyedStream represents a stream that has been partitioned by key.
// Operations on a KeyedStream have access to per-key state.
type KeyedStream struct {
	env    *StreamExecutionEnvironment
	nodeID int
}

// Process applies a stateful processing function, returning a DataStream.
func (ks *KeyedStream) Process(fn ProcessFunc) *DataStream {
	return ks.processWithName(fn, "")
}

// ProcessWithName applies a stateful processing function with a named operator.
func (ks *KeyedStream) ProcessWithName(name string, fn ProcessFunc) *DataStream {
	return ks.processWithName(fn, name)
}

func (ks *KeyedStream) processWithName(fn ProcessFunc, name string) *DataStream {
	node := &StreamNode{
		Name:      name,
		Type:      NodeProcess,
		ProcessFn: fn,
	}
	id := ks.env.graph.addNode(node)
	ks.env.graph.addEdge(ks.nodeID, id, ShuffleForward)
	return &DataStream{env: ks.env, nodeID: id}
}

// Window assigns a WindowAssigner, returning a WindowedStream.
func (ks *KeyedStream) Window(assigner WindowAssigner) *WindowedStream {
	return &WindowedStream{
		env:      ks.env,
		nodeID:   ks.nodeID,
		assigner: assigner,
	}
}
