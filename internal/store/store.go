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
func (s *Store) RegisterObserver(o *raft.Observer)
func (s *Store) Request(eqr *proto.ExecuteQueryRequest) ([]*proto.ExecuteQueryResponse, error)
func (s *Store) SetRequestCompression(batch int, size int)
func (s *Store) Stats() (map[string]interface{}, error)
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
func (s *Store) setKeyTime(key string, t time.Time) error
*/

import (
	"errors"
	"expvar"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	"github.com/tarungka/wire/internal/db"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/rsync"
	"github.com/tarungka/wire/internal/snapshot"
	"github.com/tarungka/wire/internal/utils"

	rlog "github.com/tarungka/wire/internal/log"
)

var (
	// ErrNotOpen is returned when a Store is not open.
	ErrNotOpen = errors.New("store not open")

	// ErrOpen is returned when a Store is already open.
	ErrOpen = errors.New("store already open")

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
	ErrNotImplemented = errors.New("Not implemented")
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
	stats = expvar.NewMap("store")
	ResetStats()
}

// ResetStats resets the expvar stats for this module. Mostly for test purposes.
func ResetStats() {
	stats.Init()
	stats.Add(numSnapshots, 0)
	stats.Add(numSnapshotsFailed, 0)
	stats.Add(numUserSnapshots, 0)
	stats.Add(numUserSnapshotsFailed, 0)
	stats.Add(numWALSnapshots, 0)
	stats.Add(numWALSnapshotsFailed, 0)
	stats.Add(numSnapshotsFull, 0)
	stats.Add(numSnapshotsIncremental, 0)
	stats.Add(numFullCheckpointFailed, 0)
	stats.Add(numWALCheckpointTruncateFailed, 0)
	stats.Add(numAutoVacuums, 0)
	stats.Add(numAutoVacuumsFailed, 0)
	stats.Add(autoVacuumDuration, 0)
	stats.Add(numAutoOptimizes, 0)
	stats.Add(numAutoOptimizesFailed, 0)
	stats.Add(autoOptimizeDuration, 0)
	stats.Add(numBoots, 0)
	stats.Add(numBackups, 0)
	stats.Add(numLoads, 0)
	stats.Add(numRestores, 0)
	stats.Add(numRestoresFailed, 0)
	stats.Add(numRecoveries, 0)
	stats.Add(numProviderChecks, 0)
	stats.Add(numProviderProvides, 0)
	stats.Add(numProviderProvidesFail, 0)
	stats.Add(numAutoRestores, 0)
	stats.Add(numAutoRestoresSkipped, 0)
	stats.Add(numAutoRestoresFailed, 0)
	stats.Add(numUncompressedCommands, 0)
	stats.Add(numCompressedCommands, 0)
	stats.Add(numJoins, 0)
	stats.Add(numIgnoredJoins, 0)
	stats.Add(numRemovedBeforeJoins, 0)
	stats.Add(numDBStatsErrors, 0)
	stats.Add(snapshotCreateDuration, 0)
	stats.Add(snapshotCreateChkTruncateDuration, 0)
	stats.Add(snapshotCreateWALCompactDuration, 0)
	stats.Add(numSnapshotPersists, 0)
	stats.Add(numSnapshotPersistsFailed, 0)
	stats.Add(snapshotPersistDuration, 0)
	stats.Add(snapshotPrecompactWALSize, 0)
	stats.Add(snapshotWALSize, 0)
	stats.Add(leaderChangesObserved, 0)
	stats.Add(leaderChangesDropped, 0)
	stats.Add(failedHeartbeatObserved, 0)
	stats.Add(nodesReapedOK, 0)
	stats.Add(nodesReapedFailed, 0)
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

	raft   *raft.Raft // The consensus mechanism.
	ly     Layer
	raftTn *NodeTransport
	raftID string // Node ID.

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
	db    *badger.DB

	mu sync.Mutex

	// For whitebox testing
	numFullSnapshots int
	numAutoVacuums   int
	numAutoOptimizes int
	numIgnoredJoins  int
	numNoops         *atomic.Uint64
	numSnapshots     *atomic.Uint64
}

type Config struct {
	Dir string    // The working directory for raft.
	Tn  Transport // The underlying Transport for raft.
	ID  string    // Node ID.
}

// func New(ly Layer, ko *koanf.Koanf) *Store {
// allocate a new store in memory and initialize
func New(ly Layer, c *Config) *Store {
	newLogger := logger.GetLogger("store")
	newLogger.Print("creating new store")
	return &Store{
		open:            rsync.NewAtomicBool(),
		ly:              ly,
		raftDir:         c.Dir,
		raftID:          c.ID,
		peersPath:       filepath.Join(c.Dir, peersPath),
		peersInfoPath:   filepath.Join(c.Dir, peersInfoPath),
		restoreDoneCh:   make(chan struct{}),
		leaderObservers: make([]chan<- struct{}, 0),
		reqMarshaller:   command.NewRequestMarshaler(),
		notifyingNodes:  make(map[string]*Server),
		ApplyTimeout:    applyTimeout,
		fsmIdx:          &atomic.Uint64{},
		fsmTarget:       rsync.NewReadyTarget[uint64](),
		fsmTerm:         &atomic.Uint64{},
		fsmUpdateTime:   rsync.NewAtomicTime(),
		appendedAtTime:  rsync.NewAtomicTime(),
		dbModifiedTime:  rsync.NewAtomicTime(),
		logger:          newLogger,
		readyChans:      rsync.NewReadyChannels(),
		snapshotDir:     filepath.Join(c.Dir, snapshotsDirName),
		// snapshotCAS:     rsync.NewCheckAndSet(),
		// Unsure if I need the following data
		// dbAppliedIdx:    &atomic.Uint64{},
		// appliedTarget:   rsync.NewReadyTarget[uint64](),
		// numNoops:        &atomic.Uint64{},
		// numSnapshots:    &atomic.Uint64{},
	}
}

