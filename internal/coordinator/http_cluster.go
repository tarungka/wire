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

	wms := s.coord.ListWorkers()
	workers := make([]nodeResponse, 0, len(wms))
	for _, w := range wms {
		workers = append(workers, nodeResponse{
			ID:                 w.ID,
			Address:            w.Address,
			TaskSlotsTotal:     w.TaskSlotsTotal,
			TaskSlotsAvailable: w.TaskSlotsAvailable,
			LastHeartbeat:      formatTime(w.LastHeartbeat),
			RunningTasks:       w.RunningTasks,
		})
	}

	writeJSON(w, http.StatusOK, clusterStatusResponse{
		Leader:  leader,
		Workers: workers,
	})
}

func (s *HTTPServer) handleRemoveNode(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")

	if err := s.coord.RemoveWorker(nodeID); err != nil {
		if err == ErrWorkerNotFound {
			writeError(w, http.StatusNotFound, "NODE_NOT_FOUND", "worker node not found")
			return
		}
		s.coord.log.Error().Err(err).Str("node_id", nodeID).Msg("failed to delete worker from store")
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to remove node from store")
		return
	}

	// TODO: reschedule tasks from the removed worker

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "node_id": nodeID})
}
