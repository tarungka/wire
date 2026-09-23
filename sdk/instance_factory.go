package sdk

import "fmt"

// InstanceContext identifies an embedded operator instance. Factories must
// return a fresh, unopened connector for each call; Open/Close belong to runtime.
type InstanceContext struct {
	Index       int
	Parallelism int
}

type SourceFactory func(InstanceContext) (Source, error)
type SinkFactory func(InstanceContext) (Sink, error)

func (env *StreamExecutionEnvironment) AddSourceFactory(name string, factory SourceFactory) *DataStream {
	id := env.graph.addNode(&StreamNode{Name: name, Type: NodeSource, SourceFactory: factory})
	return &DataStream{env: env, nodeID: id}
}
func (ds *DataStream) AddSinkFactory(name string, factory SinkFactory) *DataStream {
	id := ds.env.graph.addNode(&StreamNode{Name: name, Type: NodeSink, SinkFactory: factory})
	ds.addEdge(id, ShuffleForward)
	return &DataStream{env: ds.env, nodeID: id}
}

func instantiateNode(node *StreamNode, index, parallelism int) (StreamNode, error) {
	instance := *node
	var err error
	if node.SourceFactory != nil {
		instance.Source, err = node.SourceFactory(InstanceContext{Index: index, Parallelism: parallelism})
	}
	if err == nil && node.SinkFactory != nil {
		instance.Sink, err = node.SinkFactory(InstanceContext{Index: index, Parallelism: parallelism})
	}
	if err != nil {
		return instance, fmt.Errorf("sdk: %s instance %d: %w", node.Name, index, err)
	}
	if (node.Type == NodeSource && nilConnector(instance.Source)) || (node.Type == NodeSink && nilConnector(instance.Sink)) {
		return instance, fmt.Errorf("sdk: %s instance %d has no connector", node.Name, index)
	}
	if _, ok := instance.Sink.(TransactionalSink); ok {
		return instance, fmt.Errorf("sdk: transactional sinks require cluster checkpoint decisions")
	}
	return instance, nil
}
