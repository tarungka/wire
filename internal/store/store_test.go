package store

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarungka/wire/internal/command"
	commandProto "github.com/tarungka/wire/internal/command/proto"
	"github.com/tarungka/wire/internal/db"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/rsync"
)

// testSink implements raft.SnapshotSink for testing.
type testSink struct {
	*bytes.Buffer
	id     string
	closed bool
}

func newTestSink(id string) *testSink {
	return &testSink{
		Buffer: new(bytes.Buffer),
		id:     id,
	}
}

func (t *testSink) ID() string {
	return t.id
}

func (t *testSink) Close() error {
	if t.closed {
		return fmt.Errorf("sink already closed")
	}
	t.closed = true
	return nil
}

func (t *testSink) Cancel() error {
	t.closed = true
	return nil
}

// newTestStore creates a new Store for testing.
// It returns the store and a cleanup function.
func newTestStore(t *testing.T, useMemDB bool) (*Store, func()) {
	t.Helper()

	raftDir := t.TempDir()

	var testDB *badger.DB
	var dbPath string
	var err error

	if useMemDB {
		testDB, err = db.OpenInMemory()
		require.NoError(t, err, "failed to open in-memory db")
	} else {
		dbPath = t.TempDir()
		testDB, err = db.Open(dbPath)
		require.NoError(t, err, "failed to open db at %s", dbPath)
	}

	// Suppress verbose logging during tests unless explicitly needed.
	// log := logger.New(logger.Pretty, zerolog.Disabled) // Or zerolog.ErrorLevel for errors only
	log := logger.GetLogger("store_test")
	log = log.Level(zerolog.ErrorLevel) // Reduce noise during tests

	s := &Store{
		open:            rsync.NewAtomicBool(),
		raftDir:         raftDir,
		logger:          log,
		db:              testDB,
		dbDir:           dbPath, // Store the dbPath if not in-memory
		reqMarshaller:   command.NewRequestMarshaler(),
		fsmIdx:          &atomic.Uint64{},
		fsmTerm:         &atomic.Uint64{},
		fsmUpdateTime:   rsync.NewAtomicTime(),
		numNoops:        &atomic.Uint64{},
		numSnapshots:    &atomic.Uint64{},
		ApplyTimeout:    1 * time.Second, // Shorter timeout for tests
		readyChans:      rsync.NewReadyChannels(),
		notifyingNodes:  make(map[string]*Server),
		leaderObservers: make([]chan<- struct{}, 0),
	}
	s.open.Set() // Simulate store being open for FSM methods directly

	cleanup := func() {
		if s.db != nil && !s.db.IsClosed() {
			err := s.db.Close()
			assert.NoError(t, err, "error closing test db")
		}
		// Raft dir cleanup is handled by t.TempDir()
		// If db was file-based, its t.TempDir() also handles cleanup.
	}

	return s, cleanup
}

func TestStore_FSMApply_Set(t *testing.T) {
	s, cleanup := newTestStore(t, true)
	defer cleanup()

	key := "testkey"
	value := "testvalue"
	stmt := fmt.Sprintf("SET %s %s", key, value)

	cmd := &commandProto.Command{
		Type:       commandProto.Command_COMMAND_TYPE_EXECUTE,
		SubCommand: command.MustMarshal(&commandProto.ExecuteRequest{Statements: []*commandProto.Statement{{Sql: stmt}}}),
	}
	logData, err := command.Marshal(cmd)
	require.NoError(t, err)

	raftLog := &raft.Log{
		Index: 1,
		Term:  1,
		Data:  logData,
	}

	resp := s.fsmApply(raftLog)
	require.NotNil(t, resp)
	fsmResp, ok := resp.(*fsmExecuteQueryResponse)
	require.True(t, ok, "response is not of type *fsmExecuteQueryResponse")

	assert.NoError(t, fsmResp.error)
	require.Len(t, fsmResp.results, 1)
	assert.Equal(t, int64(1), fsmResp.results[0].GetRowsAffected())

	assert.Equal(t, uint64(1), s.fsmIdx.Load())
	assert.Equal(t, uint64(1), s.fsmTerm.Load())
	assert.NotZero(t, s.fsmUpdateTime.Load())

	// Verify data in DB
	err = s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		require.NoError(t, err)
		val, err := item.ValueCopy(nil)
		require.NoError(t, err)
		assert.Equal(t, value, string(val))
		return nil
	})
	require.NoError(t, err)
}

