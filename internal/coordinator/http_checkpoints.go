package coordinator

import (
	"net/http"
	"strconv"

	"github.com/tarungka/wire/internal/protocol"
)

func checkpointResponse(checkpoint *CheckpointMeta) map[string]any {
	return map[string]any{"id": checkpoint.ID, "job_id": checkpoint.JobID, "epoch": checkpoint.EpochID, "status": checkpoint.Status.String(), "timestamp": checkpoint.Timestamp}
}

func (s *HTTPServer) handleTriggerCheckpoint(w http.ResponseWriter, r *http.Request) {
	checkpoint, err := s.coord.TriggerCheckpoint(r.PathValue("job_id"))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, checkpointResponse(checkpoint))
}

func (s *HTTPServer) handleGetCheckpoint(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("checkpoint_id"), 10, 64)
	if err != nil || id == 0 {
		http.Error(w, "invalid checkpoint ID", http.StatusBadRequest)
		return
	}
	data, err := s.coord.store.Get(CheckpointKey(r.PathValue("job_id"), id))
	if err != nil {
		http.Error(w, "checkpoint storage unavailable", http.StatusInternalServerError)
		return
	}
	if len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	var checkpoint CheckpointMeta
	if err := protocol.DecodeMsgPack(data, &checkpoint); err != nil {
		http.Error(w, "invalid checkpoint metadata", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, checkpointResponse(&checkpoint))
}