// open the store
func (s *Store) Open() (retError error) {
	defer func() {
		if retError == nil {
			s.open.Set()
		}
	}()

	var err error

	s.logger.Debug().Msg("Opening the store")
	// fmt.Printf("Opening the store\n")

	// Reset/set the defaults
	s.fsmIdx.Store(0)
	s.fsmTarget.Reset()
	s.fsmTerm.Store(0)
	s.fsmUpdateTime.Store(time.Time{})
	s.appendedAtTime.Store(time.Time{})
	s.openT = time.Now()

	// s.logger.Info().Msgf("Opening store with node ID %s, listening on %s", s.raftID, s.ly.Addr().String())

	s.logger.Info().Msgf("Ensuring data directories exist %s", s.raftDir)
	if err := os.MkdirAll(filepath.Dir(s.raftDir), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.peersPath), 0755); err != nil {
		return err
	}

	// Creating network layer
	nt := raft.NewNetworkTransport(NewTransport((s.ly)), connectionPoolCount, connectionTimeout, nil)
	s.raftTn = NewNodeTransport(nt)

	s.numTrailingLogs = uint64(float64(s.SnapshotThreshold) * trailingScale)

	config := s.raftConfig()
	config.LocalID = raft.ServerID(s.raftID)

	// Create store for the Snapshots.
	snapshotStore, err := snapshot.NewStore(filepath.Join(s.snapshotDir))
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %s", err)
	}
	snapshotStore.LogReaping = s.hcLogLevel() < hclog.Warn
	s.snapshotStore = snapshotStore
	snaps, err := s.snapshotStore.List()
	if err != nil {
		return fmt.Errorf("list snapshots: %s", err)
	}
	s.logger.Printf("%d preexisting snapshots present", len(snaps))

	// Create the log store and stable store
	s.boltStore, err = rlog.New(filepath.Join(s.raftDir, raftDBPath), s.NoFreeListSync)
	if err != nil {
		return fmt.Errorf("new log store: %s", err)
	}
	s.raftStable = s.boltStore
	s.raftLog, err = raft.NewLogCache(raftLogCacheSize, s.boltStore)
	if err != nil {
		return fmt.Errorf("new cached store: %s", err)
	}

	// TODO: Request to recover node?
	if pathExists(s.peersPath) {
		s.logger.Printf("attempting node recovery using %s", s.peersPath)
		config, err := raft.ReadConfigJSON(s.peersPath)
		if err != nil {
			return fmt.Errorf("failed to read peers file: %s", err.Error())
		}
		s.logger.Debug().Msgf("The config is: %v", config)
		// if err = RecoverNode(s.raftDir, s.logger, s.raftLog, s.boltStore, s.snapshotStore, s.raftTn, config); err != nil {
		// 	return fmt.Errorf("failed to recover node: %s", err.Error())
		// }
		// if err := os.Rename(s.peersPath, s.peersInfoPath); err != nil {
		// 	return fmt.Errorf("failed to move %s after recovery: %s", s.peersPath, err.Error())
		// }
		// s.logger.Printf("node recovered successfully using %s", s.peersPath)
		stats.Add(numRecoveries, 1)
	}

	// TODO: add a path here
	s.db, err = db.Open("")
	if err != nil {
		s.logger.Fatal().Err(err).Msgf("failed to create on disk database: %s", err)
	}

	if s.raftLog == nil || s.raftStable == nil || s.raftTn == nil {
		fmt.Print("Something is wrong")
		return fmt.Errorf("error something went horribly wrong")
	}

	// Instantiate the Raft system.
	ra, err := raft.NewRaft(config, NewFSM(s), s.raftLog, s.raftStable, s.snapshotStore, s.raftTn)
	if err != nil {
		return fmt.Errorf("creating the raft system failed: %s", err)
	}
	s.raft = ra

	// Open the observer channels.
	s.observerChan = make(chan raft.Observation, observerChanLen)
	s.observer = raft.NewObserver(s.observerChan, false, func(o *raft.Observation) bool {
		_, isLeaderChange := o.Data.(raft.LeaderObservation)
		_, isFailedHeartBeat := o.Data.(raft.FailedHeartbeatObservation)
		return isLeaderChange || isFailedHeartBeat
	})

	// Register and listen for leader changes.
	s.raft.RegisterObserver(s.observer)
	s.observerClose, s.observerDone = s.observe()

	return nil
}