func TestStore_FSMApply_Delete(t *testing.T) {
	s, cleanup := newTestStore(t, true)
	defer cleanup()

	key := "testkeydel"
	value := "testvaluedel"

	// 1. SET the key first
	setStmt := fmt.Sprintf("SET %s %s", key, value)
	setCmd := &commandProto.Command{
		Type:       commandProto.Command_COMMAND_TYPE_EXECUTE,
		SubCommand: command.MustMarshal(&commandProto.ExecuteRequest{Statements: []*commandProto.Statement{{Sql: setStmt}}}),
	}
	setLogData, err := command.Marshal(setCmd)
	require.NoError(t, err)
	setRaftLog := &raft.Log{Index: 1, Term: 1, Data: setLogData}
	s.fsmApply(setRaftLog) // Apply SET

	// 2. DELETE the key
	delStmt := fmt.Sprintf("DELETE %s", key)
	delCmd := &commandProto.Command{
		Type:       commandProto.Command_COMMAND_TYPE_EXECUTE,
		SubCommand: command.MustMarshal(&commandProto.ExecuteRequest{Statements: []*commandProto.Statement{{Sql: delStmt}}}),
	}
	delLogData, err := command.Marshal(delCmd)
	require.NoError(t, err)

	delRaftLog := &raft.Log{
		Index: 2, // Next index
		Term:  1,
		Data:  delLogData,
	}

	resp := s.fsmApply(delRaftLog)
	require.NotNil(t, resp)
	fsmResp, ok := resp.(*fsmExecuteQueryResponse)
	require.True(t, ok)

	assert.NoError(t, fsmResp.error)
	require.Len(t, fsmResp.results, 1)
	assert.Equal(t, int64(1), fsmResp.results[0].GetRowsAffected()) // Badger Delete is idempotent

	assert.Equal(t, uint64(2), s.fsmIdx.Load())
	assert.Equal(t, uint64(1), s.fsmTerm.Load())

	// Verify data is deleted from DB
	err = s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(key))
		assert.ErrorIs(t, err, badger.ErrKeyNotFound)
		return nil
	})
	require.NoError(t, err) // View itself should not error
}

func TestStore_FSMApply_InvalidStatement(t *testing.T) {
	s, cleanup := newTestStore(t, true)
	defer cleanup()

	stmt := "INVALIDCMD foo bar"
	cmd := &commandProto.Command{
		Type:       commandProto.Command_COMMAND_TYPE_EXECUTE,
		SubCommand: command.MustMarshal(&commandProto.ExecuteRequest{Statements: []*commandProto.Statement{{Sql: stmt}}}),
	}
	logData, err := command.Marshal(cmd)
	require.NoError(t, err)

	raftLog := &raft.Log{Index: 1, Term: 1, Data: logData}

	resp := s.fsmApply(raftLog)
	require.NotNil(t, resp)
	fsmResp, ok := resp.(*fsmExecuteQueryResponse)
	require.True(t, ok)

	assert.Error(t, fsmResp.error)
	assert.Contains(t, fsmResp.error.Error(), "unrecognized statement type in EXECUTE: INVALIDCMD")

	assert.Equal(t, uint64(1), s.fsmIdx.Load()) // Index/term still updated
	assert.Equal(t, uint64(1), s.fsmTerm.Load())
}

func TestStore_FSMApply_Noop(t *testing.T) {
	s, cleanup := newTestStore(t, true)
	defer cleanup()

	cmd := &commandProto.Command{Type: commandProto.Command_COMMAND_TYPE_NOOP}
	logData, err := command.Marshal(cmd)
	require.NoError(t, err)

	raftLog := &raft.Log{Index: 5, Term: 2, Data: logData}

	resp := s.fsmApply(raftLog)
	require.NotNil(t, resp)
	fsmResp, ok := resp.(*fsmExecuteQueryResponse)
	require.True(t, ok)

	assert.NoError(t, fsmResp.error)
	assert.Empty(t, fsmResp.results) // NOOP has no results

	assert.Equal(t, uint64(5), s.fsmIdx.Load())
	assert.Equal(t, uint64(2), s.fsmTerm.Load())
}

func TestStore_FSMApply_Unimplemented(t *testing.T) {
	s, cleanup := newTestStore(t, true)
	defer cleanup()

	// Using QUERY type which is not meant to modify state via fsmApply directly
	cmd := &commandProto.Command{
		Type: commandProto.Command_COMMAND_TYPE_QUERY,
		SubCommand: command.MustMarshal(&commandProto.QueryRequest{
			Statements: []*commandProto.Statement{{Sql: "GET foo"}},
		}),
	}
	logData, err := command.Marshal(cmd)
	require.NoError(t, err)

	raftLog := &raft.Log{Index: 1, Term: 1, Data: logData}

	resp := s.fsmApply(raftLog)
	require.NotNil(t, resp)
	fsmResp, ok := resp.(*fsmExecuteQueryResponse)
	require.True(t, ok)

	assert.ErrorIs(t, fsmResp.error, ErrNotImplemented) // Changed to ErrNotImplemented based on fsmApply logic
	assert.Contains(t, fsmResp.error.Error(), "QUERY command type should not be applied to FSM state")


	assert.Equal(t, uint64(1), s.fsmIdx.Load()) // Index/term updated
	assert.Equal(t, uint64(1), s.fsmTerm.Load())
}

