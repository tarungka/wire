package sdk

import "fmt"

// StreamNodeType identifies the kind of operator in the stream graph.
type StreamNodeType uint8

const (
	NodeSource StreamNodeType = iota
	NodeMap
	NodeFlatMap
	NodeFilter
	NodeKeyBy
	NodeWindow
	NodeReduce
	NodeProcess
	NodeSink
)

// ShuffleType defines how events are partitioned between operators.
type ShuffleType uint8

const (
	ShuffleForward   ShuffleType = iota // 1:1 mapping.
	ShuffleHash                         // Partition by key hash.
	ShuffleBroadcast                    // Send to all downstream.
	ShuffleRebalance                    // Round-robin.
)

// StreamNode represents a single operator in the logical DAG.
type StreamNode struct {
	ID          int
	Name        string
	Type        StreamNodeType
	Parallelism int

	// ClassName identifies a registered operator factory on the worker.
	// Required for Cluster mode (see worker.Registry); unused in Embedded.
	ClassName string
	// Config carries the operator-specific configuration bytes that are
	// passed verbatim to the worker factory at deploy time. Typically
	// msgpack-encoded. Only meaningful in Cluster mode.
	Config []byte

	// Operator function pointers — exactly one is set in Embedded mode.
	// In Cluster mode these are nil and ClassName is used instead.
	MapFn      MapFunc
	FlatMapFn  FlatMapFunc
	FilterFn   FilterFunc
	KeyByFn    KeySelector
	ProcessFn  ProcessFunc
	ReduceFn   ReduceFunc
	WindowFn   WindowFunc
	Source     Source
	Sink       Sink
	Window     WindowAssigner
	Aggregator Aggregator

	// Timestamp extractor (optional).
	TimestampExtractor TimestampExtractor

	// Windowed stream config.
	AllowedLateness int64 // millis
}

// StreamEdge connects two nodes in the graph.
type StreamEdge struct {
	SourceID int
	TargetID int
	Shuffle  ShuffleType
}

// StreamGraph is the internal DAG representation of a pipeline.
// Users interact with DataStream/KeyedStream/WindowedStream, never the graph directly.
type StreamGraph struct {
	nodes  map[int]*StreamNode
	edges  []StreamEdge
	names  map[string]bool
	nextID int
}

func newStreamGraph() *StreamGraph {
	return &StreamGraph{
		nodes: make(map[int]*StreamNode),
		names: make(map[string]bool),
	}
}

// addNode adds a node to the graph and returns its ID.
func (g *StreamGraph) addNode(node *StreamNode) int {
	id := g.nextID
	g.nextID++
	node.ID = id
	g.nodes[id] = node
	if node.Name != "" {
		g.names[node.Name] = true
	}
	return id
}

// addEdge adds an edge between two nodes.
func (g *StreamGraph) addEdge(sourceID, targetID int, shuffle ShuffleType) {
	g.edges = append(g.edges, StreamEdge{
		SourceID: sourceID,
		TargetID: targetID,
		Shuffle:  shuffle,
	})
}

// validateForCluster checks the subset of SDK graphs supported by the current
// cluster runtime. Cluster mode resolves operators by ClassName from the
// worker registry; inline closures cannot be serialized across the RPC
// boundary. The current cluster scheduler also supports only forward, linear
// chains. Keyed shuffle, stateful process/reduce, and windows are embedded-only
// or target capabilities until distributed shuffle and managed state are wired
// through the worker runtime.
func (g *StreamGraph) validateForCluster() error {
	for _, node := range g.nodes {
		if !nodeTypeSupportedInCluster(node.Type) {
			return fmt.Errorf(
				"%w: cluster mode does not yet support %s operators; "+
					"the current cluster runtime supports only named source/map/flatmap/filter/sink "+
					"operators with forward edges. Run this graph in Embedded mode or remove %q until "+
					"distributed shuffle and managed state land. See docs/current-capabilities.md",
				ErrInvalidConfig, nodeTypeString(node.Type), nodeDisplayName(node),
			)
		}
		if node.ClassName == "" {
			return fmt.Errorf(
				"%w: cluster-mode graph requires ClassName on every node; "+
					"node %q (%s) has no ClassName. Use AddSourceNamed, MapNamed, "+
					"FlatMapNamed, FilterNamed, or AddSinkNamed, or run this graph in Embedded mode. "+
					"See docs/current-capabilities.md",
				ErrInvalidConfig, nodeDisplayName(node), nodeTypeString(node.Type),
			)
		}
	}
	for _, edge := range g.edges {
		if edge.Shuffle != ShuffleForward {
			return fmt.Errorf(
				"%w: cluster mode does not yet support %s shuffle from %q to %q; "+
					"only forward edges are supported. KeyBy creates hash shuffle and requires "+
					"Milestone 1 distributed shuffle. See docs/current-capabilities.md",
				ErrInvalidConfig,
				shuffleTypeString(edge.Shuffle),
				nodeDisplayName(g.nodes[edge.SourceID]),
				nodeDisplayName(g.nodes[edge.TargetID]),
			)
		}
	}
	return nil
}

