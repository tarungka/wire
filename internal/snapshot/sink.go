package snapshot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/raft"
)

const (
	maxFilenameLen = 255
)

// Sink is a sink for writing snapshot data to a Snapshot store.
type Sink struct {
	str  *Store
	meta *raft.SnapshotMeta

	snapDirPath    string
	snapTmpDirPath string
	dataFD         *os.File
	opened         bool
}

// NewSink creates a new Sink object.
func NewSink(str *Store, meta *raft.SnapshotMeta) *Sink {
	return &Sink{
		str:  str,
		meta: meta,
	}
}

// Open opens the sink for writing.
func (s *Sink) Open() error {
	if s.opened {
		return nil
	}
	s.opened = true

	// Make temp snapshot directory
	s.snapDirPath = filepath.Join(s.str.Dir(), s.meta.ID)
	s.snapTmpDirPath = tmpName(s.snapDirPath)
	if err := os.MkdirAll(s.snapTmpDirPath, 0755); err != nil {
		return err
	}

	dataPath := filepath.Join(s.snapTmpDirPath, s.meta.ID+".data")
	dataFD, err := os.Create(dataPath)
	if err != nil {
		return err
	}
	s.dataFD = dataFD
	return nil
}

// Write writes snapshot data to the sink. The snapshot is not in place
// until Close is called.
func (s *Sink) Write(p []byte) (n int, err error) {
	return s.dataFD.Write(p)
}

// ID returns the ID of the snapshot being written.
func (s *Sink) ID() string {
	return s.meta.ID
}

// Cancel cancels the snapshot. Cancel must be called if the snapshot is not
// going to be closed.
func (s *Sink) Cancel() error {
	if !s.opened {
		return nil
	}
	s.opened = false
	if err := s.dataFD.Close(); err != nil {
		return err
	}
	s.dataFD = nil
	return RemoveAllTmpSnapshotData(s.str.Dir())
}

// Close closes the sink, and finalizes creation of the snapshot. It is critical
// that Close is called, or the snapshot will not be in place. It is OK to call
// Close without every calling Write. In that case the Snapshot will be finalized
// as usual, but will effectively be the same as the previously created snapshot.
func (s *Sink) Close() error {
	if !s.opened {
		return nil
	}
	s.opened = false

	if err := s.dataFD.Close(); err != nil {
		return err
	}

	// Write meta data
	if err := s.writeMeta(s.snapTmpDirPath); err != nil {
		return err
	}

	if err := s.processSnapshotData(); err != nil {
		return err
	}

	// Get size of BadgerDB file and set in meta.
	dbPath, err := s.str.getDBPath()
	if err != nil {
		return err
	}
	fi, err := os.Stat(dbPath)
	if err != nil {
		return err
	}
	if err := updateMetaSize(s.snapDirPath, fi.Size()); err != nil {
		return fmt.Errorf("failed to update snapshot meta size: %s", err.Error())
	}

	if err := s.str.unsetFullNeeded(); err != nil {
		return err
	}

	_, err = s.str.Reap()
	return err
}

