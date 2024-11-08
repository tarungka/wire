package store

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/command/proto"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/new/db"
	"github.com/tarungka/wire/internal/new/db/badgerdb"
	"github.com/tarungka/wire/internal/rsync"
	"github.com/tarungka/wire/internal/utils"
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

type Config struct {
	Dir string // The working directory for raft.
	ID  string // Node ID.

	StoreDatabase string // can be one of: badgerdb, rocksdb
}

type NodeStore struct {
	open      *rsync.AtomicBool
	raftDir   string
	peersPath string

	raft   *raft.Raft
	ly     Layer
	raftTn *NodeTransport
	raftID string

	// FSM
	db           *badgerdb.DB // the badger database
	fsmMu        sync.RWMutex
	fsmIndex     *atomic.Uint64
	fsmTerm      *atomic.Uint64
	fsmUpdatedAt *rsync.AtomicTime

	// Snapshot Store
	snapshotStore raft.SnapshotStore

	// Stable Store
	raftStable raft.StableStore // Persistent k-v store.

	// Log Store
	raftLog raft.LogStore // Persistent log store.

	// storeDb is the name of the backend database for the stable and log store
	storeDb string
	// physical store containing info about the the stable and the log store
	dbStore db.DbStore // currently supported are badgerDB, rocksDB and boltDB

	// observer
	observerChan chan raft.Observation
	observer     *raft.Observer

	// Other configs
	snapshotDir string

	logger zerolog.Logger
}

func New(ly Layer, c *Config) (*NodeStore, error) {
	newLogger := logger.GetLogger("store")
	newLogger.Print("creating new store")
	// dbConfig := db.Config{
	// 	Dir: c.Dir,
	// }
	// newDbStore, err := db.New(c.DatabaseType, &dbConfig)
	// newDbStore, err := db.New("badgerdb", &dbConfig)
	// if err != nil {
	// 	return nil, err
	// }
	return &NodeStore{
		open:    rsync.NewAtomicBool(),
		ly:      ly,
		raftDir: c.Dir,
		raftID:  c.ID,
		logger:  newLogger,
		// dbStore:      newDbStore,
		fsmIndex:     &atomic.Uint64{},
		fsmTerm:      &atomic.Uint64{},
		fsmUpdatedAt: rsync.NewAtomicTime(),
		snapshotDir:  filepath.Join(c.Dir, snapshotsDirName),
		storeDb:      c.StoreDatabase,
	}, nil
}

func (s *NodeStore) Open() (retErr error) {
	defer func() {
		if retErr == nil {
			s.logger.Print("successfully opened the store, setting state to open")
			s.open.Set()
			return
		}
		s.logger.Panic().Msgf("error when opening store: %v", retErr)
	}()

	var err error

	s.fsmIndex.Store(0)
	s.fsmTerm.Store(0)
	s.fsmUpdatedAt.Store(time.Time{}) // empty

	nt := raft.NewNetworkTransport(NewTransport(s.ly), connectionPoolCount, connectionTimeout, nil)
	s.raftTn = NewNodeTransport(nt)

	snapshotStore, err := raft.NewFileSnapshotStore(s.snapshotDir, 2, os.Stderr)
	if err != nil {
		return err
	}
	s.snapshotStore = snapshotStore

	cfg := &db.Config{
		Dir: "/tmp/new-wire-store",
	}
	s.logger.Printf("the backend database for the store is: %v", s.storeDb)
	s.dbStore, err = db.New(s.storeDb, cfg)
	if err != nil {
		s.logger.Printf("error when creating a new store")
		return err
	}
	s.raftStable = s.dbStore
	s.raftLog, err = raft.NewLogCache(raftLogCacheSize, s.dbStore)
	if err != nil {
		s.logger.Err(err).Msgf("error when creating a new cached log store")
		return err
	}
	// when sending the path for badger, IsNewNode also needs to be updated
	badgerCfg := &badgerdb.Config{Dir: ""} // this defaults to /tmp/badger
	s.db = badgerdb.New(badgerCfg)
	s.db.Open()

	if s.raftLog == nil || s.raftStable == nil || s.raftTn == nil {
		s.logger.Error().Msgf("something went horribly wrong")
		return fmt.Errorf("error something went horribly wrong")
	}

	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(s.raftID)
	// TODO: find a better and more apt fix for this; does not happen in bbolt
	// s.raftStable.SetUint64([]byte("CurrentTerm"), uint64(0))
	// s.raftLog.StoreLog(&raft.Log{})
	// s.raftStable.SetUint64([]byte("CurrentTerm"), uint64(0))
	s.raft, err = raft.NewRaft(config, s, s.raftLog, s.raftStable, snapshotStore, s.raftTn)
	if err != nil {
		s.logger.Err(err).Msg("error when creating a new raft node")
		return fmt.Errorf("creating the raft system failed: %s", err)
	}

	// watch for changes
	s.observerChan = make(chan raft.Observation, observerChanLen)
	s.observer = raft.NewObserver(s.observerChan, false, func(o *raft.Observation) bool {
		_, isLeaderChange := o.Data.(raft.LeaderObservation)
		_, isFailedHeartBeat := o.Data.(raft.FailedHeartbeatObservation)
		return isLeaderChange || isFailedHeartBeat
	})

	// Register and listen for leader changes.
	s.raft.RegisterObserver(s.observer)
	// TODO: write the observer channels

	return nil
}

