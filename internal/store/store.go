package store

import (
	"errors"
	"expvar"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/command"
	"github.com/tarungka/wire/internal/command/proto"
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
	raftDBPath          = "raft.db"
	raftLogCacheSize    = 128
	observerChanLen     = 50
)

const (
	numBoots                = "num_boots"
	numLoads                = "num_loads"
	numJoins                = "num_joins"
	failedHeartbeatObserved = "failed_heartbeat_observed"
	leaderChangesObserved   = "leader_changes_observed"
	leaderChangesDropped    = "leader_changes_dropped"
	numRecoveries           = "num_recoveries"
	nodesReapedFailed       = "nodes_reaped_failed"
	nodesReapedOK           = "nodes_reaped_ok"
	numAutoRestores         = "num_auto_restores"
	numAutoRestoresSkipped  = "num_auto_restores_skipped"
	numAutoRestoresFailed   = "num_auto_restores_failed"
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
	stats.Add(numRecoveries, 0)
	stats.Add(nodesReapedOK, 0)
	stats.Add(nodesReapedFailed, 0)
	stats.Add(numAutoRestores, 0)
	stats.Add(numAutoRestoresSkipped, 0)
	stats.Add(numAutoRestoresFailed, 0)
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

	mu sync.Mutex
}

type Config struct {
	Dir string    // The working directory for raft.
	Tn  Transport // The underlying Transport for raft.
	ID  string    // Node ID.
	// Logger *log.Logger // The logger to use to log stuff.
}

// func New(ly Layer, ko *koanf.Koanf) *Store {
func New(ly Layer,  c *Config) *Store {

	// raftDir := ko.String("raft_dir")
	// raftId := ko.String("node_id")
	zlog.Debug().Msg("Creating a new store!")

	// TODO: create a zerolog logger
	logger := zerolog.Logger{}

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
		fsmIdx:         &atomic.Uint64{},
		fsmTarget:      rsync.NewReadyTarget[uint64](),
		fsmTerm:        &atomic.Uint64{},
		fsmUpdateTime:  rsync.NewAtomicTime(),
		appendedAtTime: rsync.NewAtomicTime(),
		dbModifiedTime: rsync.NewAtomicTime(),
		logger:         logger,
		// snapshotCAS:     rsync.NewCheckAndSet(),
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

	var err error

	// Reset/set the defaults
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

	// TODO: impl snapshot
	//
	//
	//

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

	// Request to recover node?
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

	// Instantiate the Raft system.
	// ra, err := raft.NewRaft(config, NewFSM(s), s.raftLog, s.raftStable, s.snapshotStore, s.raftTn)
	ra, err := raft.NewRaft(config, NewFSM(s), s.raftLog, s.raftStable, nil, s.raftTn)
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
	config.Logger = hclog.FromStandardLogger(log.New(os.Stderr, "[raft] ", log.LstdFlags), opts)
	return config
}

func (s *Store) hcLogLevel() hclog.Level {
	return hclog.LevelFromString(s.RaftLogLevel)
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
		for {
			select {
			case o := <-s.observerChan:
				switch signal := o.Data.(type) {
				case raft.FailedHeartbeatObservation:
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
				return
			}
		}
	}()
	return closeCh, doneCh
}

// Nodes returns the slice of nodes in the cluster, sorted by ID ascending.
func (s *Store) Nodes() ([]*Server, error) {
	if !s.open.Is() {
		return nil, ErrNotOpen
	}

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

func (s *Store) load(lr *proto.LoadRequest) error {
	return nil
}

func (s *Store) remove(id string) error {
	f := s.raft.RemoveServer(raft.ServerID(id), 0, 0)
	if f.Error() != nil && f.Error() == raft.ErrNotLeader {
		return ErrNotLeader
	}
	return f.Error()
}

func IsNewNode(raftDir string) bool {
	// If there is any preexisting Raft state, then this node
	// has already been created.
	return !pathExists(filepath.Join(raftDir, raftDBPath))
}