// raftConfig returns a new Raft config for the store.
func (s *Store) raftConfig() *raft.Config {
	config := raft.DefaultConfig()
	config.ShutdownOnRemove = s.ShutdownOnRemove
	config.LogLevel = s.RaftLogLevel
	if s.SnapshotThreshold != 0 {
		config.SnapshotThreshold = s.SnapshotThreshold
		config.TrailingLogs = s.numTrailingLogs
	}
	if s.SnapshotInterval != 0 {
		config.SnapshotInterval = s.SnapshotInterval
	}
	if s.LeaderLeaseTimeout != 0 {
		config.LeaderLeaseTimeout = s.LeaderLeaseTimeout
	}
	if s.HeartbeatTimeout != 0 {
		config.HeartbeatTimeout = s.HeartbeatTimeout
	}
	if s.ElectionTimeout != 0 {
		config.ElectionTimeout = s.ElectionTimeout
	}
	opts := hclog.DefaultOptions
	opts.Name = ""
	opts.Level = s.hcLogLevel()
	// Todo: need to update this?
	config.Logger = hclog.FromStandardLogger(log.New(os.Stderr, "[ raft] ", log.LstdFlags), opts)
	return config
}

func (s *Store) hcLogLevel() hclog.Level {
	return hclog.LevelFromString(s.RaftLogLevel)
}

func (s *Store) logIncremental() bool {
	return s.hcLogLevel() < hclog.Warn
}

func (s *Store) logBackup() bool {
	return s.hcLogLevel() < hclog.Warn
}

// pathExists returns true if the given path exists.
func pathExists(p string) bool {
	if _, err := os.Lstat(p); err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}

func (s *Store) observe() (closeCh, doneCh chan struct{}) {
	closeCh = make(chan struct{})
	doneCh = make(chan struct{})

	go func() {
		defer close(doneCh)
		s.logger.Print("observing for leader changes")
		for {
			select {
			case o := <-s.observerChan:
				switch signal := o.Data.(type) {
				case raft.FailedHeartbeatObservation:
					s.logger.Print("heartbeat failed")
					stats.Add(failedHeartbeatObserved, 1)

					nodes, err := s.Nodes()
					if err != nil {
						s.logger.Info().Msgf("failed to get nodes configuration during reap check: %s", err.Error())
					}
					servers := Servers(nodes)
					id := string(signal.PeerID)
					dur := time.Since(signal.LastContact)

					isReadOnly, found := servers.IsReadOnly(id)
					if !found {
						s.logger.Info().Msgf("node %s (failing heartbeat) is not present in configuration", id)
						break
					}

					if (isReadOnly && s.ReapReadOnlyTimeout > 0 && dur > s.ReapReadOnlyTimeout) ||
						(!isReadOnly && s.ReapTimeout > 0 && dur > s.ReapTimeout) {
						pn := "voting node"
						if isReadOnly {
							pn = "non-voting node"
						}
						if err := s.remove(id); err != nil {
							stats.Add(nodesReapedFailed, 1)
							s.logger.Printf("failed to reap %s %s: %s", pn, id, err.Error())
						} else {
							stats.Add(nodesReapedOK, 1)
							s.logger.Printf("successfully reaped %s %s", pn, id)
						}
					}
				case raft.LeaderObservation:
					s.logger.Print("leader change")
					s.leaderObserversMu.RLock()
					for i := range s.leaderObservers {
						select {
						case s.leaderObservers[i] <- struct{}{}:
							stats.Add(leaderChangesObserved, 1)
						default:
							stats.Add(leaderChangesDropped, 1)
						}
					}
					s.leaderObserversMu.RUnlock()
					s.selfLeaderChange(signal.LeaderID == raft.ServerID(s.raftID))
					if signal.LeaderID == raft.ServerID(s.raftID) {
						s.logger.Printf("this node (ID=%s) is now Leader", s.raftID)
					} else {
						if signal.LeaderID == "" {
							s.logger.Printf("Leader is now unknown")
						} else {
							s.logger.Printf("node %s is now Leader", signal.LeaderID)
						}
					}
				}

			case <-closeCh:
				s.logger.Print("stopping to observe changes for leader")
				return
			}
		}
	}()
	return closeCh, doneCh
}

// Stepdown forces this node to relinquish leadership to another node in
// the cluster. If this node is not the leader, and 'wait' is true, an error
// will be returned.
func (s *Store) Stepdown(wait bool) error {
	if !s.open.Is() {
		return ErrNotOpen
	}
	f := s.raft.LeadershipTransfer()
	if !wait {
		return nil
	}
	return f.Error()
}

// Close closes the store. If wait is true, waits for a graceful shutdown.
// functionality is incomplete
func (s *Store) Close(wait bool) (retErr error) {
	defer func() {
		if retErr == nil {
			s.logger.Printf("store closed with node ID %s, listening on %s", s.raftID, s.ly.Addr().String())
			s.open.Unset()
		}
	}()
	if !s.open.Is() {
		// Protect against closing already-closed resource, such as channels.
		return nil
	}
	// if err := s.snapshotCAS.BeginWithRetry("close", 10*time.Millisecond, 10*time.Second); err != nil {
	// 	return err
	// }
	// defer s.snapshotCAS.End()

	// s.dechunkManager.Close()

	// close(s.observerClose)
	// <-s.observerDone

	// close(s.snapshotWClose)
	// <-s.snapshotWDone

	f := s.raft.Shutdown()
	if wait {
		if f.Error() != nil {
			return f.Error()
		}
	}
	if err := s.raftTn.Close(); err != nil {
		return err
	}

	// Only shutdown Bolt and SQLite when Raft is done.
	if err := s.db.Close(); err != nil {
		return err
	}
	if err := s.boltStore.Close(); err != nil {
		return err
	}
	return nil
}

