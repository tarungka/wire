package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/rpc"
)

// attachTaskAddressesLocked resolves all endpoints before mutating descriptors.
// Caller holds c.mu, so placement and advertised worker addresses stay stable.
func (c *Coordinator) attachTaskAddressesLocked(assignments map[string][]rpc.TaskDescriptor) error {
	addresses := make(map[string]string)
	owners := make(map[string]string)
	for workerID, tasks := range assignments {
		worker := c.workers[workerID]
		if worker == nil {
			return fmt.Errorf("unknown assigned worker %q", workerID)
		}
		for _, task := range tasks {
			if _, exists := addresses[task.TaskID]; exists {
				return fmt.Errorf("duplicate task placement %q", task.TaskID)
			}
			addresses[task.TaskID] = worker.Address
			owners[task.TaskID] = workerID
		}
	}
	for _, tasks := range assignments {
		for _, task := range tasks {
			for _, input := range task.Upstream {
				if addresses[input.TaskID] == "" {
					return fmt.Errorf("upstream task %q has no worker address", input.TaskID)
				}
			}
			for _, output := range task.Downstream {
				if addresses[output.TaskID] == "" {
					return fmt.Errorf("downstream task %q has no worker address", output.TaskID)
				}
			}
		}
	}
	for _, tasks := range assignments {
		for i := range tasks {
			for j := range tasks[i].Upstream {
				tasks[i].Upstream[j].Address = addresses[tasks[i].Upstream[j].TaskID]
				tasks[i].Upstream[j].WorkerID = owners[tasks[i].Upstream[j].TaskID]
			}
			for j := range tasks[i].Downstream {
				tasks[i].Downstream[j].Address = addresses[tasks[i].Downstream[j].TaskID]
			}
		}
	}
	return nil
}
