package coordinator

import "net/http"

func (s *HTTPServer) handleClusterStatus(w http.ResponseWriter, _ *http.Request) {
	info, isSelf, err := s.coord.GetLeaderInfo()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "NO_LEADER", "no leader elected")
		return
	}

	leader := &leaderResponse{
		LeaderID:       info.NodeID,
		LeaderHTTPAddr: info.Address,
		LeaderEpoch:    info.Epoch,
		IsSelf:         isSelf,
	}

	s.coord.mu.RLock()
	workers := make([]nodeResponse, 0, len(s.coord.workers))
	for _, w := range s.coord.workers {
		workers = append(workers, nodeResponse{
			ID:                 w.ID,
			Address:            w.Address,
			TaskSlotsTotal:     w.TaskSlotsTotal,
			TaskSlotsAvailable: w.TaskSlotsAvailable,
			LastHeartbeat:      formatTime(w.LastHeartbeat),
			RunningTasks:       w.RunningTasks,
		})
	}
	s.coord.mu.RUnlock()

	writeJSON(w, http.StatusOK, clusterStatusResponse{
		Leader:  leader,
		Workers: workers,
	})
}

func (s *HTTPServer) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")

	s.coord.mu.Lock()
	_, ok := s.coord.workers[nodeID]
	if !ok {
		s.coord.mu.Unlock()
		writeError(w, http.StatusNotFound, "NODE_NOT_FOUND", "worker node not found")
		return
	}
	delete(s.coord.workers, nodeID)
	s.coord.mu.Unlock()

	// Delete from store as well.
	s.coord.store.Delete(WorkerMetaKey(nodeID))

	s.coord.log.Info().Str("node_id", nodeID).Msg("worker node removed")
	// TODO: reschedule tasks from the removed worker

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "node_id": nodeID})
}