// Nodes returns the slice of nodes in the cluster, sorted by ID ascending.
func (s *Store) Nodes() ([]*Server, error) {
	if !s.open.Is() {
		return nil, ErrNotOpen
	}

	s.logger.Debug().Msg("a node exists!")

	f := s.raft.GetConfiguration()
	if f.Error() != nil {
		return nil, f.Error()
	}

	rs := f.Configuration().Servers
	servers := make([]*Server, len(rs))
	for i := range rs {
		servers[i] = &Server{
			ID:       string(rs[i].ID),
			Addr:     string(rs[i].Address),
			Suffrage: rs[i].Suffrage.String(),
		}
	}

	sort.Sort(Servers(servers))
	return servers, nil
}

// selfLeaderChange is called when this node detects that its leadership
// status has changed.
func (s *Store) selfLeaderChange(leader bool) {
	if s.restorePath != "" {
		defer func() {
			// Whatever happens, this is a one-shot attempt to perform a restore
			err := os.Remove(s.restorePath)
			if err != nil {
				s.logger.Printf("failed to remove restore path after restore %s: %s",
					s.restorePath, err.Error())
			}
			s.restorePath = ""
			close(s.restoreDoneCh)
		}()

		if !leader {
			s.logger.Printf("different node became leader, not performing auto-restore")
			stats.Add(numAutoRestoresSkipped, 1)
		} else {
			s.logger.Printf("this node is now leader, auto-restoring from %s", s.restorePath)
			if err := s.installRestore(); err != nil {
				s.logger.Printf("failed to auto-restore from %s: %s", s.restorePath, err.Error())
				stats.Add(numAutoRestoresFailed, 1)
				return
			}
			stats.Add(numAutoRestores, 1)
			s.logger.Printf("node auto-restored successfully from %s", s.restorePath)
		}
	}
}

// installRestore restores data from a restorePath
func (s *Store) installRestore() error {
	f, err := os.Open(s.restorePath)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	lr := &proto.LoadRequest{
		Data: b,
	}
	return s.load(lr)
}

// remove removes the node, with the given ID, from the cluster.
func (s *Store) remove(id string) error {
	f := s.raft.RemoveServer(raft.ServerID(id), 0, 0)
	if f.Error() != nil && f.Error() == raft.ErrNotLeader {
		return ErrNotLeader
	}
	return f.Error()
}

// IsNewNode checks if this the a new or pre-existing node
func IsNewNode(raftDir string) bool {
	// If there is any preexisting Raft state, then this node
	// has already been created.
	return !pathExists(filepath.Join(raftDir, raftDBPath))
}

// Implementation of the manager

// LeaderAddr returns the address of the current leader. Returns a
// blank string if there is no leader or if the Store is not open.
func (s *Store) LeaderAddr() (string, error) {
	if s.open.Is() {
		return "", nil
	}
	addr, _ := s.raft.LeaderWithID()
	return string(addr), nil
}

// LeaderID returns the node ID of the Raft leader. Returns a
// blank string if there is no leader, or an error.
func (s *Store) LeaderID() (string, error) {
	if !s.open.Is() {
		return "", nil
	}
	_, id := s.raft.LeaderWithID()
	return string(id), nil
}

// LeaderWithID is used to return the current leader address and ID of the cluster.
// It may return empty strings if there is no current leader or the leader is unknown.
func (s *Store) LeaderWithID() (string, string) {
	if !s.open.Is() {
		return "", ""
	}
	addr, id := s.raft.LeaderWithID()
	return string(addr), string(id)
}

// HasLeaderID returns true if the cluster has a leader ID, false otherwise.
func (s *Store) HasLeaderID() bool {
	id, err := s.LeaderID()
	if err != nil {
		s.logger.Err(err).Msg("Error when getting leader id")
		return false
	}
	return id != ""
}

// LeaderCommitIndex returns the Raft leader commit index, as indicated
// by the latest AppendEntries RPC. If this node is the Leader then the
// commit index is returned directly from the Raft object.
func (s *Store) LeaderCommitIndex() (uint64, error) {
	if !s.open.Is() {
		return 0, ErrNotOpen
	}
	if s.raft.State() == raft.Leader {
		return s.raft.CommitIndex(), nil
	}
	return s.raftTn.LeaderCommitIndex(), nil
}

func (s *Store) CommitIndex() (uint64, error) {
	if !s.open.Is() {
		return 0, ErrNotOpen
	}
	return s.raft.CommitIndex(), nil
}

