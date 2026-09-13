package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

func TestHeartbeatRefreshesOnlyCurrentEpoch(t *testing.T) {
	for _, valid := range []bool{true, false} {
		name := "stale"
		if valid {
			name = "current"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := newTestCoordinator(t)
			old := time.Now().Add(-2 * c.config.WorkerTimeout)
			c.workers["worker"] = &WorkerMeta{ID: "worker", LastHeartbeat: old, TaskSlotsTotal: 4, TaskSlotsAvailable: 0}
			c.EnqueueCommand("worker", rpc.WorkerCommand{Type: rpc.CommandTypeDeployTask, TaskID: "pending"})
			req := rpc.HeartbeatRequest{WorkerID: "worker", EpochID: 5, Load: &rpc.WorkerLoad{ActiveSlots: 1}}
			if !valid {
				req.EpochID--
			}
			result, rpcErr := c.HandleHeartbeat(context.Background(), 1, encode(t, req))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			response := result.(*rpc.HeartbeatResponse)
			if response.Accepted != valid {
				t.Fatalf("response: %+v", response)
			}
			w := c.workers["worker"]
			if valid {
				if !w.LastHeartbeat.After(old) || w.TaskSlotsAvailable != 3 || len(response.Commands) != 1 {
					t.Fatalf("heartbeat did not refresh worker: %+v", w)
				}
			} else if !w.LastHeartbeat.Equal(old) || w.TaskSlotsAvailable != 0 || len(response.Commands) != 0 || len(c.DrainCommands("worker")) != 1 {
				t.Fatal("stale heartbeat changed liveness or consumed commands")
			}
		})
	}
}