func TestStore_FSMInteraction_SnapshotAndPersist(t *testing.T) {
	s, cleanup := newTestStore(t, false) // Use file-based DB for snapshot/restore
	defer cleanup()

	// 1. Set some data
	s.fsmIdx.Store(10) // Simulate prior logs
	s.fsmTerm.Store(2)
	key, value := "snapkey", "snapvalue"
	setStmt := fmt.Sprintf("SET %s %s", key, value)
	setCmd := &commandProto.Command{
		Type:       commandProto.Command_COMMAND_TYPE_EXECUTE,
		SubCommand: command.MustMarshal(&commandProto.ExecuteRequest{Statements: []*commandProto.Statement{{Sql: setStmt}}}),
	}
	setLogData, err := command.Marshal(setCmd)
	require.NoError(t, err)
	setRaftLog := &raft.Log{Index: 11, Term: 2, Data: setLogData} // Apply this log
	applyResp := s.fsmApply(setRaftLog)
	fsmApplyResp, _ := applyResp.(*fsmExecuteQueryResponse)
	require.NoError(t, fsmApplyResp.error)


	// 2. Call fsmSnapshot
	fsmSnap, err := s.fsmSnapshot()
	require.NoError(t, err)
	require.NotNil(t, fsmSnap)

	// 3. Persist to testSink
	sink := newTestSink("test-snapshot-1")
	err = fsmSnap.Persist(sink)
	require.NoError(t, err)

	// 4. Verify metadata
	snapData := sink.Bytes()
	require.GreaterOrEqual(t, len(snapData), 16, "snapshot data too short for metadata")

	restoredIndex := binary.BigEndian.Uint64(snapData[0:8])
	restoredTerm := binary.BigEndian.Uint64(snapData[8:16])

	assert.Equal(t, s.fsmIdx.Load(), restoredIndex, "snapshot index mismatch") // Should be 11
	assert.Equal(t, s.fsmTerm.Load(), restoredTerm, "snapshot term mismatch")   // Should be 2


	// 5. (Harder part) Verify data by loading into a new DB
	snapshotDBContent := bytes.NewReader(snapData[16:]) // Skip metadata

	restoreDBPath := t.TempDir()
	restoredDB, err := db.Open(restoreDBPath)
	require.NoError(t, err, "failed to open DB for restore validation")
	defer restoredDB.Close()

	// Load the snapshot content (actual DB backup part)
	err = restoredDB.Load(snapshotDBContent, 256) // Using 256 as MaxPendingWrites
	require.NoError(t, err, "failed to load snapshot data into new DB")

	// Check if the original data is present
	err = restoredDB.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		require.NoError(t, err)
		valBytes, err := item.ValueCopy(nil)
		require.NoError(t, err)
		assert.Equal(t, value, string(valBytes))
		return nil
	})
	require.NoError(t, err)

	// Release the snapshot (closes the pipe reader in our snapshot implementation)
	fsmSnap.Release()
}

// Helper to create a minimal BadgerDB backup stream for testing restore
func createMinimalBadgerBackup(t *testing.T, key, value string) []byte {
	t.Helper()
	tempDBPath := t.TempDir()
	tempDB, err := db.Open(tempDBPath)
	require.NoError(t, err)
	defer tempDB.Close() // Ensure tempDB is closed to release resources

	if key != "" && value != "" {
		err = tempDB.Update(func(txn *badger.Txn) error {
			return txn.Set([]byte(key), []byte(value))
		})
		require.NoError(t, err)
	}

	var buf bytes.Buffer
	_, err = tempDB.Backup(&buf, 0) // since=0 for full backup
	require.NoError(t, err)

	// It's important to close the DB before its directory is removed by t.TempDir()
	// If not closed properly, Badger might not flush everything or might hold locks.
	err = tempDB.Close()
	require.NoError(t, err)

	// Double check removal, sometimes TempDir might have issues if files are locked
	// This is more of a sanity check for test stability than a requirement for the backup itself.
	// On Windows, file locks can be particularly sticky.
	// For this test, we assume t.TempDir() handles cleanup after tempDB.Close().

	return buf.Bytes()
}