func (s *Store) Remove(rn *commandProto.RemoveNodeRequest) error {
	if !s.open.Is() {
		return ErrNotOpen
	}
	id := rn.Id

	s.logger.Printf("received request to remove node %s", id)
	if err := s.remove(id); err != nil {
		return err
	}

	s.logger.Printf("node %s removed successfully", id)
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
	s.logger.Printf("notifying node %v", nr)
	if !s.open.Is() {
		return ErrNotOpen
	}

	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()

	if s.BootstrapExpect == 0 || s.bootstrapped || s.HasLeader() {
		// There is no reason this node will bootstrap.
		//
		// - Read-only nodes require that BootstrapExpect is set to 0, so this
		// block ensures that notifying a read-only node will not cause a bootstrap.
		// - If the node is already bootstrapped, then there is nothing to do.
		// - If the node already has a leader, then no bootstrapping is required.
		return nil
	}

	if _, ok := s.notifyingNodes[nr.Id]; ok {
		s.logger.Printf("failed to notify a node %s", nr.Id)
		return nil
	}

	// Confirm that this node can resolve the remote address. This can happen due
	// to incomplete DNS records across the underlying infrastructure. If it can't
	// then don't consider this Notify attempt successful -- so the notifying node
	// will presumably try again.
	if addr, err := resolvableAddress(nr.Address); err != nil {
		return fmt.Errorf("failed to resolve %s: %w", addr, err)
	}
	s.logger.Printf("resolved node %s:%s", nr.Id, nr.Address)

	s.notifyingNodes[nr.Id] = &Server{nr.Id, nr.Address, "voter"}
	if len(s.notifyingNodes) < s.BootstrapExpect {
		s.logger.Printf("not reached a quorum of nodes; current number of nodes are: %d expect: %d", len(s.notifyingNodes), s.BootstrapExpect)
		return nil
	}

	raftServers := make([]raft.Server, 0, len(s.notifyingNodes))
	for _, n := range s.notifyingNodes {
		raftServers = append(raftServers, raft.Server{
			ID:      raft.ServerID(n.ID),
			Address: raft.ServerAddress(n.Addr),
		})
	}

	s.logger.Printf("reached expected bootstrap count of %d, starting cluster bootstrap",
		s.BootstrapExpect)
	bf := s.raft.BootstrapCluster(raft.Configuration{
		Servers: raftServers,
	})
	if bf.Error() != nil {
		s.logger.Printf("cluster bootstrap failed: %s", bf.Error())
	} else {
		s.logger.Printf("cluster bootstrap successful, servers: %s", raftServers)
	}
	s.bootstrapped = true
	return nil
}

// Join request to join this store
func (s *Store) Join(jr *commandProto.JoinRequest) error {
	s.logger.Print("got a join request to the store")
	if !s.open.Is() {
		return ErrNotOpen
	}

	if s.raft.State() != raft.Leader {
		s.logger.Print("join request to store; but not the leader")
		return ErrNotLeader
	}

	id := jr.Id
	addr := jr.Address
	voter := jr.Voter

	// Confirm that this node can resolve the remote address. This can happen due
	// to incomplete DNS records across the underlying infrastructure. If it can't
	// then don't consider this join attempt successful -- so the joining node
	// will presumably try again.
	if addr, err := resolvableAddress(addr); err != nil {
		s.logger.Info().Msgf("failed to resolve %s: %w", addr, err)
		return fmt.Errorf("failed to resolve %s: %w", addr, err)
	}

	configFuture := s.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		s.logger.Printf("failed to get raft configuration: %v", err)
		return err
	}
	s.logger.Printf("the raft configuration is: %v", configFuture)

	for _, srv := range configFuture.Configuration().Servers {
		// If a node already exists with either the joining node's ID or address,
		// that node may need to be removed from the config first.
		if srv.ID == raft.ServerID(id) || srv.Address == raft.ServerAddress(addr) {
			// However, if *both* the ID and the address are the same, then no
			// join is actually needed.
			if srv.Address == raft.ServerAddress(addr) && srv.ID == raft.ServerID(id) {
				stats.Add(numIgnoredJoins, 1)
				s.numIgnoredJoins++
				s.logger.Printf("node %s at %s already member of cluster, ignoring join request", id, addr)
				return nil
			}

			if err := s.remove(id); err != nil {
				s.logger.Printf("failed to remove node %s: %v", id, err)
				return err
			}
			stats.Add(numRemovedBeforeJoins, 1)
			s.logger.Printf("removed node %s prior to rejoin with changed ID or address", id)
		}
	}

	var f raft.IndexFuture
	if voter {
		s.logger.Info().Msgf("adding %v:%v as a voter", id, addr)
		f = s.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 0)
	} else {
		s.logger.Info().Msgf("adding %v:%v as a NON-voter", id, addr)
		f = s.raft.AddNonvoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 0)
	}
	// TODO: understand why would this error
	if e := f.(raft.Future); e.Error() != nil {
		if e.Error() == raft.ErrNotLeader {
			return ErrNotLeader
		}
		return e.Error()
	}
	stats.Add(numJoins, 1)
	s.logger.Printf("node with ID %s, at %s, joined successfully as %s", id, addr, prettyVoter(voter))
	return nil
}

// Implementation for the HTTP daemon

// Snapshot performs a snapshot, leaving n trailing logs behind. If n
// is greater than zero, that many logs are left in the log after
// snapshotting. If n is zero, then the number set at Store creation is used.
// Finally, once this function returns, the trailing log configuration value
// is reset to the value set at Store creation.
func (s *Store) Snapshot(n uint64) (retError error) {
	defer func() {
		if retError != nil {
			stats.Add(numUserSnapshotsFailed, 1)
			s.logger.Printf("failed to generate user-requested snapshot: %s", retError.Error())
		}
	}()

	if n > 0 {
		cfg := s.raft.ReloadableConfig()
		defer func() {
			cfg.TrailingLogs = s.numTrailingLogs
			if err := s.raft.ReloadConfig(cfg); err != nil {
				s.logger.Printf("failed to reload Raft config: %s", err.Error())
			}
		}()
		cfg.TrailingLogs = n
		if err := s.raft.ReloadConfig(cfg); err != nil {
			return fmt.Errorf("failed to reload Raft config: %s", err.Error())
		}
	}
	if err := s.raft.Snapshot().Error(); err != nil {
		if strings.Contains(err.Error(), ErrLoadInProgress.Error()) {
			return ErrLoadInProgress
		}
		return err
	}
	stats.Add(numUserSnapshots, 1)
	return nil
}

