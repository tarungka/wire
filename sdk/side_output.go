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
	if tag.Name == "" || !node.hasSideOutput(tag.Name) {
		panic("sdk: output tag is not declared on this operator")
	}
	return &DataStream{env: ds.env, nodeID: ds.nodeID, outputTag: tag.Name}
}
func (ds *DataStream) addEdge(target int, shuffle ShuffleType) {
	ds.env.graph.edges = append(ds.env.graph.edges, StreamEdge{SourceID: ds.nodeID, TargetID: target, Shuffle: shuffle, SideOutput: ds.outputTag})
}

// WithSideOutputs declares the tags a Process function may emit. Declarations
// allow graph validation before operators start and catch misspelled tags.
func (ds *DataStream) WithSideOutputs(tags ...OutputTag) *DataStream {
	node := ds.env.graph.nodes[ds.nodeID]
	if node.Type != NodeProcess {
		panic("sdk: WithSideOutputs requires Process")
	}
	for _, tag := range tags {
		if tag.Name == "" {
			panic("sdk: empty side output tag")
		}
		if !node.hasSideOutput(tag.Name) {
			node.SideOutputTags = append(node.SideOutputTags, tag.Name)
		}
	}
	return ds
}
func (node *StreamNode) hasSideOutput(tag string) bool {
	if node.LateOutputTag == tag && tag != "" {
		return true
	}
	for _, declared := range node.SideOutputTags {
		if declared == tag {
			return true
		}
	}
	return false
}
