package sdk

// ConnectedStream keeps two inputs distinct until their per-input functions
// have run. CoMap/CoFlatMap then merge outputs without imposing cross-input order.
type ConnectedStream struct{ first, second *DataStream }

func (ds *DataStream) Connect(other *DataStream) *ConnectedStream {
	if other == nil || other.env != ds.env {
		panic("sdk: connected inputs must belong to the same environment")
	}
	return &ConnectedStream{first: ds, second: other}
}
func (cs *ConnectedStream) CoMap(first, second MapFunc) *DataStream {
	return cs.first.Map(first).Union(cs.second.Map(second))
}
func (cs *ConnectedStream) CoFlatMap(first, second FlatMapFunc) *DataStream {
	return cs.first.FlatMap(first).Union(cs.second.FlatMap(second))
}

func (ds *DataStream) redistribute(shuffle ShuffleType) *DataStream {
	id := ds.env.graph.addNode(&StreamNode{Type: NodeMap, ClassName: "wire.identity", MapFn: identityEvent})
	ds.addEdge(id, shuffle)
	return &DataStream{env: ds.env, nodeID: id}
}

// Rebalance distributes records round-robin across downstream instances.
func (ds *DataStream) Rebalance() *DataStream { return ds.redistribute(ShuffleRebalance) }

// Broadcast sends an independent copy to every downstream instance.
func (ds *DataStream) Broadcast() *DataStream { return ds.redistribute(ShuffleBroadcast) }