// Backup writes a consistent snapshot of the underlying database to dst. This
// can be called while writes are being made to the system. The backup may fail
// if the system is actively snapshotting. The client can just retry in this case.
func (s *Store) Backup(br *proto.BackupRequest, dst io.Writer) (retErr error) {
	// TODO: need to impl
	s.logger.Panic().Msgf("%s", ErrNotImplemented)
	return ErrNotImplemented
}

func (s *Store) Ready() bool {
	// if store is open and all readyChans are closed and has a leader
	return s.open.Is() && s.readyChans.Ready() && s.HasLeader()
}

// HasLeader returns true if the cluster has a leader, false otherwise.
func (s *Store) HasLeader() bool {
	if !s.open.Is() {
		return false
	}
	return s.raft.Leader() != ""
}

// Committed blocks until the local commit index is greater than or
// equal to the Leader index, as checked when the function is called.
// It returns the committed index. If the Leader index is 0, then the
// system waits until the commit index is at least 1.
func (s *Store) Committed(timeout time.Duration) (uint64, error) {
	lci, err := s.LeaderCommitIndex()
	if err != nil {
		return lci, err
	}
	return lci, s.WaitForCommitIndex(max(1, lci), timeout)
}

// WaitForCommitIndex blocks until the local Raft commit index is equal to
// or greater the given index, or the timeout expires.
func (s *Store) WaitForCommitIndex(idx uint64, timeout time.Duration) error {
	check := func() bool {
		return s.raft.CommitIndex() >= idx
	}
	return rsync.NewPollTrue(check, commitEquivalenceDelay, timeout).Run("commit index")
}

// Addr returns the address of the store.
func (s *Store) Addr() string {
	if !s.open.Is() {
		return ""
	}
	return string(s.raftTn.LocalAddr())
}

