package rpc

// CheckpointCleanupRequest names one immutable replica identity. EpochID fences
// the current coordinator command; SnapshotEpoch belongs to the stored archive.
type CheckpointCleanupRequest struct {
	WorkerID      string `codec:"wid"`
	EpochID       uint64 `codec:"eid"`
	JobID         string `codec:"jid"`
	SavepointID   string `codec:"sid"`
	TaskID        string `codec:"tid"`
	CheckpointID  uint64 `codec:"cid"`
	SnapshotEpoch uint64 `codec:"snapshot_epoch"`
}
