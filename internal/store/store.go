package store

/*                             NEED TO IMPLEMENT
Following are the functions that still need to be implemented or are partially
implemented and need to complete based on the other features being built

func (s *Store) Backup(br *commandProto.BackupRequest, dst io.Writer) (retErr error)
func (s *Store) load(lr *commandProto.LoadRequest) error
func (s *Store) SetRestorePath(path string) error
func (s *Store) fsmSnapshot() (fSnap raft.FSMSnapshot, retErr error)
func (s *Store) fsmRestore(rc io.ReadCloser) (retErr error)
func (s *Store) fsmApply(l *raft.Log) (e interface{})
	// TODO: Implementation truncated
}
func (s *Store) Close(wait bool) (retErr error)
func (s *Store) DBAppliedIndex() uint64
func (s *Store) Database(leader bool) ([]byte, error)
func (s *Store) DeregisterObserver(o *raft.Observer)
func (s *Store) Execute(ex *proto.ExecuteRequest) ([]*proto.ExecuteQueryResponse, error)
func (s *Store) LastOptimizeTime() (time.Time, error)
func (s *Store) LastVacuumTime() (time.Time, error)
func (s *Store) Noop(id string) (raft.ApplyFuture, error)
func (s *Store) Query(qr *proto.QueryRequest) ([]*proto.QueryRows, error)
func (s *Store) RORWCount(eqr *proto.ExecuteQueryRequest) (nRW int, nRO int)
func (s *Store) ReadFrom(r io.Reader) (int64, error)
func (s *Store) RegisterLeaderChange(c chan<- struct{})
	// TODO: Implementation truncated
}
func (s *Store) RegisterObserver(o *raft.Observer)
func (s *Store) Request(eqr *proto.ExecuteQueryRequest) ([]*proto.ExecuteQueryResponse, error)
func (s *Store) SetRequestCompression(batch int, size int)
func (s *Store) Stats() (map[string]interface{}, error)
	// TODO: Implementation truncated
}
func (s *Store) Vacuum() error
func (s *Store) WaitForAllApplied(timeout time.Duration) error
func (s *Store) WaitForAppliedFSM(timeout time.Duration) (uint64, error)
func (s *Store) WaitForAppliedIndex(idx uint64, timeout time.Duration) error
func (s *Store) WaitForFSMIndex(idx uint64, timeout time.Duration) (uint64, error)
func (s *Store) WaitForLeader(timeout time.Duration) (string, error)
func (s *Store) WaitForRemoval(id string, timeout time.Duration) error
func (s *Store) autoOptimizeNeeded(t time.Time) (bool, error)
func (s *Store) autoVacNeeded(t time.Time) (bool, error)
func (s *Store) clearKeyTime(key string) error
func (s *Store) dbModified() bool
func (s *Store) execute(ex *proto.ExecuteRequest) ([]*proto.ExecuteQueryResponse, error)
func (s *Store) getKeyTime(key string) (time.Time, error)
func (s *Store) initOptimizeTime() error
func (s *Store) initVacuumTime() error
func (s *Store) isStaleRead(freshness int64, strict bool) bool
func (s *Store) logBackup() bool
func (s *Store) logIncremental() bool
func (s *Store) runWALSnapshotting() (closeCh chan struct{}, doneCh chan struct{})
	// TODO: Implementation truncated
}
func (s *Store) setKeyTime(key string, t time.Time) error
*/

import (
	"errors"
	"expvar"
	// "fmt"
	"io"
	// "log"
	// "net"
	// "os"
	// "path/filepath"
	// "sort"
	// "strconv"
	// "strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/command"
	"github.com/tarungka/wire/internal/command/proto"
	commandProto "github.com/tarungka/wire/internal/command/proto"
	// "github.com/tarungka/wire/internal/db"
	// "github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/rsync"
	// "github.com/tarungka/wire/internal/snapshot"
	// "github.com/tarungka/wire/internal/utils"

	rlog "github.com/tarungka/wire/internal/log"
)

