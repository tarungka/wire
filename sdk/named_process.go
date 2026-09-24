package sdk

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/tarungka/wire/internal/engine"
)

// ProcessNamed applies a registered worker Process factory to a keyed stream.
func (ks *KeyedStream) ProcessNamed(name, className string, config []byte) *DataStream {
	id := ks.env.graph.addNode(&StreamNode{Name: name, Type: NodeProcess, ClassName: className, Config: append([]byte(nil), config...)})
	ks.env.graph.addEdge(ks.nodeID, id, ShuffleForward)
	return &DataStream{env: ks.env, nodeID: id}
}

// ProcessOperator is the runtime adapter for a managed SDK Process function.
// Register one fresh operator per task using worker.RegisterProcess. The runtime
// opens it, restores its snapshot, then serializes data/watermark/checkpoint calls.
type ProcessOperator struct{ *processAdapter }

func NewProcessOperator(fn ProcessFunc, onTimer TimerFunc, backend StateBackendConfig, tags ...OutputTag) *ProcessOperator {
	op := &processAdapter{fn: fn, onTimer: onTimer, config: backend}
	for _, tag := range tags {
		op.sideTags = append(op.sideTags, tag.Name)
	}
	return &ProcessOperator{processAdapter: op}
}

func (op *ProcessOperator) SetSideOutputTags(tags []string) {
	op.sideTags = append([]string(nil), tags...)
}
func (op *ProcessOperator) SetProcessIdentity(jobID, operatorID string, instance int) {
	op.config = op.config.forOperator(jobID, operatorID)
	op.instance = instance
}

func (config StateBackendConfig) forOperator(jobID, operatorID string) StateBackendConfig {
	if config.DataDir != "" {
		identity := sha256.Sum256([]byte(jobID + "\x00" + operatorID))
		config.DataDir = filepath.Join(config.DataDir, fmt.Sprintf("%x", identity))
	}
	return config
}

// SetStateBackendFactory allows worker deployment to apply per-job storage.
func (op *ProcessOperator) SetStateBackendFactory(factory func() (engine.StateBackend, func(), error)) {
	op.backendFactory = factory
}
