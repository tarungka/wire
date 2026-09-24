package transport

import (
	"fmt"

	"github.com/tarungka/wire/internal/protocol"
)

// TaskSource is an input authorized by the receiving task's deployment.
// WorkerID must be present when the mux binds peer identity to TLS certificates.
type TaskSource struct {
	TaskID         string
	WorkerID       string
	PartitionIndex uint16
}

// RegisterTaskSources installs immutable source ownership before any stream can
// enter the task's bounded queue. The map belongs to this registration generation.
func (m *Mux) RegisterTaskSources(taskID string, sources []TaskSource) error {
	type key struct {
		task      string
		partition uint16
	}
	allowed := make(map[key]string, len(sources))
	if len(sources) == 0 {
		return fmt.Errorf("transport: task sources required")
	}
	for _, source := range sources {
		if source.TaskID == "" || (m.cfg.RequirePeerIdentity && source.WorkerID == "") {
			return fmt.Errorf("transport: source task and authenticated worker ownership required")
		}
		k := key{source.TaskID, source.PartitionIndex}
		if _, exists := allowed[k]; exists {
			return fmt.Errorf("transport: duplicate task source")
		}
		allowed[k] = source.WorkerID
	}
	return m.registerTask(taskID, len(sources), true, func(peer string, header protocol.StreamHeaderMsg) bool {
		owner, ok := allowed[key{header.SourceTaskID, header.PartitionIndex}]
		return ok && (!m.cfg.RequirePeerIdentity || owner == peer)
	})
}