var (
	// ErrStoreNotOpen is returned when a Store is not open.
	ErrStoreNotOpen = errors.New("store not open")

	// ErrStoreOpen is returned when a Store is already open.
	ErrStoreOpen = errors.New("store already open")

	// ErrNotReady is returned when a Store is not ready to accept requests.
	ErrNotReady = errors.New("store not ready")

	// ErrNotLeader is returned when a node attempts to execute a leader-only
	// operation.
	ErrNotLeader = errors.New("not leader")

	// ErrNotSingleNode is returned when a node attempts to execute a single-node
	// only operation.
	ErrNotSingleNode = errors.New("not single-node")

	// ErrStaleRead is returned if the executing the query would violate the
	// requested freshness.
	ErrStaleRead = errors.New("stale read")

	// ErrOpenTimeout is returned when the Store does not apply its initial
	// logs within the specified time.
	ErrOpenTimeout = errors.New("timeout waiting for initial logs application")

	// ErrWaitForRemovalTimeout is returned when the Store does not confirm removal
	// of a node within the specified time.
	ErrWaitForRemovalTimeout = errors.New("timeout waiting for node removal confirmation")

	// ErrWaitForLeaderTimeout is returned when the Store cannot determine the leader
	// within the specified time.
	ErrWaitForLeaderTimeout = errors.New("timeout waiting for leader")

	// ErrInvalidBackupFormat is returned when the requested backup format
	// is not valid.
	ErrInvalidBackupFormat = errors.New("invalid backup format")

	// ErrInvalidVacuumFormat is returned when the requested backup format is not
	// compatible with vacuum.
	ErrInvalidVacuum = errors.New("invalid vacuum")

	// ErrLoadInProgress is returned when a load is already in progress and the
	// requested operation cannot be performed.
	ErrLoadInProgress = errors.New("load in progress")

	// ErrNotImplemented when there is no implementation of the function
	// will only exits until this application in under development
	ErrNotImplemented = errors.New("not implemented")

	// ErrDatabaseNotOpen when the database is closed
	ErrDatabaseNotOpen = errors.New("database is not open")
)

type PragmaCheckRequest proto.Request

const (
	applyTimeout           = 10 * time.Second
	peersInfoPath          = "raft/peers.info"
	peersPath              = "raft/peers.json"
	connectionPoolCount    = 5
	connectionTimeout      = 10 * time.Second
	trailingScale          = 1.25
	raftDBPath             = "raft.db"
	raftLogCacheSize       = 128
	observerChanLen        = 50
	appliedWaitDelay       = 100 * time.Millisecond
	commitEquivalenceDelay = 50 * time.Millisecond
	leaderWaitDelay        = 100 * time.Millisecond
	snapshotsDirName       = "wsnapshots"
)

const (
	numSnapshots                      = "num_snapshots"
	numSnapshotsFailed                = "num_snapshots_failed"
	numUserSnapshots                  = "num_user_snapshots"
	numUserSnapshotsFailed            = "num_user_snapshots_failed"
	numWALSnapshots                   = "num_wal_snapshots"
	numWALSnapshotsFailed             = "num_wal_snapshots_failed"
	numSnapshotsFull                  = "num_snapshots_full"
	numSnapshotsIncremental           = "num_snapshots_incremental"
	numFullCheckpointFailed           = "num_full_checkpoint_failed"
	numWALCheckpointTruncateFailed    = "num_wal_checkpoint_truncate_failed"
	numAutoVacuums                    = "num_auto_vacuums"
	numAutoVacuumsFailed              = "num_auto_vacuums_failed"
	autoVacuumDuration                = "auto_vacuum_duration"
	numAutoOptimizes                  = "num_auto_optimizes"
	numAutoOptimizesFailed            = "num_auto_optimizes_failed"
	autoOptimizeDuration              = "auto_optimize_duration"
	numBoots                          = "num_boots"
	numBackups                        = "num_backups"
	numLoads                          = "num_loads"
	numRestores                       = "num_restores"
	numRestoresFailed                 = "num_restores_failed"
	numAutoRestores                   = "num_auto_restores"
	numAutoRestoresSkipped            = "num_auto_restores_skipped"
	numAutoRestoresFailed             = "num_auto_restores_failed"
	numRecoveries                     = "num_recoveries"
	numProviderChecks                 = "num_provider_checks"
	numProviderProvides               = "num_provider_provides"
	numProviderProvidesFail           = "num_provider_provides_fail"
	numUncompressedCommands           = "num_uncompressed_commands"
	numCompressedCommands             = "num_compressed_commands"
	numJoins                          = "num_joins"
	numIgnoredJoins                   = "num_ignored_joins"
	numRemovedBeforeJoins             = "num_removed_before_joins"
	numDBStatsErrors                  = "num_db_stats_errors"
	snapshotCreateDuration            = "snapshot_create_duration"
	snapshotCreateChkTruncateDuration = "snapshot_create_chk_truncate_duration"
	snapshotCreateWALCompactDuration  = "snapshot_create_wal_compact_duration"
	numSnapshotPersists               = "num_snapshot_persists"
	numSnapshotPersistsFailed         = "num_snapshot_persists_failed"
	snapshotPersistDuration           = "snapshot_persist_duration"
	snapshotPrecompactWALSize         = "snapshot_precompact_wal_size"
	snapshotWALSize                   = "snapshot_wal_size"
	leaderChangesObserved             = "leader_changes_observed"
	leaderChangesDropped              = "leader_changes_dropped"
	failedHeartbeatObserved           = "failed_heartbeat_observed"
	nodesReapedOK                     = "nodes_reaped_ok"
	nodesReapedFailed                 = "nodes_reaped_failed"
)