func (s *Sink) processSnapshotData() (retErr error) {
	defer func() {
		if retErr != nil {
			err := RemoveAllTmpSnapshotData(s.str.Dir())
			if err != nil {
				s.str.logger.Printf("failed to remove temporary snapshot data: %s", err.Error())
			}
		}
	}()

	// Check the state of the store before processing this new snapshot. This
	// allows us to perform some sanity checks on the incoming snapshot data.
	snapshots, err := s.str.getSnapshots()
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		// if !db.IsValidSQLiteFile(s.dataFD.Name()) {
		// 	// We have no snapshots yet, so the incoming data must be a valid BadgerDB file.
		// 	return fmt.Errorf("data for first snapshot must be a valid BadgerDB file")
		// }
	} else if len(snapshots) > 0 {
		// Confirm that existing snapshot is "less" than the incoming snapshot -- in other
		// words that it is from a point earlier in life of the Raft log.
		cmpSnapPrev := (*cmpSnapshotMeta)(snapshots[len(snapshots)-1])
		cmpSnapNew := (*cmpSnapshotMeta)(s.meta)
		if !cmpSnapPrev.Less(cmpSnapNew) {
			return fmt.Errorf("incoming snapshot %s is not later than most recent existing snapshot %s",
				cmpSnapNew.ID, cmpSnapPrev.ID)
		}
	}

	dataSz, err := fileSize(s.dataFD.Name())
	if err != nil {
		return err
	}

	// Writing zero data for a snapshot is acceptable, and indicates the snapshot
	// is empty. This could happen if lots of entries were written to the Raft log,
	// which would trigger a Raft snapshot, but those entries didn't actually change
	// the database. Otherwise, the data must be a valid BadgerDB file or WAL file.
	if dataSz != 0 {
		fdName := s.dataFD.Name()
		if dataSz <= maxFilenameLen {
			// It might contain the path of a file that we're to move here.
			b, err := os.ReadFile(fdName)
			if err != nil {
				return err
			}
			if fileExists(string(b)) {
				if err := os.Rename(string(b), fdName); err != nil {
					return err
				}
			}
		}
		// if db.IsValidSQLiteFile(fdName) {
		// 	if err := os.Rename(fdName, filepath.Join(s.str.Dir(), s.meta.ID+".db")); err != nil {
		// 		return err
		// 	}
		// } else if db.IsValidSQLiteWALFile(fdName) {
		// 	// With WAL data incoming, then we must have a valid BadgerDB file from the previous snapshot.
		// 	snapPrev := snapshots[len(snapshots)-1]
		// 	snapPrevDB := filepath.Join(s.str.Dir(), snapPrev.ID+".db")
		// 	if !db.IsValidSQLiteFile(snapPrevDB) {
		// 		return fmt.Errorf("previous snapshot data is not a BadgerDB file: %s", snapPrevDB)
		// 	}
		// 	if err := os.Rename(fdName, filepath.Join(s.str.Dir(), s.meta.ID+".db-wal")); err != nil {
		// 		return err
		// 	}
		// } else {
		// 	return fmt.Errorf("invalid snapshot data file: %s", fdName)
		// }
	}

	// Indicate snapshot data been successfully persisted to disk by renaming
	// the temp directory to a non-temporary name.
	if err := os.Rename(s.snapTmpDirPath, s.snapDirPath); err != nil {
		return err
	}
	if err := syncDirMaybe(s.str.Dir()); err != nil {
		return err
	}

	// Now check if we need to replay any WAL file into the previous BadgerDB file. This is
	// the final step of any snapshot process.
	snapshots, err = s.str.getSnapshots()
	if err != nil {
		return err
	}
	if len(snapshots) >= 2 {
		// snapPrev := snapshots[len(snapshots)-2]
		// snapNew := snapshots[len(snapshots)-1]
		// snapPrevDB := filepath.Join(s.str.Dir(), snapPrev.ID+".db")
		// snapNewDB := filepath.Join(s.str.Dir(), snapNew.ID+".db")
		// snapNewWAL := filepath.Join(s.str.Dir(), snapNew.ID+".db-wal")

		// if db.IsValidSQLiteWALFile(snapNewWAL) || dataSz == 0 {
		// 	// One of two things have happened. Either the snapshot data is empty, in which
		// 	// case we can just make the existing BadgerDB file the new snapshot, or the snapshot
		// 	// data is a valid WAL file, in which case we need to replay it into the existing
		// 	// BadgerDB file.
		// 	if err := os.Rename(snapPrevDB, snapNewDB); err != nil {
		// 		return err
		// 	}
		// 	// Ensure any WAL files are processed and removed.
		// 	if err := db.CheckpointRemove(snapNewDB); err != nil {
		// 		return fmt.Errorf("failed to checkpoint WAL file: %s", err.Error())
		// 	}
		// }
	}
	return syncDirMaybe(s.str.Dir())
}

func (s *Sink) writeMeta(dir string) error {
	return writeMeta(dir, s.meta)
}

func fileSize(path string) (int64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return stat.Size(), nil
}
