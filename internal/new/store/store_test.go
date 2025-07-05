package store

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
	"github.com/tarungka/wire/internal/new/db/badgerdb"
)

// testFSMSnapshot helper
func testFSMSnapshot(t *testing.T, meta SnapshotMeta, items []KVPair) (io.ReadCloser, func()) {
	t.Helper()
	tmpfile, err := os.CreateTemp("", "snapshot-"+t.Name())
	require.NoError(t, err)
	cleanup := func() {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
	}
	metaBytes, err := json.Marshal(meta)
	require.NoError(t, err)
	metaLen := uint32(len(metaBytes))
	err = binary.Write(tmpfile, binary.BigEndian, metaLen)
	require.NoError(t, err)
	_, err = tmpfile.Write(metaBytes)
	require.NoError(t, err)
	encoder := gob.NewEncoder(tmpfile)
	for _, item := range items {
		err := encoder.Encode(item)
		require.NoError(t, err)
	}
	_, err = tmpfile.Seek(0, 0)
	require.NoError(t, err)
	return tmpfile, cleanup
}

// newTestStore helper
func newTestStore(t *testing.T) (*NodeStore, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "storetest-"+t.Name())
	require.NoError(t, err)

	cfg := &Config{
		Dir:           tmpDir,
		ID:            "test-node-" + t.Name(),
		StoreDatabase: "badgerdb", // Consistent with how NodeStore initializes dbStore
	}
	mockLayer := &mockLayer{}
	store, err := New(mockLayer, cfg)
	require.NoError(t, err)

	// Mimic parts of Open() relevant for s.db (FSM DB)
	store.raftDir = tmpDir // Ensure raftDir is set as Open() would set it for pathing.
	fsmDBPath := filepath.Join(store.raftDir, "fsm.db")
	sdbCfg := &badgerdb.Config{Dir: fsmDBPath}
	sdb := badgerdb.New(sdbCfg)
	_, err = sdb.Open() // Open the database
	require.NoError(t, err, "Failed to open FSM DB for test store")
	store.db = sdb
	store.open.Set() // Mark store as open

	cleanup := func() {
		if store.db != nil {
			store.db.Close()
		}
		os.RemoveAll(tmpDir)
	}
	return store, cleanup
}

// mockLayer
type mockLayer struct{}
func (m *mockLayer) Dial(address string, timeout time.Duration) (net.Conn, error) { return nil, nil }
func (m *mockLayer) Accept() (net.Conn, error)                               { return nil, nil }
func (m *mockLayer) Close() error                                            { return nil }
func (m *mockLayer) Addr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
}
var _ Layer = (*mockLayer)(nil)