// stats captures stats for the Store.
var stats *expvar.Map

func init() {
	// TODO: Implementation truncated
}

// ResetStats resets the expvar stats for this module. Mostly for test purposes.
func ResetStats() {
	// TODO: Implementation truncated
}

// ClusterState defines the possible Raft states the current node can be in
type ClusterState int

// Represents the Raft cluster states
const (
	Leader ClusterState = iota
	Follower
	Candidate
	Shutdown
	Unknown
)

// SnapshotStore is the interface Snapshot stores must implement.
type SnapshotStore interface {
	raft.SnapshotStore

	// FullNeeded returns true if a full snapshot is needed.
	FullNeeded() (bool, error)

	// SetFullNeeded explicitly sets that a full snapshot is needed.
	SetFullNeeded() error

	// Stats returns stats about the Snapshot Store.
	Stats() (map[string]interface{}, error)
}

// Wire Store is a BBolt/badgerDB database, where all changes are made via Raft consensus.
type Store struct {
	open          *rsync.AtomicBool
	raftDir       string
	peersPath     string
	peersInfoPath string

	raftConsensus *raft.Raft // The consensus mechanism.
	ly            Layer
	raftTn        *NodeTransport
	raftID        string // Node ID.

	ShutdownOnRemove     bool
	SnapshotThreshold    uint64
	SnapshotInterval     time.Duration
	LeaderLeaseTimeout   time.Duration
	HeartbeatTimeout     time.Duration
	ElectionTimeout      time.Duration
	ApplyTimeout         time.Duration
	RaftLogLevel         string
	NoFreeListSync       bool
	AutoVacInterval      time.Duration
	AutoOptimizeInterval time.Duration

	// Raft changes observer
	leaderObserversMu sync.RWMutex
	leaderObservers   []chan<- struct{}
	observerClose     chan struct{}
	observerDone      chan struct{}
	observerChan      chan raft.Observation
	observer          *raft.Observer

	firstLogAppliedT time.Time // Time first log is applied
	openT            time.Time // Timestamp when Store opens.

	reqMarshaller *command.RequestMarshaler // Request marshaler for writing to log.
	raftLog       raft.LogStore             // Persistent log store.
	raftStable    raft.StableStore          // Persistent k-v store.
	boltStore     *rlog.Log                 // Physical store.

	// TODO: Create this
	logger zerolog.Logger

	notifyMu        sync.Mutex
	BootstrapExpect int
	bootstrapped    bool
	notifyingNodes  map[string]*Server // List of nodes in the cluster

	// Node-reaping configuration
	ReapTimeout         time.Duration
	ReapReadOnlyTimeout time.Duration

	// Latest log entry index actually reflected by the FSM. Due to Raft code
	// these values are not updated automatically after a Snapshot-restore.
	fsmIdx        *atomic.Uint64
	fsmTarget     *rsync.ReadyTarget[uint64]
	fsmTerm       *atomic.Uint64
	fsmUpdateTime *rsync.AtomicTime // This is node-local time.

	// appendedAtTime is the Leader's clock time when that Leader appended the log entry.
	// The Leader that actually appended the log entry is not necessarily the current Leader.
	appendedAtTime *rsync.AtomicTime

	dbModifiedTime *rsync.AtomicTime // Last time the database file was modified.

	numTrailingLogs uint64

	restorePath   string
	restoreDoneCh chan struct{}

	// Channels that must be closed for the Store to be considered ready.
	readyChans *rsync.ReadyChannels

	// Snapshot
	snapshotDir   string
	snapshotStore SnapshotStore // Snapshot store.

	// Database
	dbDir string
	db    *badger.DB // pointer to the badgerDB

	mu sync.Mutex

	// For whitebox testing
	numFullSnapshots int
	numAutoVacuums   int
	numAutoOptimizes int
	numIgnoredJoins  int
	numNoops         *atomic.Uint64
	numSnapshots     *atomic.Uint64
}