// Impl of the raft FSM
var _ raft.FSM = (*NodeStore)(nil)

func (s *NodeStore) Apply(l *raft.Log) interface{} {
	defer func() {
		s.fsmIndex.Store(l.Index)
		s.fsmTerm.Store(l.Term)
		s.fsmUpdatedAt.Store(time.Now())
	}()

	s.logger.Print("Applying log to the FSM")

	// The index is can never decrease, ie it is always unique and in order
	// key will always be unique
	key := utils.ConvertUint64ToBytes(l.Index)
	val, err := utils.EncodeMsgPack(l)
	if err != nil {
		return err
	}
	s.fsmMu.Lock()
	defer s.fsmMu.Unlock()
	s.db.Set(key, val.Bytes())
	return nil
}

func (s *NodeStore) Snapshot() (raft.FSMSnapshot, error) {

	s.logger.Print("snapshoting FSM")
	s.fsmMu.RLock()
	defer s.fsmMu.RUnlock()

	name := ""
	fsmSnapshot := NewSnapshot(io.NopCloser(bytes.NewBufferString(name)))
	fs := FSMSnapshot{
		FSMSnapshot: fsmSnapshot,
		OnFailure: func() {
			s.logger.Printf("Persisting snapshot did not succeed, full snapshot needed")
			// if err := s.snapshotStore.SetFullNeeded(); err != nil {
			// 	// If this happens, only recourse is to shut down the node.
			// 	s.logger.Fatalf("failed to set full snapshot needed: %s", err)
			// }
		},
	}

	return &fs, ErrNotImplemented
}

func (s *NodeStore) Restore(snapshot io.ReadCloser) error {
	s.logger.Print("restoing FSM")
	return ErrNotImplemented
}

// Impl of the raft Stable Store
var _ raft.StableStore = (*NodeStore)(nil)

func (s *NodeStore) Set(key, val []byte) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	s.logger.Print("setting key and val in stable store")
	return s.dbStore.Set(key, val)
}

// Get returns the value for key, or an empty byte slice if key was not found.
func (s *NodeStore) Get(key []byte) ([]byte, error) {
	if !s.open.Is() {
		return nil, ErrStoreNotOpen
	}
	s.logger.Print("getting key and val in stable store")
	return s.dbStore.Get(key)
}

func (s *NodeStore) SetUint64(key []byte, val uint64) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	s.logger.Print("setting uint64 key and val in stable store")
	return s.dbStore.SetUint64(key, val)
}

