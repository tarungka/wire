package sdk

import "fmt"

// DataStream represents an unbounded stream of events.
// Methods return a new DataStream (or KeyedStream) pointing to the newly
// added graph node, enabling fluent chaining.
type DataStream struct {
	env    *StreamExecutionEnvironment
	nodeID int
}

// Map applies a one-to-one transformation.
func (ds *DataStream) Map(fn MapFunc) *DataStream {
	return ds.mapWithName(fn, "")
}

// MapWithName applies a one-to-one transformation with a named operator.
func (ds *DataStream) MapWithName(name string, fn MapFunc) *DataStream {
	return ds.mapWithName(fn, name)
}

func (ds *DataStream) mapWithName(fn MapFunc, name string) *DataStream {
	node := &StreamNode{
		Name:  name,
		Type:  NodeMap,
		MapFn: fn,
	}
	id := ds.env.graph.addNode(node)
	ds.env.graph.addEdge(ds.nodeID, id, ShuffleForward)
	return &DataStream{env: ds.env, nodeID: id}
}

// FlatMap applies a one-to-many transformation.
func (ds *DataStream) FlatMap(fn FlatMapFunc) *DataStream {
	return ds.flatMapWithName(fn, "")
}

// FlatMapWithName applies a one-to-many transformation with a named operator.
func (ds *DataStream) FlatMapWithName(name string, fn FlatMapFunc) *DataStream {
	return ds.flatMapWithName(fn, name)
}

func (ds *DataStream) flatMapWithName(fn FlatMapFunc, name string) *DataStream {
	node := &StreamNode{
		Name:      name,
		Type:      NodeFlatMap,
		FlatMapFn: fn,
	}
	id := ds.env.graph.addNode(node)
	ds.env.graph.addEdge(ds.nodeID, id, ShuffleForward)
	return &DataStream{env: ds.env, nodeID: id}
}

// Filter keeps only events that satisfy the predicate.
func (ds *DataStream) Filter(fn FilterFunc) *DataStream {
	return ds.filterWithName(fn, "")
}

// FilterWithName filters with a named operator.
func (ds *DataStream) FilterWithName(name string, fn FilterFunc) *DataStream {
	return ds.filterWithName(fn, name)
}

func (ds *DataStream) filterWithName(fn FilterFunc, name string) *DataStream {
	node := &StreamNode{
		Name:     name,
		Type:     NodeFilter,
		FilterFn: fn,
	}
	id := ds.env.graph.addNode(node)
	ds.env.graph.addEdge(ds.nodeID, id, ShuffleForward)
	return &DataStream{env: ds.env, nodeID: id}
}

// KeyBy partitions the stream by key, returning a KeyedStream.
func (ds *DataStream) KeyBy(selector KeySelector) *KeyedStream {
	return ds.keyByWithName(selector, "")
}

// KeyByWithName partitions the stream by key with a named operator.
func (ds *DataStream) KeyByWithName(name string, selector KeySelector) *KeyedStream {
	return ds.keyByWithName(selector, name)
}

func (ds *DataStream) keyByWithName(selector KeySelector, name string) *KeyedStream {
	node := &StreamNode{
		Name:    name,
		Type:    NodeKeyBy,
		KeyByFn: selector,
	}
	id := ds.env.graph.addNode(node)
	ds.env.graph.addEdge(ds.nodeID, id, ShuffleHash)
	return &KeyedStream{env: ds.env, nodeID: id}
}

// Union merges this stream with one or more other streams.
func (ds *DataStream) Union(others ...*DataStream) *DataStream {
	// All input streams feed into ds's node via forward edges.
	for _, other := range others {
		ds.env.graph.addEdge(other.nodeID, ds.nodeID, ShuffleForward)
	}
	return ds
}

// AssignTimestamps sets a custom timestamp extractor.
func (ds *DataStream) AssignTimestamps(extractor TimestampExtractor) *DataStream {
	ds.env.graph.nodes[ds.nodeID].TimestampExtractor = extractor
	return ds
}

// AddSink adds a terminal sink operator.
func (ds *DataStream) AddSink(sink Sink) *DataStream {
	return ds.addSinkWithName(sink, "")
}

// AddSinkWithName adds a terminal sink operator with a name.
func (ds *DataStream) AddSinkWithName(name string, sink Sink) *DataStream {
	return ds.addSinkWithName(sink, name)
}

func (ds *DataStream) addSinkWithName(sink Sink, name string) *DataStream {
	node := &StreamNode{
		Name: name,
		Type: NodeSink,
		Sink: sink,
	}
	id := ds.env.graph.addNode(node)
	ds.env.graph.addEdge(ds.nodeID, id, ShuffleForward)
	return &DataStream{env: ds.env, nodeID: id}
}

// SetParallelism sets the parallelism for this operator.
func (ds *DataStream) SetParallelism(p int) *DataStream {
	ds.env.graph.nodes[ds.nodeID].Parallelism = p
	return ds
}

// Name sets a human-readable name for this operator.
func (ds *DataStream) Name(name string) *DataStream {
	node := ds.env.graph.nodes[ds.nodeID]
	// Remove old name from tracking if present.
	if node.Name != "" {
		delete(ds.env.graph.names, node.Name)
	}
	node.Name = name
	if name != "" {
		ds.env.graph.names[name] = true
	}
	return ds
}

// autoName generates a unique name for an operator if none is provided.
func autoName(g *StreamGraph, prefix string) string {
	name := prefix
	if !g.names[name] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", prefix, i)
		if !g.names[candidate] {
			return candidate
		}
	}
}