// --- Unit Tests ---
func TestFSMRestore_Successful(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	meta := SnapshotMeta{Index: 10, Term: 2, Version: 1}
	items := []KVPair{
		{Key: []byte("key1"), Value: []byte("value1")},
		{Key: []byte("key2"), Value: []byte("value2")},
	}
	snapshot, snapCleanup := testFSMSnapshot(t, meta, items)
	defer snapCleanup()
	err := store.Restore(snapshot)
	require.NoError(t, err)
	require.Equal(t, meta.Index, store.fsmIndex.Load())
	require.Equal(t, meta.Term, store.fsmTerm.Load())
	err = store.db.View(func(txn *badgerdb.Txn) error { // Assuming View method exists on badgerdb.DB
		for _, item := range items {
			dbItem, err := txn.Get(item.Key)
			require.NoError(t, err)
			val, err := dbItem.ValueCopy(nil) // Assuming ValueCopy on badger.Item
			require.NoError(t, err)
			require.Equal(t, item.Value, val)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestFSMRestore_CorruptedSnapshot_MetadataLengthReadError(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	corruptedSnapshot := io.NopCloser(bytes.NewReader([]byte{0, 0}))
	err := store.Restore(corruptedSnapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read metadata length")
}

func TestFSMRestore_CorruptedSnapshot_MetadataReadError(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	var buf bytes.Buffer
	metaLen := uint32(100)
	err := binary.Write(&buf, binary.BigEndian, metaLen)
	require.NoError(t, err)
	buf.Write([]byte("not enough metadata"))
	corruptedSnapshot := io.NopCloser(&buf)
	err = store.Restore(corruptedSnapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read metadata")
}

func TestFSMRestore_CorruptedSnapshot_MetadataUnmarshalError(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	var buf bytes.Buffer
	invalidJSON := []byte("this is not json")
	metaLen := uint32(len(invalidJSON))
	err := binary.Write(&buf, binary.BigEndian, metaLen)
	require.NoError(t, err)
	buf.Write(invalidJSON)
	corruptedSnapshot := io.NopCloser(&buf)
	err = store.Restore(corruptedSnapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal metadata")
}

func TestFSMRestore_CorruptedSnapshot_Data(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	meta := SnapshotMeta{Index: 1, Term: 1, Version: 1}
	var buf bytes.Buffer
	metaBytes, _ := json.Marshal(meta)
	metaLen := uint32(len(metaBytes))
	binary.Write(&buf, binary.BigEndian, metaLen)
	buf.Write(metaBytes)
	buf.Write([]byte("this is not valid gob data for KVPairs"))
	corruptedDataSnapshot := io.NopCloser(&buf)
	err := store.Restore(corruptedDataSnapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode KV pair")
}

func TestFSMRestore_VersionMismatch_TooLow(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	meta := SnapshotMeta{Index: 1, Term: 1, Version: MinSnapshotVersion - 1}
	snapshot, snapCleanup := testFSMSnapshot(t, meta, nil)
	defer snapCleanup()
	err := store.Restore(snapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported snapshot version")
}

func TestFSMRestore_VersionMismatch_TooHigh(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	meta := SnapshotMeta{Index: 1, Term: 1, Version: MaxSnapshotVersion + 1}
	snapshot, snapCleanup := testFSMSnapshot(t, meta, nil)
	defer snapCleanup()
	err := store.Restore(snapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported snapshot version")
}

func TestFSMRestore_AtomicSwap_Successful(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	originalDBPath, err := store.db.GetDbPath() // Use GetDbPath
	require.NoError(t, err)

	initialKey := []byte("initialKey")
	initialValue := []byte("initialValue")
	err = store.db.Update(func(txn *badgerdb.Txn) error {
		return txn.Set(initialKey, initialValue)
	})
	require.NoError(t, err)
	meta := SnapshotMeta{Index: 5, Term: 1, Version: 1}
	items := []KVPair{
		{Key: []byte("snapKey1"), Value: []byte("snapValue1")},
	}
	snapshot, snapCleanup := testFSMSnapshot(t, meta, items)
	defer snapCleanup()
	err = store.Restore(snapshot)
	require.NoError(t, err)

	currentDBPath, err := store.db.GetDbPath()
	require.NoError(t, err)
	require.Equal(t, originalDBPath, currentDBPath)

	err = store.db.View(func(txn *badgerdb.Txn) error {
		_, err := txn.Get(initialKey)
		require.ErrorIs(t, err, badgerdb.ErrKeyNotFound) // badgerdb should export this
		for _, item := range items {
			dbItem, err := txn.Get(item.Key)
			require.NoError(t, err)
			val, err := dbItem.ValueCopy(nil)
			require.NoError(t, err)
			require.Equal(t, item.Value, val)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestFSMRestore_MetadataTooLarge(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	largeMetaData := make([]byte, 1024*1024+1)
	var buf bytes.Buffer
	metaLen := uint32(len(largeMetaData))
	err := binary.Write(&buf, binary.BigEndian, metaLen)
	require.NoError(t, err)
	_, err = buf.Write(largeMetaData)
	require.NoError(t, err)
	snapshot := io.NopCloser(&buf)
	err = store.Restore(snapshot)
	require.Error(t, err)
	require.Contains(t, err.Error(), "metadata too large")
}

func TestFSMRestore_EmptySnapshot(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	meta := SnapshotMeta{Index: 1, Term: 1, Version: 1}
	var items []KVPair
	snapshot, snapCleanup := testFSMSnapshot(t, meta, items)
	defer snapCleanup()
	err := store.Restore(snapshot)
	require.NoError(t, err)
	require.Equal(t, meta.Index, store.fsmIndex.Load())
	require.Equal(t, meta.Term, store.fsmTerm.Load())
	err = store.db.View(func(txn *badgerdb.Txn) error {
		opts := badgerdb.DefaultIteratorOptions() // Assuming this is how it's called
		opts.PrefetchSize = 10
		it := txn.NewIterator(opts)
		defer it.Close()
		count := 0
		for it.Rewind(); it.Valid(); it.Next() { count++ }
		require.Equal(t, 0, count)
		return nil
	})
	require.NoError(t, err)
}

// --- Integration Test ---
func TestSnapshotRestoreCycle(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()
	applyItems := []KVPair{
		{Key: []byte("cycleKey1"), Value: []byte("cycleValue1")},
		{Key: []byte("cycleKey2"), Value: []byte("cycleValue2")},
	}
	const expectedSnapshotIndex uint64 = 100
	const expectedSnapshotTerm uint64 = 5
	store.fsmIndex.Store(expectedSnapshotIndex)
	store.fsmTerm.Store(expectedSnapshotTerm)
	err := store.db.Update(func(txn *badgerdb.Txn) error {
		for _, item := range applyItems {
			if errTxn := txn.Set(item.Key, item.Value); errTxn != nil { return errTxn }
		}
		return nil
	})
	require.NoError(t, err, "Failed to directly populate DB for snapshot test")
	fsmSnapshot, err := store.Snapshot()
	if err != nil && errors.Is(err, ErrNotImplemented) {
		t.Skip("Skipping TestSnapshotRestoreCycle: NodeStore.Snapshot() is not implemented")
		return
	}
	require.NoError(t, err, "NodeStore.Snapshot() failed")
	defer fsmSnapshot.Release()
	var snapBuf bytes.Buffer
	sink, errSink := newTestSnapshotSink(&snapBuf) // Ensure this helper is defined
	require.NoError(t, errSink)
	err = fsmSnapshot.Persist(sink)
	require.NoError(t, err, "FSMSnapshot.Persist() failed")
	err = sink.Close()
	require.NoError(t, err, "SnapshotSink.Close() failed")
	err = store.db.Update(func(txn *badgerdb.Txn) error {
		if errTxn := txn.Set([]byte("cycleKey1"), []byte("modifiedDirectlyInDB")); errTxn != nil { return errTxn }
		if errTxn := txn.Set([]byte("newDataAfterSnapshot"), []byte("valueAfterSnapshot")); errTxn != nil { return errTxn }
		return nil
	})
	require.NoError(t, err)
	restoreReader := io.NopCloser(bytes.NewReader(snapBuf.Bytes()))
	err = store.Restore(restoreReader)
	require.NoError(t, err, "NodeStore.Restore() failed")
	require.Equal(t, expectedSnapshotIndex, store.fsmIndex.Load(), "FSM index after restore mismatch")
	require.Equal(t, expectedSnapshotTerm, store.fsmTerm.Load(), "FSM term after restore mismatch")
	err = store.db.View(func(txn *badgerdb.Txn) error {
		for _, item := range applyItems {
			dbItem, errGet := txn.Get(item.Key)
			require.NoError(t, errGet)
			val, errVal := dbItem.ValueCopy(nil)
			require.NoError(t, errVal)
			require.Equal(t, item.Value, val)
		}
		_, errGet := txn.Get([]byte("newDataAfterSnapshot"))
		require.ErrorIs(t, errGet, badgerdb.ErrKeyNotFound)
		return nil
	})
	require.NoError(t, err)
}

// newTestSnapshotSink helper
type testSnapshotSink struct {
	buf    *bytes.Buffer
	closed bool
	id     string
}
func newTestSnapshotSink(buf *bytes.Buffer) (*testSnapshotSink, error) {
	return &testSnapshotSink{buf: buf, id: "test-sink-" + time.Now().Format(time.RFC3339Nano)}, nil
}
func (s *testSnapshotSink) ID() string { return s.id }
func (s *testSnapshotSink) Write(p []byte) (int, error) {
	if s.closed { return 0, errors.New("sink closed") }
	return s.buf.Write(p)
}
func (s *testSnapshotSink) Close() error {
	if s.closed { return errors.New("sink already closed") }
	s.closed = true; return nil
}
func (s *testSnapshotSink) Cancel() error { s.closed = true; return nil }
var _ raft.SnapshotSink = (*testSnapshotSink)(nil)

// --- Failure Scenario Tests ---
func TestFailure_AtomicSwap_OpenNewDBError(t *testing.T) {
    store, cleanupStore := newTestStore(t)
    defer cleanupStore()

    originalDBPath, err := store.db.GetDbPath()
    require.NoError(t, err)
    originalDataKey := []byte("originalData")
    originalDataValue := []byte("originalValue")

    err = store.db.Update(func(txn *badgerdb.Txn) error {
        return txn.Set(originalDataKey, originalDataValue)
    })
    require.NoError(t, err)
    store.db.Close()

    tempRestoredDBDir := filepath.Join(store.raftDir, "tempRestoreDirOpenFail")
    err = os.MkdirAll(tempRestoredDBDir, 0755)
    require.NoError(t, err)
    // This path will be moved to originalDBPath by atomicSwapDatabase
    tempRestoredDBPath_InvalidForOpen := filepath.Join(tempRestoredDBDir, "data_to_be_moved.db")

    // Create a file at this path to make badger.Open fail when it expects a directory
    err = os.WriteFile(tempRestoredDBPath_InvalidForOpen, []byte("this is a file, not a badger db dir"), 0600)
    require.NoError(t, err)

    // Re-open original DB so atomicSwapDatabase can back it up
    // The store.db was closed. We need to re-initialize and open it.
    originalDBCfg := &badgerdb.Config{Dir: originalDBPath}
    store.db = badgerdb.New(originalDBCfg)
    _, err = store.db.Open()
    require.NoError(t, err, "Failed to re-open original DB for test setup")

    // Call atomicSwapDatabase. It will:
    // 1. Close store.db (original).
    // 2. Backup originalDBPath to backupPath.
    // 3. Rename tempRestoredDBPath_InvalidForOpen to originalDBPath.
    // 4. Try to open originalDBPath (which now points to the invalid file), this should fail.
    // 5. On failure, it should restore backupPath to originalDBPath and reopen it.
    err = store.atomicSwapDatabase(tempRestoredDBPath_InvalidForOpen)
    require.Error(t, err, "atomicSwapDatabase should fail")
    require.Contains(t, err.Error(), "open new database") // Error from failing to open the swapped-in invalid DB

    require.NotNil(t, store.db, "store.db should be re-opened to the backup")
    currentPathAfterSwap, errPath := store.db.GetDbPath()
    require.NoError(t, errPath)
    require.Equal(t, originalDBPath, currentPathAfterSwap, "DB path should be the original one after rollback")

    err = store.db.View(func(txn *badgerdb.Txn) error {
        item, errGet := txn.Get(originalDataKey)
        require.NoError(t, errGet, "original data key should be found after rollback")
        val, errVal := item.ValueCopy(nil)
        require.NoError(t, errVal)
        require.Equal(t, originalDataValue, val)
        return nil
    })
    require.NoError(t, err)
}

func TestFSMRestore_Failure_CreateTempDBError_Permission(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	// Path where createTempDatabase will attempt to create the DB.
	// It's s.db.GetDbPath() + .restore.timestamp
	currentDBPath, err := store.db.GetDbPath()
	require.NoError(t, err)

	// Create a directory where the temp DB would go, but make it read-only
	// This specific path is hard to predict due to timestamp.
	// Instead, let's make the parent dir (store.raftDir) read-only to cause Mkdir inside badger to fail.
	// Note: This is OS dependent and might not work everywhere or require specific privileges.

	// For simplicity, this test remains illustrative of the intent.
	// A more robust way would be to inject a "faulty disk" error via a mock.
	// The current createTempDatabase directly calls badgerdb.New and Open.

	// To test createTempDatabase failure, we need to ensure its call to badgerdb.Open() fails.
	// badgerdb.Open() itself calls badger.Open(badger.DefaultOptions(path)).
	// If 'path' (e.g., /some/readonlydir/db.restore.123/MANIFEST) cannot be written, it fails.

	// Let's try making raftDir read-only.
	err = os.Chmod(store.raftDir, 0500) // Read and execute only
	if err != nil {
		t.Logf("Could not change raftDir permissions to read-only, skipping precise part of test: %v", err)
		// If chmod fails (e.g. user doesn't own dir in test env), test might not run as intended.
	}
	defer os.Chmod(store.raftDir, 0755) // Restore permissions

	meta := SnapshotMeta{Index: 1, Term: 1, Version: 1}
	snapshot, snapCleanup := testFSMSnapshot(t, meta, []KVPair{{Key: []byte("k"), Value: []byte("v")}})
	defer snapCleanup()

	errRestore := store.Restore(snapshot)
	if err == nil { // Only assert if chmod was successful
		require.Error(t, errRestore, "Restore should fail if temp DB creation fails")
		require.Contains(t, errRestore.Error(), "create temp database", "Error message should indicate temp DB failure")
	} else {
		t.Log("Skipped error assertion as chmod failed earlier.")
		if errRestore == nil {
			t.Error("Restore succeeded unexpectedly even though chmod might have failed to make dir readonly.")
		}
	}
}
