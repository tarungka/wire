package sdk

func (ws *WindowedStream) SetLateOutputTag(tag OutputTag) *WindowedStream {
	if tag.Name == "" {
		panic("sdk: output tag must not be empty")
	}
	ws.lateOutputTag = tag.Name
	return ws
}
func (ds *DataStream) GetSideOutput(tag OutputTag) *DataStream {
	node := ds.env.graph.nodes[ds.nodeID]
	if tag.Name == "" || node.LateOutputTag != tag.Name {
		panic("sdk: output tag is not declared on this window")
	}
	return &DataStream{env: ds.env, nodeID: ds.nodeID, outputTag: tag.Name}
}
func (ds *DataStream) addEdge(target int, shuffle ShuffleType) {
	ds.env.graph.edges = append(ds.env.graph.edges, StreamEdge{SourceID: ds.nodeID, TargetID: target, Shuffle: shuffle, SideOutput: ds.outputTag})
}