// logSize returns the size of the Raft log on disk.
func (s *Store) logSize() (int64, error) {
	fi, err := os.Stat(filepath.Join(s.raftDir, raftDBPath))
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// IsVoter returns true if the current node is a voter in the cluster. If there
// is no reference to the current node in the current cluster configuration then
// false will also be returned.
func (s *Store) IsVoter() (bool, error) {
	if !s.open.Is() {
		return false, ErrNotOpen
	}

	cfg := s.raft.GetConfiguration()
	if err := cfg.Error(); err != nil {
		return false, err
	}
	for _, srv := range cfg.Configuration().Servers {
		if srv.ID == raft.ServerID(s.raftID) {
			return srv.Suffrage == raft.Voter, nil
		}
	}
	return false, nil
}

// Stats returns stats for the store.
// Not complete: does not include badger db stats
func (s *Store) Stats() (map[string]interface{}, error) {
	if !s.open.Is() {
		return map[string]interface{}{
			"open": false,
		}, nil
	}

	nodes, err := s.Nodes()
	if err != nil {
		return nil, err
	}
	leaderAddr, leaderID := s.LeaderWithID()

	// Perform type-conversion to actual numbers where possible.
	raftStats := make(map[string]interface{})
	for k, v := range s.raft.Stats() {
		if s, err := strconv.ParseInt(v, 10, 64); err != nil {
			raftStats[k] = v
		} else {
			raftStats[k] = s
		}
	}
	raftStats["log_size"], err = s.logSize()
	if err != nil {
		return nil, err
	}
	raftStats["voter"], err = s.IsVoter()
	if err != nil {
		return nil, err
	}
	raftStats["bolt"] = s.boltStore.Stats()
	raftStats["transport"] = s.raftTn.Stats()

	dirSz, err := utils.DirSize(s.raftDir)
	if err != nil {
		return nil, err
	}

	status := map[string]interface{}{
		"open":            s.open,
		"node_id":         s.raftID,
		"raft":            raftStats,
		"fsm_index":       s.fsmIdx.Load(),
		"fsm_term":        s.fsmTerm.Load(),
		"fsm_update_time": s.fsmUpdateTime.Load(),
		"addr":            s.Addr(),
		"leader": map[string]string{
			"node_id": leaderID,
			"addr":    leaderAddr,
		},
		"leader_appended_at_time": s.appendedAtTime.Load(),
		"ready":                   s.Ready(),
		"observer": map[string]uint64{
			"observed": s.observer.GetNumObserved(),
			"dropped":  s.observer.GetNumDropped(),
		},
		"apply_timeout":      s.ApplyTimeout.String(),
		"heartbeat_timeout":  s.HeartbeatTimeout.String(),
		"election_timeout":   s.ElectionTimeout.String(),
		"snapshot_threshold": s.SnapshotThreshold,
		"snapshot_interval":  s.SnapshotInterval.String(),
		// "snapshot_cas":           s.snapshotCAS.Stats(),
		"reap_timeout":           s.ReapTimeout.String(),
		"reap_read_only_timeout": s.ReapReadOnlyTimeout.String(),
		"no_freelist_sync":       s.NoFreeListSync,
		"trailing_logs":          s.numTrailingLogs,
		"request_marshaler":      s.reqMarshaller.Stats(),
		"nodes":                  nodes,
		"dir":                    s.raftDir,
		"dir_size":               dirSz,
		"dir_size_friendly":      utils.FriendlyBytes(uint64(dirSz)),
	}

	// Snapshot stats may be in flux if a snapshot is in progress. Only
	// report them if they are available.
	snapsStats, err := s.snapshotStore.Stats()
	if err == nil {
		status["snapshot_store"] = snapsStats
	}

	return status, nil
}

// Cluster interface implementation

// Execute executes queries that return no rows, but do modify the database.
// func (s *Store) Execute(ex *proto.ExecuteRequest) ([]*proto.ExecuteQueryResponse, error) {
// 	p := (*PragmaCheckRequest)(ex.Request)
// 	if err := p.Check(); err != nil {
// 		return nil, err
// 	}

// 	if !s.open.Is() {
// 		return nil, ErrNotOpen
// 	}

// 	if s.raft.State() != raft.Leader {
// 		return nil, ErrNotLeader
// 	}
// 	if !s.Ready() {
// 		return nil, ErrNotReady
// 	}
// 	return s.execute(ex)
// }

// type fsmExecuteQueryResponse struct {
// 	results []*proto.ExecuteQueryResponse
// 	error   error
// }

// func (s *Store) execute(ex *proto.ExecuteRequest) ([]*proto.ExecuteQueryResponse, error) {
// 	b, compressed, err := s.tryCompress(ex)
// 	if err != nil {
// 		return nil, err
// 	}

// 	c := &proto.Command{
// 		Type:       proto.Command_COMMAND_TYPE_EXECUTE,
// 		SubCommand: b,
// 		Compressed: compressed,
// 	}

// 	b, err = command.Marshal(c)
// 	if err != nil {
// 		return nil, err
// 	}

// 	af := s.raft.Apply(b, s.ApplyTimeout)
// 	if af.Error() != nil {
// 		if af.Error() == raft.ErrNotLeader {
// 			return nil, ErrNotLeader
// 		}
// 		return nil, af.Error()
// 	}
// 	r := af.Response().(*fsmExecuteQueryResponse)
// 	return r.results, r.error
// }

// tryCompress attempts to compress the given command. If the command is
// successfully compressed, the compressed byte slice is returned, along with
// a boolean true. If the command cannot be compressed, the uncompressed byte
// slice is returned, along with a boolean false. The stats are updated
// accordingly.
func (s *Store) tryCompress(rq command.Requester) ([]byte, bool, error) {
	b, compressed, err := s.reqMarshaller.Marshal(rq)
	if err != nil {
		return nil, false, err
	}
	if compressed {
		stats.Add(numCompressedCommands, 1)
	} else {
		stats.Add(numUncompressedCommands, 1)
	}
	return b, compressed, nil
}

// ID returns the Raft ID of the store.
func (s *Store) ID() string {
	return s.raftID
}

// Bootstrap executes a cluster bootstrap on this node, using the given
// Servers as the configuration.
func (s *Store) Bootstrap(servers ...*Server) error {
	raftServers := make([]raft.Server, len(servers))
	for i := range servers {
		raftServers[i] = raft.Server{
			ID:      raft.ServerID(servers[i].ID),
			Address: raft.ServerAddress(servers[i].Addr),
		}
	}
	fut := s.raft.BootstrapCluster(raft.Configuration{
		Servers: raftServers,
	})
	return fut.Error()
}

func resolvableAddress(addr string) (string, error) {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		// Just try the given address directly.
		h = addr
	}
	_, err = net.LookupHost(h)
	return h, err
}

// prettyVoter converts bool to "voter" or "non-voter"
func prettyVoter(v bool) string {
	if v {
		return "voter"
	}
	return "non-voter"
}

// Database related functions

// Loads an entire BadgerDB file into the database, sending the request
// through the Raft log.
func (s *Store) Load(lr *proto.LoadRequest) error {
	if !s.open.Is() {
		return ErrNotOpen
	}

	if !s.Ready() {
		return ErrNotReady
	}

	if err := s.load(lr); err != nil {
		return err
	}
	stats.Add(numLoads, 1)
	return nil
}

// load loads an entire BadgerDb file into the database, and is for internal use
// only. It does not check for readiness, and does not update statistics.
func (s *Store) load(lr *proto.LoadRequest) error {
	startT := time.Now()

	b, err := command.MarshalLoadRequest(lr)
	if err != nil {
		s.logger.Printf("load failed during load-request marshalling %s", err.Error())
		return err
	}

	c := &proto.Command{
		Type:       proto.Command_COMMAND_TYPE_LOAD,
		SubCommand: b,
	}

	b, err = command.Marshal(c)
	if err != nil {
		return err
	}

	// TODO: need to test if the FSM is aware of these changes to flush it
	// to the BadgerDB
	// TODO: the impl is incomplete
	af := s.raft.Apply(b, s.ApplyTimeout)
	if af.Error() != nil {
		if af.Error() == raft.ErrNotLeader {
			return ErrNotLeader
		}
		s.logger.Printf("load failed during Apply: %s", af.Error())
		return af.Error()
	}
	s.logger.Printf("node loaded in %s (%d bytes)", time.Since(startT), len(b))
	return nil
}