// GetUint64 returns the uint64 value for key, or 0 if key was not found.
func (s *NodeStore) GetUint64(key []byte) (uint64, error) {
	if !s.open.Is() {
		return 0, ErrStoreNotOpen
	}
	s.logger.Print("getting uint64 key and val in stable store")
	return s.dbStore.GetUint64(key)
}

// Impl of the raft Log Store
var _ raft.LogStore = (*NodeStore)(nil)

// FirstIndex returns the first index written. 0 for no entries.
func (s *NodeStore) FirstIndex() (uint64, error) {
	if !s.open.Is() {
		return 0, ErrStoreNotOpen
	}
	s.logger.Print("firstIndex in log store")
	return s.dbStore.FirstIndex()
}

// LastIndex returns the last index written. 0 for no entries.
func (s *NodeStore) LastIndex() (uint64, error) {
	if !s.open.Is() {
		return 0, ErrStoreNotOpen
	}
	return s.dbStore.LastIndex()
}

// GetLog gets a log entry at a given index.
func (s *NodeStore) GetLog(index uint64, log *raft.Log) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	return s.dbStore.GetLog(index, log)
}

// StoreLog stores a log entry.
func (s *NodeStore) StoreLog(log *raft.Log) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	return s.dbStore.StoreLog(log)
}

// StoreLogs stores multiple log entries. By default the logs stored may not be contiguous with previous logs (i.e. may have a gap in Index since the last log written). If an implementation can't tolerate this it may optionally implement `MonotonicLogStore` to indicate that this is not allowed. This changes Raft's behaviour after restoring a user snapshot to remove all previous logs instead of relying on a "gap" to signal the discontinuity between logs before the snapshot and logs after.
func (s *NodeStore) StoreLogs(logs []*raft.Log) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	return s.dbStore.StoreLogs(logs)
}

// DeleteRange deletes a range of log entries. The range is inclusive.
func (s *NodeStore) DeleteRange(min, max uint64) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	return s.dbStore.DeleteRange(min, max)
}

func (s *NodeStore) Bootstrap(servers ...*Server) error {
	raftServers := make([]raft.Server, len(servers))

	for i := range servers {
		raftServers[i] = raft.Server{
			ID:      raft.ServerID(servers[i].ID),
			Address: raft.ServerAddress(servers[i].Addr),
		}
	}

	fut := s.raft.BootstrapCluster(raft.Configuration{Servers: raftServers})
	return fut.Error()
}

func (s *NodeStore) Nodes() ([]raft.Server, error) {
	if !s.open.Is() {
		return nil, ErrStoreNotOpen
	}

	raftConfig := s.raft.GetConfiguration()
	return raftConfig.Configuration().Servers, nil
}

func (s *NodeStore) IsLeader() bool {
	if !s.open.Is() {
		return false
	}
	leaderAddr, leaderId := s.raft.LeaderWithID()
	s.logger.Printf("The leader addr is: %v and leader id is: %v", leaderAddr, leaderId)

	return s.raft.State() == raft.Leader
}

// Stepdown forces this node to relinquish leadership to another node in
// the cluster. If this node is not the leader, and 'wait' is true, an error
// will be returned.
func (s *NodeStore) Stepdown(wait bool) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	f := s.raft.LeadershipTransfer()
	if !wait {
		return nil
	}
	return f.Error()
}

// Close closes the store. If wait is true, waits for a graceful shutdown.
func (s *NodeStore) Close(wait bool) (retErr error) {
	defer func() {
		if retErr == nil {
			s.logger.Printf("store closed with node ID %s, listening on %s", s.raftID, s.ly.Addr().String())
			s.open.Unset()
		}
	}()
	if !s.open.Is() {
		return nil
	}

	f := s.raft.Shutdown()
	if wait {
		if f.Error() != nil {
			return f.Error()
		}
	}
	if err := s.raftTn.Close(); err != nil {
		return err
	}

	if err := s.db.Close(); err != nil {
		return err
	}
	if err := s.dbStore.Close(); err != nil {
		return err
	}
	return nil
}

