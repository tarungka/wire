package badgerdb

import (
	"bytes"
	"os"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()

	// Create a temporary directory for the test database
	tempDir, err := os.MkdirTemp("", "badger-test-*")
	require.NoError(t, err)

	db := New(&Config{
		Dir: tempDir,
	})

	// Open the database
	badgerDB, err := db.Open()
	require.NoError(t, err)
	db.db = badgerDB

	// Return cleanup function
	cleanup := func() {
		badgerDB.Close()
		os.RemoveAll(tempDir)
	}

	return db, cleanup
}

// TODO: Test the Close() in the db command not from cleanup
func TestDB_OpenClose(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	assert.True(t, db.open.Is(), "Database should be open")
}

func TestDB_SetGet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tests := []struct {
		name  string
		key   []byte
		value []byte
	}{
		{
			name:  "simple key-value",
			key:   []byte("key1"),
			value: []byte("value1"),
		},
		{
			name:  "empty value",
			key:   []byte("key2"),
			value: []byte{},
		},
		{
			name:  "binary data",
			key:   []byte{0x00, 0x01, 0x02},
			value: []byte{0x03, 0x04, 0x05},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.Set(tt.key, tt.value)
			require.NoError(t, err)

			got, err := db.Get(tt.key)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(tt.value, got))
		})
	}
}

func TestDB_SetGetUint64(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tests := []struct {
		name  string
		key   []byte
		value uint64
	}{
		{
			name:  "zero value",
			key:   []byte("key1"),
			value: 0,
		},
		{
			name:  "max uint64",
			key:   []byte("key2"),
			value: ^uint64(0),
		},
		{
			name:  "random value",
			key:   []byte("key3"),
			value: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.SetUint64(tt.key, tt.value)
			require.NoError(t, err)

			got, err := db.GetUint64(tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.value, got)
		})
	}
}

// FIX: This test case is breaking
func TestDB_FirstLastIndex(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Test empty database
	first, err := db.FirstIndex()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), first)

	last, err := db.LastIndex()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), last)

	// Store some logs
	logs := []*raft.Log{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("first")},
		{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("second")},
		{Index: 3, Term: 2, Type: raft.LogCommand, Data: []byte("third")},
	}

	err = db.StoreLogs(logs)
	require.NoError(t, err)

	first, err = db.FirstIndex()
	require.NoError(t, err)
	assert.Equal(t, uint64(1), first)

	last, err = db.LastIndex()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), last)
}

// FIX: This test case is breaking
func TestDB_GetLog(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Store a log
	expected := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  []byte("test log"),
	}
	err := db.StoreLog(expected)
	require.NoError(t, err)

	// Retrieve the log
	var got raft.Log
	err = db.GetLog(1, &got)
	require.NoError(t, err)

	assert.Equal(t, expected.Index, got.Index)
	assert.Equal(t, expected.Term, got.Term)
	assert.Equal(t, expected.Type, got.Type)
	assert.True(t, bytes.Equal(expected.Data, got.Data))
}

// FIX: This test case is breaking
func TestDB_DeleteRange(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Store some logs
	logs := []*raft.Log{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("one")},
		{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("two")},
		{Index: 3, Term: 2, Type: raft.LogCommand, Data: []byte("three")},
		{Index: 4, Term: 2, Type: raft.LogCommand, Data: []byte("four")},
		{Index: 5, Term: 2, Type: raft.LogCommand, Data: []byte("five")},
	}

	err := db.StoreLogs(logs)
	require.NoError(t, err)

	// Delete range from 2 to 4
	err = db.DeleteRange(2, 4)
	require.NoError(t, err)

	// Verify logs 2-4 are deleted
	for i := uint64(2); i <= 4; i++ {
		var log raft.Log
		err = db.GetLog(i, &log)
		assert.Error(t, err, "Log %d should be deleted", i)
	}

	// Verify logs 1 and 5 still exist
	var log raft.Log
	err = db.GetLog(1, &log)
	require.NoError(t, err)
	assert.Equal(t, []byte("one"), log.Data)

	err = db.GetLog(5, &log)
	require.NoError(t, err)
	assert.Equal(t, []byte("five"), log.Data)
}

func TestDB_OpenInMemory(t *testing.T) {
	db := New(&Config{})

	// Open in-memory database
	badgerDB, err := db.OpenInMemory()
	require.NoError(t, err)
	defer badgerDB.Close()

	db.db = badgerDB
	db.open.Set()

	// Test basic operations
	err = db.Set([]byte("key"), []byte("value"))
	require.NoError(t, err)

	val, err := db.Get([]byte("key"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), val)
}