func nodeTypeSupportedInCluster(t StreamNodeType) bool {
	switch t {
	case NodeSource, NodeMap, NodeFlatMap, NodeFilter, NodeSink:
		return true
	default:
		return false
	}
}

// validate checks the graph for structural errors.
func (g *StreamGraph) validate() error {
	if len(g.nodes) == 0 {
		return ErrEmptyPipeline
	}

	hasSources := false
	hasSinks := false
	for _, node := range g.nodes {
		if node.Type == NodeSource {
			hasSources = true
		}
		if node.Type == NodeSink {
			hasSinks = true
		}
	}
	if !hasSources {
		return ErrNoSources
	}
	if !hasSinks {
		return ErrNoSinks
	}

	// Check for duplicate names.
	nameCount := make(map[string]int)
	for _, node := range g.nodes {
		if node.Name != "" {
			nameCount[node.Name]++
			if nameCount[node.Name] > 1 {
				return fmt.Errorf("%w: %q", ErrDuplicateName, node.Name)
			}
		}
	}

	// Cycle detection using DFS.
	if g.hasCycle() {
		return ErrCyclicGraph
	}

	return nil
}

func nodeDisplayName(node *StreamNode) string {
	if node == nil {
		return "<unknown>"
	}
	if node.Name != "" {
		return node.Name
	}
	return fmt.Sprintf("%s-%d", nodeTypeString(node.Type), node.ID)
}

func shuffleTypeString(s ShuffleType) string {
	switch s {
	case ShuffleForward:
		return "forward"
	case ShuffleHash:
		return "hash"
	case ShuffleBroadcast:
		return "broadcast"
	case ShuffleRebalance:
		return "rebalance"
	default:
		return "unknown"
	}
}

// hasCycle performs a DFS-based cycle detection.
func (g *StreamGraph) hasCycle() bool {
	// Build adjacency list.
	adj := make(map[int][]int)
	for _, edge := range g.edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
	}

	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)

	color := make(map[int]int)
	for id := range g.nodes {
		color[id] = white
	}

	var dfs func(id int) bool
	dfs = func(id int) bool {
		color[id] = gray
		for _, next := range adj[id] {
			if color[next] == gray {
				return true
			}
			if color[next] == white && dfs(next) {
				return true
			}
		}
		color[id] = black
		return false
	}

	for id := range g.nodes {
		if color[id] == white {
			if dfs(id) {
				return true
			}
		}
	}
	return false
}

// topoSort returns nodes in topological order.
func (g *StreamGraph) topoSort() []*StreamNode {
	// Build adjacency and in-degree.
	adj := make(map[int][]int)
	inDegree := make(map[int]int)
	for id := range g.nodes {
		inDegree[id] = 0
	}
	for _, edge := range g.edges {
		adj[edge.SourceID] = append(adj[edge.SourceID], edge.TargetID)
		inDegree[edge.TargetID]++
	}

	// Kahn's algorithm.
	var queue []int
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var result []*StreamNode
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		result = append(result, g.nodes[id])
		for _, next := range adj[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	return result
}

// sources returns all source nodes.
func (g *StreamGraph) sources() []*StreamNode {
	var result []*StreamNode
	for _, node := range g.nodes {
		if node.Type == NodeSource {
			result = append(result, node)
		}
	}
	return result
}

// downstream returns the nodes downstream of the given node ID.
func (g *StreamGraph) downstream(nodeID int) []StreamEdge {
	var result []StreamEdge
	for _, edge := range g.edges {
		if edge.SourceID == nodeID {
			result = append(result, edge)
		}
	}
	return result
}

// upstream returns the edges into the given node ID.
func (g *StreamGraph) upstream(nodeID int) []StreamEdge {
	var result []StreamEdge
	for _, edge := range g.edges {
		if edge.TargetID == nodeID {
			result = append(result, edge)
		}
	}
	return result
}
