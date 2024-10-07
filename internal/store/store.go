package store

import (
	"errors"
	"expvar"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/command"
	"github.com/tarungka/wire/rsync"

	rlog "github.com/tarungka/wire/log"
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
)

const (
	applyTimeout        = 10 * time.Second
	peersInfoPath       = "raft/peers.info"
	peersPath           = "raft/peers.json"
	connectionPoolCount = 5
	connectionTimeout   = 10 * time.Second
	trailingScale       = 1.25
)

const (
	numBoots                = "num_boots"
	numLoads                = "num_loads"
	numJoins                = "num_joins"
	failedHeartbeatObserved = "failed_heartbeat_observed"
	leaderChangesObserved   = "leader_changes_observed"
	leaderChangesDropped    = "leader_changes_dropped"
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
	stats.Add(numBoots, 0)
	stats.Add(numLoads, 0)
	stats.Add(numJoins, 0)
	stats.Add(failedHeartbeatObserved, 0)
	stats.Add(leaderChangesObserved, 0)
	stats.Add(leaderChangesDropped, 0)
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

// Wire Store is a BBolt database, where all changes are made via Raft consensus.
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

	mu sync.Mutex
}

type Config struct {
	Dir string    // The working directory for raft.
	Tn  Transport // The underlying Transport for raft.
	ID  string    // Node ID.
	// Logger *log.Logger // The logger to use to log stuff.
}

func New(ly Layer, ko *koanf.Koanf) *Store {

	raftDir := ko.String("raft_dir")
	raftId := ko.String("raft_id")

	// TODO: create a zerolog logger
	logger := zerolog.Logger{}

	return &Store{
		open:          rsync.NewAtomicBool(),
		ly:            ly,
		raftDir:       raftDir,
		raftID:        raftId,
		peersPath:     filepath.Join(raftDir, peersPath),
		peersInfoPath: filepath.Join(raftDir, peersInfoPath),

		leaderObservers: make([]chan<- struct{}, 0),
		reqMarshaller:   command.NewRequestMarshaler(),
		notifyingNodes:  make(map[string]*Server),
		ApplyTimeout:    applyTimeout,
		// snapshotCAS:     rsync.NewCheckAndSet(),
		fsmIdx:         &atomic.Uint64{},
		fsmTarget:      rsync.NewReadyTarget[uint64](),
		fsmTerm:        &atomic.Uint64{},
		fsmUpdateTime:  rsync.NewAtomicTime(),
		appendedAtTime: rsync.NewAtomicTime(),
		dbModifiedTime: rsync.NewAtomicTime(),
		logger:         logger,
		// Unsure if I need the following data
		// dbAppliedIdx:    &atomic.Uint64{},
		// appliedTarget:   rsync.NewReadyTarget[uint64](),
		// numNoops:        &atomic.Uint64{},
		// numSnapshots:    &atomic.Uint64{},
	}
}

func (s *Store) Open() (retError error) {

	defer func() {
		if retError == nil {
			s.open.Set()
		}
	}()

	s.fsmIdx.Store(0)
	s.fsmTarget.Reset()
	s.fsmTerm.Store(0)
	s.fsmUpdateTime.Store(time.Time{})
	s.appendedAtTime.Store(time.Time{})
	s.openT = time.Now()

	s.logger.Info().Msgf("Opening store with node ID %s, listening on %s", s.raftID, s.ly.Addr().String())

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
	config.Logger = hclog.FromStandardLogger(log.New(os.Stderr, "[raft] ", log.LstdFlags), opts)
	return config
}

func (s *Store) hcLogLevel() hclog.Level {
	return hclog.LevelFromString(s.RaftLogLevel)
}