func (s *NodeStore) ID() string {
	return s.raftID
}

func (s *NodeStore) LeaderID() (string, error) {
	if !s.open.Is() {
		return "", ErrStoreNotOpen
	}
	_, id := s.raft.LeaderWithID()
	return string(id), nil
}

// IsNewNode returns whether a node using raftDir would be a brand-new node.
// It also means that the window for this node joining a different cluster has passed.
func IsNewNode(raftDir string) bool {
	// If there is any preexisting Raft state, then this node
	// has already been created.
	return !utils.PathExists("/tmp/badger")
}

func (s *NodeStore) Ready() bool {
	// if store is open and all readyChans are closed and has a leader
	// return s.open.Is() && s.readyChans.Ready() && s.HasLeader()
	return s.open.Is() && s.HasLeader()
}

// HasLeader returns true if the cluster has a leader, false otherwise.
func (s *NodeStore) HasLeader() bool {
	if !s.open.Is() {
		return false
	}
	return s.raft.Leader() != ""
}

// Database

// Cluster
func (s *NodeStore) Join(jr *proto.JoinRequest) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}

	if s.raft.State() != raft.Leader {
		return ErrNotLeader
	}

	id := jr.Id
	addr := jr.Address
	voter := jr.Voter

	s.logger.Printf("got a join request from (id:%v|addr:%v|voter:%v)", id, addr, voter)

	if addr, err := utils.ResolvableAddress(addr); err != nil {
		s.logger.Printf("failed to resolve %s: %v", addr, err)
		return fmt.Errorf("failed to resolve %s: %w", addr, err)
	}

	configFuture := s.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		s.logger.Printf("failed to get raft configuration: %v", err)
		return err
	}

	for _, srv := range configFuture.Configuration().Servers {
		// If a node already exists with either the joining node's ID or address,
		// that node may need to be removed from the config first.
		if srv.ID == raft.ServerID(id) || srv.Address == raft.ServerAddress(addr) {
			if srv.Address == raft.ServerAddress(addr) && srv.ID == raft.ServerID(id) {
				s.logger.Printf("node %s at %s already member of cluster, ignoring join request", id, addr)
				return nil
			}

			// TODO: need to write code to remove the node
			return ErrNotImplemented
		}
	}

	var f raft.IndexFuture
	if voter {
		f = s.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 10*time.Second)
	} else {
		f = s.raft.AddNonvoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 10*time.Second)
	}
	err := f.Error()
	if err != nil {
		s.logger.Err(err).Msgf("error when joining")
		if err == raft.ErrNotLeader {
			return ErrNotLeader
		}
		return err
	}

	// TODO: need to test this
	return ErrNotImplemented
}

func (s *NodeStore) LeaderAddr() (string, error) {
	if !s.open.Is() {
		return "", ErrStoreNotOpen
	}
	addr, _ := s.raft.LeaderWithID()
	return string(addr), nil
}

func (s *NodeStore) CommitIndex() (uint64, error) {
	if !s.open.Is() {
		return 0, ErrStoreNotOpen
	}
	return s.raft.CommitIndex(), nil
}

func (s *NodeStore) Notify(nr *proto.NotifyRequest) error {
	return ErrNotImplemented
}

func (s *NodeStore) Remove(rn *proto.RemoveNodeRequest) error {
	return ErrNotImplemented
}

// Control

// Http
func (s *NodeStore) Committed(timeout time.Duration) (uint64, error) {
	return 0, nil
}

// store a value in the badger database
func (s *NodeStore) StoreInDatabase(key, value string) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	if s.raft.State() != raft.Leader {
		return ErrNotLeader
	}
	if !s.Ready() {
		return ErrNotReady
	}

	s.logger.Trace().Msgf("storing key: %v and value: %v", key, value)
	keyBytes := []byte(key)
	valBytes := []byte(value)
	s.db.Set(keyBytes, valBytes)

	return nil
}

