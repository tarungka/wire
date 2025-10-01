package rocksdb

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestDB(t *testing.T) (*DB, func()) {
	tempDir, err := ioutil.TempDir("", "rocksdb_test")
	require.NoError(t, err)

	config := &Config{Dir: tempDir}
	db := New(config)
	err = db.Open()
	require.NoError(t, err)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tempDir)
	}

	return db, cleanup
}

func TestDB_OpenClose(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "rocksdb_open_close")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	config := &Config{Dir: tempDir}
	db := New(config)

	// Test Open
	err = db.Open()
	assert.NoError(t, err)
	assert.True(t, db.open.Is())

	// Test Close
	err = db.Close()
	assert.NoError(t, err)
	assert.False(t, db.open.Is())
}

func TestDB_SetGet(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	key := []byte("hello")
	value := []byte("world")

	// Test Set
	err := db.Set(key, value)
	assert.NoError(t, err)

	// Test Get
	retrieved, err := db.Get(key)
	assert.NoError(t, err)
	assert.Equal(t, value, retrieved)
}

func TestDB_SetGetUint64(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	key := []byte("my_uint")
	value := uint64(1234567890)

	// Test SetUint64
	err := db.SetUint64(key, value)
	assert.NoError(t, err)

	// Test GetUint64
	retrieved, err := db.GetUint64(key)
	assert.NoError(t, err)
	assert.Equal(t, value, retrieved)
}

func TestDB_Logs(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	log1 := &raft.Log{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("command1")}
	log2 := &raft.Log{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("command2")}
	log3 := &raft.Log{Index: 3, Term: 2, Type: raft.LogCommand, Data: []byte("command3")}

	// Test StoreLogs
	err := db.StoreLogs([]*raft.Log{log1, log2, log3})
	assert.NoError(t, err)

	// Test FirstIndex
	first, err := db.FirstIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(1), first)

	// Test LastIndex
	last, err := db.LastIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(3), last)

	// Test GetLog
	retrievedLog := &raft.Log{}
	err = db.GetLog(2, retrievedLog)
	assert.NoError(t, err)
	assert.Equal(t, log2, retrievedLog)

	// Test DeleteRange
	err = db.DeleteRange(1, 2)
	assert.NoError(t, err)

	// Verify deletion
	err = db.GetLog(1, &raft.Log{})
	assert.ErrorIs(t, err, raft.ErrLogNotFound)
	err = db.GetLog(2, &raft.Log{})
	assert.ErrorIs(t, err, raft.ErrLogNotFound)

	// Verify log 3 still exists
	err = db.GetLog(3, retrievedLog)
	assert.NoError(t, err)
	assert.Equal(t, log3, retrievedLog)

	// Check indices again
	first, err = db.FirstIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(3), first)

	last, err = db.LastIndex()
	assert.NoError(t, err)
	assert.Equal(t, uint64(3), last)
}