// fsmSnapshot returns a snapshot of the database.
//
// Hashicorp Raft guarantees that this function will not be called concurrently
// with Apply, as it states Apply() and Snapshot() are always called from the same
// thread.
func (s *Store) fsmSnapshot() (fSnap raft.FSMSnapshot, retErr error) {
	return nil, ErrNotImplemented
}

func (s *Store) fsmApply(l *raft.Log) (e interface{}) {
	return ErrNotImplemented
}

// TODO: implementation is not complete
// fsmRestore restores the node to a previous state. The Hashicorp docs state this
// will not be called concurrently with Apply(), so synchronization with Execute()
// is not necessary.
func (s *Store) fsmRestore(rc io.ReadCloser) (retErr error) {
	defer func() {
		if retErr != nil {
			// stats.Add(numRestoresFailed, 1)
		}
	}()
	s.logger.Printf("initiating node restore on node ID %s", s.raftID)

	return nil

	// startT := time.Now()
	// // Create a scatch file to write the restore data to.
	// tmpFile, err := createTemp(s.dbDir, restoreScratchPattern)
	// if err != nil {
	// 	return fmt.Errorf("error creating temporary file for restore operation: %v", err)
	// }
	// defer os.Remove(tmpFile.Name())
	// defer tmpFile.Close()

	// // Copy it from the reader to the temporary file.
	// _, err = io.Copy(tmpFile, rc)
	// if err != nil {
	// 	return fmt.Errorf("error copying restore data: %v", err)
	// }
	// if err := tmpFile.Close(); err != nil {
	// 	return fmt.Errorf("error creating temporary file for restore operation: %v", err)
	// }

	// if err := s.db.Swap(tmpFile.Name(), s.dbConf.FKConstraints, true); err != nil {
	// 	return fmt.Errorf("error swapping database file: %v", err)
	// }
	// s.logger.Printf("successfully opened database at %s due to restore", s.db.Path())

	// // Take conservative approach and assume that everything has changed, so update
	// // the indexes. It is possible that dbAppliedIdx is now ahead of some other nodes'
	// // same value, since the last index is not necessarily a database-changing index,
	// // but that is OK. Worse that can happen is that anything paying attention to the
	// // index might consider the database to be changed when it is not, *logically* speaking.
	// li, tm, err := snapshot.LatestIndexTerm(s.snapshotDir)
	// if err != nil {
	// 	return fmt.Errorf("failed to get latest snapshot index post restore: %s", err)
	// }
	// s.fsmIdx.Store(li)
	// s.fsmTarget.Signal(li)
	// s.fsmTerm.Store(tm)
	// s.dbAppliedIdx.Store(li)
	// s.appliedTarget.Signal(li)
	// lt, err := s.db.DBLastModified()
	// if err != nil {
	// 	return fmt.Errorf("failed to get last modified time: %s", err)
	// }
	// s.dbModifiedTime.Store(lt)

	// stats.Add(numRestores, 1)
	// s.logger.Printf("node restored in %s", time.Since(startT))
	// rc.Close()
	// return nil
}

// Common raft functions

// IsLeader return true if the node is the cluster leader else returns false
func (s *Store) IsLeader() bool {
	if !s.open.Is() {
		return false
	}
	return s.raft.State() == raft.Leader
}

// Path returns the path to the store's storage directory.
func (s *Store) Path() string {
	return s.raftDir
}

// State returns the current node's Raft state
func (s *Store) State() ClusterState {
	if !s.open.Is() {
		return Unknown
	}
	state := s.raft.State()
	switch state {
	case raft.Leader:
		return Leader
	case raft.Candidate:
		return Candidate
	case raft.Follower:
		return Follower
	case raft.Shutdown:
		return Shutdown
	default:
		return Unknown
	}
}

// RegisterReadyChannel registers a channel that must be closed before the
// store is considered "ready" to serve requests.
func (s *Store) RegisterReadyChannel(ch <-chan struct{}) {
	s.readyChans.Register(ch)
}

// WaitForLeader blocks until a leader is detected, or the timeout expires.
func (s *Store) WaitForLeader(timeout time.Duration) (string, error) {
	var leaderAddr string
	check := func() bool {
		var chkErr error
		leaderAddr, chkErr = s.LeaderAddr()
		return chkErr == nil && leaderAddr != ""
	}
	err := rsync.NewPollTrue(check, leaderWaitDelay, timeout).Run("leader")
	if err != nil {
		return "", ErrWaitForLeaderTimeout
	}
	return leaderAddr, err
}

// WaitForRemoval blocks until a node with the given ID is removed from the
// cluster or the timeout expires.
func (s *Store) WaitForRemoval(id string, timeout time.Duration) error {
	check := func() bool {
		nodes, err := s.Nodes()
		return err == nil && !Servers(nodes).Contains(id)
	}
	err := rsync.NewPollTrue(check, appliedWaitDelay, timeout).Run("removal")
	if err != nil {
		return ErrWaitForRemovalTimeout
	}
	return nil
}