// GetFromDatabase retrieves a value from the Badger database by key.
// Returns the value as a string
func (s *NodeStore) GetFromDatabase(key string) (string, error) {
	if !s.open.Is() {
		return "", ErrStoreNotOpen
	}
	if s.raft.State() != raft.Leader {
		return "", ErrNotLeader
	}
	if !s.Ready() {
		return "", ErrNotReady
	}

	s.logger.Trace().Msgf("retrieving key: %v", key)
	valBytes, err := s.db.Get([]byte(key))
	if err != nil {
		return "", err
	}
	return string(valBytes), nil
}

// LeaderWithID is used to return the current leader address and ID of the cluster.
// It may return empty strings if there is no current leader or the leader is unknown.
func (s *NodeStore) LeaderWithID() (string, string) {
	if !s.open.Is() {
		return "", ""
	}
	addr, id := s.raft.LeaderWithID()
	return string(addr), string(id)
}


// Stats returns stats for the store.
func (s *NodeStore) Stats() (map[string]interface{}, error) {
	if !s.open.Is() {
		return map[string]interface{}{
			"open": false,
		}, nil
	}

	dbStatus, err := s.db.Stats()
	if err != nil {
		// stats.Add(numDBStatsErrors, 1)
		s.logger.Printf("failed to get database stats: %s", err.Error())
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
	// TODO: impl this later
	// raftStats["log_size"], err = s.logSize()
	// if err != nil {
	// 	return nil, err
	// }
	// raftStats["voter"], err = s.IsVoter()
	// if err != nil {
	// 	return nil, err
	// }
	// raftStats["bolt"] = s.boltStore.Stats()
	raftStats["transport"] = s.raftTn.Stats() // might need to find a different impl for this

	dirSz, err := utils.DirSize(s.raftDir)
	if err != nil {
		return nil, err
	}

	status := map[string]interface{}{
		"open":             s.open,
		"node_id":          s.raftID,
		"raft":             raftStats,
		// "fsm_index":        s.fsmIdx.Load(),
		"fsm_term":         s.fsmTerm.Load(),
		// "fsm_update_time":  s.fsmUpdateTime.Load(),
		// "db_applied_index": s.dbAppliedIdx.Load(),
		// "addr":             s.Addr(),
		"leader": map[string]string{
			"node_id": leaderID,
			"addr":    leaderAddr,
		},
		// "leader_appended_at_time": s.appendedAtTime.Load(),
		"ready":                   s.Ready(),
		"observer": map[string]uint64{
			"observed": s.observer.GetNumObserved(),
			"dropped":  s.observer.GetNumDropped(),
		},
		// "apply_timeout":          s.ApplyTimeout.String(),
		// "heartbeat_timeout":      s.HeartbeatTimeout.String(),
		// "election_timeout":       s.ElectionTimeout.String(),
		// "snapshot_threshold":     s.SnapshotThreshold,
		// "snapshot_interval":      s.SnapshotInterval.String(),
		// "snapshot_cas":           s.snapshotCAS.Stats(),
		// "reap_timeout":           s.ReapTimeout.String(),
		// "reap_read_only_timeout": s.ReapReadOnlyTimeout.String(),
		// "no_freelist_sync":       s.NoFreeListSync,
		// "trailing_logs":          s.numTrailingLogs,
		// "request_marshaler":      s.reqMarshaller.Stats(),
		"nodes":                  nodes,
		"dir":                    s.raftDir,
		"dir_size":               dirSz,
		"dir_size_friendly":      utils.FriendlyBytes(uint64(dirSz)),
		"sqlite3":                dbStatus,
		// "db_conf":                s.dbConf,
	}



	// // Snapshot stats may be in flux if a snapshot is in progress. Only
	// // report them if they are available.
	// snapsStats, err := s.snapshotStore.Stats()
	// if err == nil {
	// 	status["snapshot_store"] = snapsStats
	// }
	return status, nil
}