// Compile time checks if all the necessary interfaces are implemented
// kind of hacky - causes circular imports; find a better way
// var _ http.Database = (*Store)(nil)
// var _ http.Store = (*Store)(nil)
// var _ http.Cluster = (*Store)(nil)

type Config struct {
	Dir string    // The working directory for raft.
	Tn  Transport // The underlying Transport for raft.
	ID  string    // Node ID.
}

// allocate a new store in memory and initialize
func New(ly Layer, c *Config) *Store {
	// TODO: Implementation truncated
	return nil
}

// fsmApply applies a Raft log entry to the FSM.
func (s *Store) fsmApply(l *raft.Log) interface{} {
	// TODO: Implementation truncated
	return nil
}

// fsmSnapshot returns a snapshot of the FSM.
func (s *Store) fsmSnapshot() (raft.FSMSnapshot, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// fsmRestore restores the FSM from a snapshot.
func (s *Store) fsmRestore(rc io.ReadCloser) error {
	// TODO: Implementation truncated
	return nil
}

// open the store
func (s *Store) Open() (retError error) {
	// TODO: Implementation truncated
	return nil
}

// raftConfig returns a new Raft config for the store.
func (s *Store) raftConfig() *raft.Config {
	// TODO: Implementation truncated
	return nil
}

func (s *Store) hcLogLevel() hclog.Level {
	// TODO: Implementation truncated
	return 0
}

func (s *Store) logIncremental() bool {
	// TODO: Implementation truncated
	return false
}

func (s *Store) logBackup() bool {
	// TODO: Implementation truncated
	return false
}

// pathExists returns true if the given path exists.
func pathExists(p string) bool {
	// TODO: Implementation truncated
	return false
}

func (s *Store) observe() (closeCh, doneCh chan struct{}) {
	// TODO: Implementation truncated
	return nil, nil
}

// Stepdown forces this node to relinquish leadership to another node in
// the cluster. If this node is not the leader, and 'wait' is true, an error
// will be returned.
func (s *Store) Stepdown(wait bool) error {
	// TODO: Implementation truncated
	return nil
}

// Close closes the store. If wait is true, waits for a graceful shutdown.
// functionality is incomplete
func (s *Store) Close(wait bool) (retErr error) {
	// TODO: Implementation truncated
	return nil
}

// Nodes returns the slice of nodes in the cluster, sorted by ID ascending.
func (s *Store) Nodes() ([]*Server, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// selfLeaderChange is called when this node detects that its leadership
// status has changed.
func (s *Store) selfLeaderChange(leader bool) {
	// TODO: Implementation truncated
}

// installRestore restores data from a restorePath
func (s *Store) installRestore() error {
	// TODO: Implementation truncated
	return nil
}

// remove removes the node, with the given ID, from the cluster.
func (s *Store) remove(id string) error {
	// TODO: Implementation truncated
	return nil
}

// IsNewNode checks if this the a new or pre-existing node
func IsNewNode(raftDir string) bool {
	// TODO: Implementation truncated
	return false
}

// Implementation of the manager

// LeaderAddr returns the address of the current leader. Returns a
// blank string if there is no leader or if the Store is not open.
func (s *Store) LeaderAddr() (string, error) {
	// TODO: Implementation truncated
	return "", nil
}

// LeaderID returns the node ID of the Raft leader. Returns a
// blank string if there is no leader, or an error.
func (s *Store) LeaderID() (string, error) {
	// TODO: Implementation truncated
	return "", nil
}

// LeaderWithID is used to return the current leader address and ID of the cluster.
// It may return empty strings if there is no current leader or the leader is unknown.
func (s *Store) LeaderWithID() (string, string) {
	// TODO: Implementation truncated
	return "", ""
}

// HasLeaderID returns true if the cluster has a leader ID, false otherwise.
func (s *Store) HasLeaderID() bool {
	// TODO: Implementation truncated
	return false
}

// LeaderCommitIndex returns the Raft leader commit index, as indicated
// by the latest AppendEntries RPC. If this node is the Leader then the
// commit index is returned directly from the Raft object.
func (s *Store) LeaderCommitIndex() (uint64, error) {
	// TODO: Implementation truncated
	return 0, nil
}

func (s *Store) CommitIndex() (uint64, error) {
	// TODO: Implementation truncated
	return 0, nil
}

func (s *Store) Remove(rn *commandProto.RemoveNodeRequest) error {
	// TODO: Implementation truncated
	return nil
}

// Notify notifies this Store that a node is ready for bootstrapping at the
// given address. Once the number of known nodes reaches the expected level
// bootstrapping will be attempted using this Store. "Expected level" includes
// this node, so this node must self-notify to ensure the cluster bootstraps
// with the *advertised Raft address* which the Store doesn't know about.
//
// Notifying is idempotent. A node may repeatedly notify the Store without issue.
func (s *Store) Notify(nr *commandProto.NotifyRequest) error {
	// TODO: Implementation truncated
	return nil
}

// Join request to join this store
func (s *Store) Join(jr *commandProto.JoinRequest) error {
	// TODO: Implementation truncated
	return nil
}

// Implementation for the HTTP daemon

// Snapshot performs a snapshot, leaving n trailing logs behind. If n
// is greater than zero, that many logs are left in the log after
// snapshotting. If n is zero, then the number set at Store creation is used.
// Finally, once this function returns, the trailing log configuration value
// is reset to the value set at Store creation.
func (s *Store) Snapshot(n uint64) (retError error) {
	// TODO: Implementation truncated
	return nil
}

// Backup writes a consistent snapshot of the underlying database to dst. This
// can be called while writes are being made to the system. The backup may fail
// if the system is actively snapshotting. The client can just retry in this case.
func (s *Store) Backup(br *proto.BackupRequest, dst io.Writer) (retErr error) {
	// TODO: Implementation truncated
	return nil
}

func (s *Store) Ready() bool {
	// TODO: Implementation truncated
	return false
}

// HasLeader returns true if the cluster has a leader, false otherwise.
func (s *Store) HasLeader() bool {
	// TODO: Implementation truncated
	return false
}

// Committed blocks until the local commit index is greater than or
// equal to the Leader index, as checked when the function is called.
// It returns the committed index. If the Leader index is 0, then the
// system waits until the commit index is at least 1.
func (s *Store) Committed(timeout time.Duration) (uint64, error) {
	// TODO: Implementation truncated
	return 0, nil
}

// WaitForCommitIndex blocks until the local Raft commit index is equal to
// or greater the given index, or the timeout expires.
func (s *Store) WaitForCommitIndex(idx uint64, timeout time.Duration) error {
	// TODO: Implementation truncated
	return nil
}

// Addr returns the address of the store.
func (s *Store) Addr() string {
	// TODO: Implementation truncated
	return ""
}

// logSize returns the size of the Raft log on disk.
func (s *Store) logSize() (int64, error) {
	// TODO: Implementation truncated
	return 0, nil
}

// IsVoter returns true if the current node is a voter in the cluster. If there
// is no reference to the current node in the current cluster configuration then
// false will also be returned.
func (s *Store) IsVoter() (bool, error) {
	// TODO: Implementation truncated
	return false, nil
}

// Stats returns stats for the store.
// Not complete: does not include badger db stats
func (s *Store) Stats() (map[string]interface{}, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// Cluster interface implementation

// Execute executes queries that return no rows, but do modify the database.
func (s *Store) Execute(ex *proto.ExecuteRequest) ([]*proto.ExecuteQueryResponse, error) {
	// TODO: Implementation truncated
	return nil, nil
}

type fsmExecuteQueryResponse struct {
	results []*proto.ExecuteQueryResponse
	error   error
}

// executes the command, IMP this can ONLY be run on the leader as
// we call raft.Apply
func (s *Store) execute(ex *proto.ExecuteRequest) ([]*proto.ExecuteQueryResponse, error) {
	// TODO: Implementation truncated
	return nil, nil
}

func (s *Store) Query(qr *proto.QueryRequest) ([]*proto.QueryRows, error) {
	// TODO: Implementation truncated
	return nil, nil
}

func (s *Store) Request(eqr *commandProto.ExecuteQueryRequest) ([]*commandProto.ExecuteQueryResponse, error) {
	// TODO: Implementation truncated
	return nil, nil
}

// tryCompress attempts to compress the given command. If the command is
// successfully compressed, the compressed byte slice is returned, along with
// a boolean true. If the command cannot be compressed, the uncompressed byte
// slice is returned, along with a boolean false. The stats are updated
// accordingly.
func (s *Store) tryCompress(rq command.Requester) ([]byte, bool, error) {
	// TODO: Implementation truncated
	return nil, false, nil
}

// ID returns the Raft ID of the store.
func (s *Store) ID() string {
	// TODO: Implementation truncated
	return ""
}

func GetNodeAPIAddr(addr string, retries int, timeout time.Duration) (string, error) {
	// TODO: Implementation truncated
	return "", nil
}