func TestStore_FSMInteraction_Restore(t *testing.T) {
	s, cleanup := newTestStore(t, false) // Use file-based DB
	defer cleanup()

	// --- Prepare initial state for the store (will be wiped out by restore) ---
	initialKey, initialValue := "initialkey", "initialvalue"
	err := s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(initialKey), []byte(initialValue))
	})
	require.NoError(t, err)
	s.fsmIdx.Store(1) // Initial FSM state
	s.fsmTerm.Store(1)

	// --- Prepare snapshot data ---
	restoreKey, restoreValue := "restorekey", "restorevalue"
	snapIndex, snapTerm := uint64(10), uint64(2)

	dbBackupData := createMinimalBadgerBackup(t, restoreKey, restoreValue)

	var snapshotDataBuf bytes.Buffer
	// Write metadata (index, term)
	metaBytes := make([]byte, 16)
	binary.BigEndian.PutUint64(metaBytes[0:8], snapIndex)
	binary.BigEndian.PutUint64(metaBytes[8:16], snapTerm)
	_, err = snapshotDataBuf.Write(metaBytes)
	require.NoError(t, err)
	// Write DB backup
	_, err = snapshotDataBuf.Write(dbBackupData)
	require.NoError(t, err)

	// --- Call fsmRestore ---
	// s.dbDir should be set by newTestStore if not using in-memory.
	// If s.dbDir was not set, fsmRestore would use filepath.Join(s.raftDir, "badgerdb")
	// Ensure the db path logic in fsmRestore aligns with how newTestStore sets up s.dbDir.
	// For file-based, newTestStore sets s.dbDir.

	err = s.fsmRestore(io.NopCloser(bytes.NewReader(snapshotDataBuf.Bytes())))
	require.NoError(t, err)

	// --- Verify restored state ---
	assert.Equal(t, snapIndex, s.fsmIdx.Load(), "restored FSM index mismatch")
	assert.Equal(t, snapTerm, s.fsmTerm.Load(), "restored FSM term mismatch")
	assert.NotZero(t, s.fsmUpdateTime.Load(), "fsmUpdateTime should be set after restore")

	// Verify new data is present
	err = s.db.View(func(txn *badger.Txn) error {
		item, errGet := txn.Get([]byte(restoreKey))
		require.NoError(t, errGet, "restored key not found")
		valBytes, errVal := item.ValueCopy(nil)
		require.NoError(t, errVal)
		assert.Equal(t, restoreValue, string(valBytes), "restored value mismatch")
		return nil
	})
	require.NoError(t, err)

	// Verify old data is gone
	err = s.db.View(func(txn *badger.Txn) error {
		_, errGet := txn.Get([]byte(initialKey))
		assert.ErrorIs(t, errGet, badger.ErrKeyNotFound, "initial key should be gone after restore")
		return nil
	})
	require.NoError(t, err)
}

// TODO: Add tests for Store.Query (basic GET) if not covered elsewhere
// TODO: Add tests for Store.Execute (basic SET/DELETE via Raft Apply path) if not covered

func TestStore_Query_SimpleGet(t *testing.T) {
	s, cleanup := newTestStore(t, true)
	defer cleanup()

	key, value := "querykey", "queryvalue"

	// Set data using fsmApply (simulating data committed via Raft)
	setStmt := fmt.Sprintf("SET %s %s", key, value)
	setCmd := &commandProto.Command{
		Type:       commandProto.Command_COMMAND_TYPE_EXECUTE,
		SubCommand: command.MustMarshal(&commandProto.ExecuteRequest{Statements: []*commandProto.Statement{{Sql: setStmt}}}),
	}
	setLogData, err := command.Marshal(setCmd)
	require.NoError(t, err)
	s.fsmApply(&raft.Log{Index: 1, Term: 1, Data: setLogData})

	// Query the data
	queryReq := &commandProto.QueryRequest{
		Statements: []*commandProto.Statement{{Sql: fmt.Sprintf("GET %s", key)}},
	}
	queryRows, err := s.Query(queryReq)
	require.NoError(t, err)
	require.Len(t, queryRows, 1, "expected one QueryRows result")

	result := queryRows[0]
	require.Len(t, result.Columns, 2, "expected two columns")
	assert.Equal(t, "key", result.Columns[0])
	assert.Equal(t, "value", result.Columns[1])

	require.Len(t, result.Values, 1, "expected one row in values")
	rowValue := result.Values[0]
	require.Len(t, rowValue.Values, 2, "expected two datums in row")

	assert.Equal(t, key, rowValue.Values[0].GetS(), "key mismatch in query result")
	assert.Equal(t, []byte(value), rowValue.Values[1].GetB(), "value mismatch in query result")
}

func TestStore_Query_KeyNotFound(t *testing.T) {
	s, cleanup := newTestStore(t, true)
	defer cleanup()

	queryReq := &commandProto.QueryRequest{
		Statements: []*commandProto.Statement{{Sql: "GET nonexistingkey"}},
	}
	queryRows, err := s.Query(queryReq)
	require.NoError(t, err, "Query should not error for key not found")
	require.Len(t, queryRows, 1)
	assert.Empty(t, queryRows[0].Values, "expected no values for non-existing key")
}
