package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/v2"
	"github.com/tarungka/wire/internal/wal"
)

const (
	DiscoModeNone     = ""
	DiscoModeConsulKV = "consul-kv"
	DiscoModeEtcdKV   = "etcd-kv"
	DiscoModeDNS      = "dns"
	DiscoModeDNSSRV   = "dns-srv"

	HTTPAddrFlag    = "http-addr"
	HTTPAdvAddrFlag = "http-adv-addr"
	RaftAddrFlag    = "raft-addr"
	RaftAdvAddrFlag = "raft-adv-addr"

	HTTPx509CertFlag = "http-cert"
	HTTPx509KeyFlag  = "http-key"
	NodeX509CertFlag = "node-cert"
	NodeX509KeyFlag  = "node-key"
)

// StringSlice is a slice of strings which implements the flag.Value interface.
type StringSliceValue []string

// String returns a string representation of the slice.
func (s *StringSliceValue) String() string {
	return fmt.Sprintf("%v", *s)
}

// Set sets the value of the slice.
func (s *StringSliceValue) Set(value string) error {
	*s = strings.Split(value, ",")
	var r []string
	for _, v := range *s {
		if v != "" {
			r = append(r, v)
		}
	}
	*s = r
	return nil
}

// Config represents the configuration as set by command-line flags.
// All variables will be set, unless explicit noted.
type Config struct {

	// ConfigPath is the path the config file. May not be set
	ConfigPath []string

	// DataPath is path to node data. Always set.
	DataPath string

	// HTTPAddr is the bind network address for the HTTP Server.
	// It never includes a trailing HTTP or HTTPS.
	HTTPAddr string

	// HTTPAdv is the advertised HTTP server network.
	HTTPAdv string

	// HTTPAllowOrigin is the value to set for Access-Control-Allow-Origin HTTP header.
	HTTPAllowOrigin string

	// AuthFile is the path to the authentication file. May not be set.
	AuthFile string `filepath:"true"`

	// AutoBackupFile is the path to the auto-backup file. May not be set.
	AutoBackupFile string `filepath:"true"`

	// AutoRestoreFile is the path to the auto-restore file. May not be set.
	AutoRestoreFile string `filepath:"true"`

	// HTTPx509CACert is the path to the CA certificate file for when this node verifies
	// other certificates for any HTTP communications. May not be set.
	HTTPx509CACert string `filepath:"true"`

	// HTTPx509Cert is the path to the X509 cert for the HTTP server. May not be set.
	HTTPx509Cert string `filepath:"true"`

	// HTTPx509Key is the path to the private key for the HTTP server. May not be set.
	HTTPx509Key string `filepath:"true"`

	// HTTPVerifyClient indicates whether the HTTP server should verify client certificates.
	HTTPVerifyClient bool

	// NodeX509CACert is the path to the CA certificate file for when this node verifies
	// other certificates for any inter-node communications. May not be set.
	NodeX509CACert string `filepath:"true"`

	// NodeX509Cert is the path to the X509 cert for the Raft server. May not be set.
	NodeX509Cert string `filepath:"true"`

	// NodeX509Key is the path to the X509 key for the Raft server. May not be set.
	NodeX509Key string `filepath:"true"`

	// NoNodeVerify disables checking other nodes' Node X509 certs for validity.
	NoNodeVerify bool

	// NodeVerifyClient enable mutual TLS for node-to-node communication.
	NodeVerifyClient bool

	// NodeVerifyServerName is the hostname to verify on the certificates returned by nodes.
	// If NoNodeVerify is true this field is ignored.
	NodeVerifyServerName string

	// NodeID is the Raft ID for the node.
	NodeID string

	// RaftAddr is the bind network address for the Raft server.
	RaftAddr string

	// RaftAdv is the advertised Raft server address.
	RaftAdv string

	// JoinAddrs is the list of Raft addresses to use for a join attempt.
	JoinAddrs string

	// JoinAttempts is the number of times a node should attempt to join using a
	// given address.
	JoinAttempts int

	// JoinInterval is the time between retrying failed join operations.
	JoinInterval time.Duration

	// JoinAs sets the user join attempts should be performed as. May not be set.
	JoinAs string

	// BootstrapExpect is the minimum number of nodes required for a bootstrap.
	BootstrapExpect int

	// BootstrapExpectTimeout is the maximum time a bootstrap operation can take.
	BootstrapExpectTimeout time.Duration

	// DiscoMode sets the discovery mode. May not be set.
	DiscoMode string

	// DiscoKey sets the discovery prefix key.
	DiscoKey string

	// DiscoConfig sets the path to any discovery configuration file. May not be set.
	DiscoConfig string

	// AutoVacInterval sets the automatic VACUUM interval. Use 0s to disable.
	AutoVacInterval time.Duration

	// AutoOptimizeInterval sets the automatic optimization interval. Use 0s to disable.
	AutoOptimizeInterval time.Duration

	// RaftLogLevel sets the minimum logging level for the Raft subsystem.
	RaftLogLevel string

	// RaftNonVoter controls whether this node is a voting, read-only node.
	RaftNonVoter bool

	// RaftSnapThreshold is the number of outstanding log entries that trigger snapshot.
	RaftSnapThreshold uint64

	// RaftSnapInterval sets the threshold check interval.
	RaftSnapInterval time.Duration

	// RaftLeaderLeaseTimeout sets the leader lease timeout.
	RaftLeaderLeaseTimeout time.Duration

	// RaftHeartbeatTimeout specifies the time in follower state without contact
	// from a Leader before the node attempts an election.
	RaftHeartbeatTimeout time.Duration

	// RaftElectionTimeout specifies the time in candidate state without contact
	// from a Leader before the node attempts an election.
	RaftElectionTimeout time.Duration

	// RaftApplyTimeout sets the Log-apply timeout.
	RaftApplyTimeout time.Duration

	// RaftShutdownOnRemove sets whether Raft should be shutdown if the node is removed
	RaftShutdownOnRemove bool

	// RaftClusterRemoveOnShutdown sets whether the node should remove itself from the cluster on shutdown
	RaftClusterRemoveOnShutdown bool

	// RaftStepdownOnShutdown sets whether Leadership should be relinquished on shutdown
	RaftStepdownOnShutdown bool

	// RaftReapNodeTimeout sets the duration after which a non-reachable voting node is
	// reaped i.e. removed from the cluster.
	RaftReapNodeTimeout time.Duration

	// RaftReapReadOnlyNodeTimeout sets the duration after which a non-reachable non-voting node is
	// reaped i.e. removed from the cluster.
	RaftReapReadOnlyNodeTimeout time.Duration

	// ClusterConnectTimeout sets the timeout when initially connecting to another node in
	// the cluster, for non-Raft communications.
	ClusterConnectTimeout time.Duration

	// WriteQueueCap is the default capacity of Execute queues
	WriteQueueCap int

	// WriteQueueBatchSz is the default batch size for Execute queues
	WriteQueueBatchSz int

	// WriteQueueTimeout is the default time after which any data will be sent on
	// Execute queues, if a batch size has not been reached.
	WriteQueueTimeout time.Duration

	// WriteQueueTx controls whether writes from the queue are done within a transaction.
	WriteQueueTx bool

	// CPUProfile enables CPU profiling.
	CPUProfile string

	// MemProfile enables memory profiling.
	MemProfile string

	// TraceProfile enables trace profiling.
	TraceProfile string

	// DebugMode enables additional logs and other metadata to be printed
	DebugMode bool

	// StoreDatabase is the supporting backend database: badgerdb, bbolt, rocksdb
	StoreDatabase string

	// Pipeline holds pipeline-specific configurations, including WAL.
	Pipeline struct {
		WAL wal.WALConfig `koanf:"wal"`
	} `koanf:"pipeline"`
}

// Validate checks the configuration for internal consistency, and activates
// important rockdb policies. It must be called at least once on a Config
// object before the Config object is used. It is OK to call more than
// once.
func (c *Config) Validate() error {

	if err := c.checkRequired(); err != nil {
		fmt.Println(err)
	}

	dataPath, err := filepath.Abs(c.DataPath)
	if err != nil {
		return fmt.Errorf("failed to determine absolute data path: %s", err.Error())
	}
	c.DataPath = dataPath

	err = c.CheckFilePaths()
	if err != nil {
		return err
	}

	err = c.CheckDirPaths()
	if err != nil {
		return err
	}

	if !bothUnsetSet(c.HTTPx509Cert, c.HTTPx509Key) {
		return fmt.Errorf("either both -%s and -%s must be set, or neither", HTTPx509CertFlag, HTTPx509KeyFlag)
	}
	if !bothUnsetSet(c.NodeX509Cert, c.NodeX509Key) {
		return fmt.Errorf("either both -%s and -%s must be set, or neither", NodeX509CertFlag, NodeX509KeyFlag)

	}

	if c.RaftAddr == c.HTTPAddr {
		return errors.New("HTTP and Raft addresses must differ")
	}

	// Enforce policies regarding addresses
	if c.RaftAdv == "" {
		c.RaftAdv = c.RaftAddr
	}
	if c.HTTPAdv == "" {
		c.HTTPAdv = c.HTTPAddr
	}

	// Node ID policy
	if c.NodeID == "" {
		c.NodeID = c.RaftAdv
	}

	// StoreDatabase defaults to bbolt
	if c.StoreDatabase == "" {
		c.StoreDatabase = "bbolt"
	}

	// Perform some address validity checks.
	if strings.HasPrefix(strings.ToLower(c.HTTPAddr), "http") ||
		strings.HasPrefix(strings.ToLower(c.HTTPAdv), "http") {
		return errors.New("HTTP options should not include protocol (http:// or https://)")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return errors.New("HTTP bind address not valid")
	}

	hadv, _, err := net.SplitHostPort(c.HTTPAdv)
	if err != nil {
		return errors.New("HTTP advertised HTTP address not valid")
	}
	if addr := net.ParseIP(hadv); addr != nil && addr.IsUnspecified() {
		return fmt.Errorf("advertised HTTP address is not routable (%s), specify it via -%s or -%s",
			hadv, HTTPAddrFlag, HTTPAdvAddrFlag)
	}

	if _, rp, err := net.SplitHostPort(c.RaftAddr); err != nil {
		return errors.New("raft bind address not valid")
	} else if _, err := strconv.Atoi(rp); err != nil {
		return errors.New("raft bind port not valid")
	}

	radv, rp, err := net.SplitHostPort(c.RaftAdv)
	if err != nil {
		return errors.New("raft advertised address not valid")
	}
	if addr := net.ParseIP(radv); addr != nil && addr.IsUnspecified() {
		return fmt.Errorf("advertised Raft address is not routable (%s), specify it via -%s or -%s",
			radv, RaftAddrFlag, RaftAdvAddrFlag)
	}
	if _, err := strconv.Atoi(rp); err != nil {
		return errors.New("raft advertised port is not valid")
	}

	if c.RaftAdv == c.HTTPAdv {
		return errors.New("advertised HTTP and Raft addresses must differ")
	}

	// Enforce bootstrapping policies
	if c.BootstrapExpect > 0 && c.RaftNonVoter {
		return errors.New("bootstrapping only applicable to voting nodes")
	}

	// Join parameters OK?
	if c.JoinAddrs != "" {
		addrs := strings.Split(c.JoinAddrs, ",")
		for i := range addrs {
			if _, _, err := net.SplitHostPort(addrs[i]); err != nil {
				return fmt.Errorf("%s is an invalid join address", addrs[i])
			}

			if c.BootstrapExpect == 0 {
				if addrs[i] == c.RaftAdv || addrs[i] == c.RaftAddr {
					return errors.New("node cannot join with itself unless bootstrapping")
				}
				if c.AutoRestoreFile != "" {
					return errors.New("auto-restoring cannot be used when joining a cluster")
				}
			}
		}

		if c.DiscoMode != "" {
			return errors.New("disco mode cannot be used when also explicitly joining a cluster")
		}
	}

	// Valid disco mode?
	switch c.DiscoMode {
	case "":
	case DiscoModeEtcdKV, DiscoModeConsulKV:
		if c.BootstrapExpect > 0 {
			return fmt.Errorf("bootstrapping not applicable when using %s", c.DiscoMode)
		}
	case DiscoModeDNS, DiscoModeDNSSRV:
		if c.BootstrapExpect == 0 && !c.RaftNonVoter {
			return fmt.Errorf("bootstrap-expect value required when using %s with a voting node", c.DiscoMode)
		}
	default:
		return fmt.Errorf("disco mode must be one of %s, %s, %s, or %s",
			DiscoModeConsulKV, DiscoModeEtcdKV, DiscoModeDNS, DiscoModeDNSSRV)
	}

	// Validate WAL Configuration
	if c.Pipeline.WAL.Enabled {
		if c.Pipeline.WAL.Directory == "" {
			return errors.New("WAL directory must be set if WAL is enabled")
		}
		if c.Pipeline.WAL.SegmentSize <= 0 {
			return errors.New("WAL segment_size must be positive")
		}
		if c.Pipeline.WAL.MaxSegments <= 0 {
			return errors.New("WAL max_segments must be positive")
		}
		if _, err := wal.ParseCompressionType(c.Pipeline.WAL.Compression); err != nil {
			return fmt.Errorf("invalid WAL compression type: %w", err)
		}
		// SyncInterval can be 0 (means sync on write if FlushOnWrite is true), so >= 0.
		if c.Pipeline.WAL.SyncInterval < 0 {
			return errors.New("WAL sync_interval must be non-negative")
		}
	}

	return nil
}

// Check if all the required fields are set
func (c *Config) checkRequired() error {
	// TODO: pending impl
	return nil
}

// JoinAddresses returns the join addresses set at the command line. Returns nil
// if no join addresses were set.
func (c *Config) JoinAddresses() []string {
	if c.JoinAddrs == "" {
		return nil
	}
	return strings.Split(c.JoinAddrs, ",")
}

// HTTPURL returns the fully-formed, advertised HTTP API address for this config, including
// protocol, host and port.
func (c *Config) HTTPURL() string {
	apiProto := "http"
	if c.HTTPx509Cert != "" {
		apiProto = "https"
	}
	return fmt.Sprintf("%s://%s", apiProto, c.HTTPAdv)
}

// RaftPort returns the port on which the Raft system is listening. Validate must
// have been called before calling this method.
func (c *Config) RaftPort() int {
	_, port, err := net.SplitHostPort(c.RaftAddr)
	if err != nil {
		panic("RaftAddr not valid")
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		panic("RaftAddr port not valid")
	}
	return p
}

// DiscoConfigReader returns a ReadCloser providing access to the Disco config.
// The caller must call close on the ReadCloser when finished with it. If no
// config was supplied, it returns nil.
func (c *Config) DiscoConfigReader() io.ReadCloser {
	var rc io.ReadCloser
	if c.DiscoConfig == "" {
		return nil
	}

	// Open config file. If opening fails, assume string is the literal config.
	cfgFile, err := os.Open(c.DiscoConfig)
	if err != nil {
		rc = io.NopCloser(bytes.NewReader([]byte(c.DiscoConfig)))
	} else {
		rc = cfgFile
	}
	return rc
}

// CheckFilePaths checks that all file paths in the config exist.
// Empty filepaths are ignored.
func (c *Config) CheckFilePaths() error {
	v := reflect.ValueOf(c).Elem()

	// Iterate through the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		fieldValue := v.Field(i)

		if fieldValue.Kind() != reflect.String {
			continue
		}

		if tagValue, ok := field.Tag.Lookup("filepath"); ok && tagValue == "true" {
			filePath := fieldValue.String()
			if filePath == "" {
				continue
			}
			if !fileExists(filePath) {
				return fmt.Errorf("%s does not exist", filePath)
			}
		}
	}
	return nil
}

// CheckDirPaths checks that all directory paths in the config exist and are directories.
// Empty directory paths are ignored.
func (c *Config) CheckDirPaths() error {
	v := reflect.ValueOf(c).Elem()

	// Iterate through the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		fieldValue := v.Field(i)

		if fieldValue.Kind() != reflect.String {
			continue
		}

		if tagValue, ok := field.Tag.Lookup("dirpath"); ok && tagValue == "true" {
			dirPath := fieldValue.String()
			if dirPath == "" {
				continue
			}
			if !fileExists(dirPath) {
				return fmt.Errorf("%s does not exist", dirPath)
			}
			if !isDir(dirPath) {
				return fmt.Errorf("%s is not a directory", dirPath)
			}
		}
	}
	return nil
}

type BuildInfo struct {
	Version string
	Commit  string
	Branch  string
}

// func initFlags(ko *koanf.Koanf) (*Config, error) {
func initFlags(name, desc string, build *BuildInfo) (*Config, error) {

	if pflag.Parsed() {
		return nil, fmt.Errorf("command-line flags already parsed")
	}

	config := &Config{}
	showVersion := false

	f := pflag.NewFlagSet("config", pflag.ExitOnError)

	f.String("port", "8080", "port to host the web server on")
	f.Bool("override", false, "override the command line arguments with the specified config file")

	// flags related to the raft consensus
	f.String("raft_addr", "localhost:10092", "address of the raft connection")
	f.String("http_addr", "localhost:8081", "address of the http server")
	f.String("raft_dir", "./raft_database", "address of the raft connection")
	f.String("node_id", "node0", "address of the raft connection")

	// Show version information
	f.BoolVar(&showVersion, "version", false, "Show version information and exit")

	// New configs
	f.StringSliceVar(&config.ConfigPath, "config", []string{".config/config.json"}, "path to one or more config files (will be merged in order)")
	f.StringVar(&config.NodeID, "node-id", "", "Unique ID for node. If not set, set to advertised Raft address")
	// New store related configs
	f.StringVar(&config.StoreDatabase, "store-db", "bbolt", "The backend database for the stable and the log store")

	// Raft configs
	f.StringVar(&config.DataPath, "raft-dir", "", "Raft directory")
	f.StringVar(&config.RaftAddr, RaftAddrFlag, "localhost:4002", "Raft communication bind address")
	f.StringVar(&config.RaftAdv, RaftAdvAddrFlag, "", "Advertised Raft communication address. If not set, same as Raft bind address")
	f.StringVar(&config.JoinAddrs, "join", "", "Comma-delimited list of nodes, in host:port form, through which a cluster can be joined")
	f.IntVar(&config.JoinAttempts, "join-attempts", 5, "Number of join attempts to make")
	f.DurationVar(&config.JoinInterval, "join-interval", 3*time.Second, "Period between join attempts")
	f.StringVar(&config.JoinAs, "join-as", "", "Username in authentication file to join as. If not set, joins anonymously")
	f.IntVar(&config.BootstrapExpect, "bootstrap-expect", 0, "Minimum number of nodes required for a bootstrap")
	f.DurationVar(&config.BootstrapExpectTimeout, "bootstrap-expect-timeout", 120*time.Second, "Maximum time for bootstrap process")
	f.BoolVar(&config.RaftNonVoter, "raft-non-voter", false, "Configure as non-voting node")
	f.DurationVar(&config.RaftHeartbeatTimeout, "raft-timeout", time.Second, "Raft heartbeat timeout")
	f.DurationVar(&config.RaftElectionTimeout, "raft-election-timeout", time.Second, "Raft election timeout")
	f.DurationVar(&config.RaftApplyTimeout, "raft-apply-timeout", 10*time.Second, "Raft apply timeout")
	f.Uint64Var(&config.RaftSnapThreshold, "raft-snap", 8192, "Number of outstanding log entries which triggers Raft snapshot")
	f.DurationVar(&config.RaftSnapInterval, "raft-snap-int", 10*time.Second, "Snapshot threshold check interval")
	f.DurationVar(&config.RaftLeaderLeaseTimeout, "raft-leader-lease-timeout", 0, "Raft leader lease timeout. Use 0s for Raft default")
	f.BoolVar(&config.RaftStepdownOnShutdown, "raft-shutdown-stepdown", true, "If leader, stepdown before shutting down. Enabled by default")
	f.BoolVar(&config.RaftShutdownOnRemove, "raft-remove-shutdown", false, "Shutdown Raft if node removed from cluster")
	f.BoolVar(&config.RaftClusterRemoveOnShutdown, "raft-cluster-remove-shutdown", false, "Node removes itself from cluster on graceful shutdown")
	f.StringVar(&config.RaftLogLevel, "raft-log-level", "DEBUG", "Minimum log level for Raft module")
	f.DurationVar(&config.RaftReapNodeTimeout, "raft-reap-node-timeout", 0*time.Hour, "Time after which a non-reachable voting node will be reaped. If not set, no reaping takes place")
	f.DurationVar(&config.RaftReapReadOnlyNodeTimeout, "raft-reap-read-only-node-timeout", 0*time.Hour, "Time after which a non-reachable non-voting node will be reaped. If not set, no reaping takes place")
	// Communication configs
	f.StringVar(&config.HTTPAddr, HTTPAddrFlag, "localhost:4001", "HTTP server bind address. To enable HTTPS, set X.509 certificate and key")
	f.StringVar(&config.HTTPAdv, HTTPAdvAddrFlag, "", "Advertised HTTP address. If not set, same as HTTP server bind address")
	f.StringVar(&config.HTTPAllowOrigin, "http-allow-origin", "", "Value to set for Access-Control-Allow-Origin HTTP header")
	f.StringVar(&config.HTTPx509CACert, "http-ca-cert", "", "Path to X.509 CA certificate for HTTPS")
	f.StringVar(&config.HTTPx509Cert, HTTPx509CertFlag, "", "Path to HTTPS X.509 certificate")
	f.StringVar(&config.HTTPx509Key, HTTPx509KeyFlag, "", "Path to HTTPS X.509 private key")
	f.BoolVar(&config.HTTPVerifyClient, "http-verify-client", false, "Enable mutual TLS for HTTPS")
	f.StringVar(&config.NodeX509CACert, "node-ca-cert", "", "Path to X.509 CA certificate for node-to-node encryption")
	f.StringVar(&config.NodeX509Cert, NodeX509CertFlag, "", "Path to X.509 certificate for node-to-node mutual authentication and encryption")
	f.StringVar(&config.NodeX509Key, NodeX509KeyFlag, "", "Path to X.509 private key for node-to-node mutual authentication and encryption")
	f.BoolVar(&config.NoNodeVerify, "node-no-verify", false, "Skip verification of any node-node certificate")
	f.BoolVar(&config.NodeVerifyClient, "node-verify-client", false, "Enable mutual TLS for node-to-node communication")
	f.StringVar(&config.NodeVerifyServerName, "node-verify-server-name", "", "Hostname to verify on certificate returned by a node")

	f.StringVar(&config.AuthFile, "auth", "", "Path to authentication and authorization file. If not set, not enabled")
	f.StringVar(&config.AutoBackupFile, "auto-backup", "", "Path to automatic backup configuration file. If not set, not enabled")
	f.StringVar(&config.AutoRestoreFile, "auto-restore", "", "Path to automatic restore configuration file. If not set, not enabled")
	f.DurationVar(&config.AutoVacInterval, "auto-vacuum-int", 0, "Period between automatic VACUUMs. It not set, not enabled")
	f.DurationVar(&config.AutoOptimizeInterval, "auto-optimize-int", mustParseDuration("24h"), `Period between automatic 'PRAGMA optimize'. Set to 0h to disable`)
	f.DurationVar(&config.ClusterConnectTimeout, "cluster-connect-timeout", 30*time.Second, "Timeout for initial connection to other nodes")
	f.IntVar(&config.WriteQueueCap, "write-queue-capacity", 1024, "QueuedWrites queue capacity")
	f.IntVar(&config.WriteQueueBatchSz, "write-queue-batch-size", 128, "QueuedWrites queue batch size")
	f.DurationVar(&config.WriteQueueTimeout, "write-queue-timeout", 50*time.Millisecond, "QueuedWrites queue timeout")
	f.BoolVar(&config.WriteQueueTx, "write-queue-tx", false, "Use a transaction when processing a queued write")
	// Stats configs
	f.StringVar(&config.CPUProfile, "cpu-profile", "", "Path to file for CPU profiling information")
	f.StringVar(&config.MemProfile, "mem-profile", "", "Path to file for memory profiling information")
	f.StringVar(&config.TraceProfile, "trace-profile", "", "Path to file for trace profiling information")
	// Misc configs
	f.BoolVar(&config.DebugMode, "debug", false, "run in debug mode - better logs")

	// WAL Configuration Flags
	// These provide command-line overrides for settings typically in a config file.
	// Using defaults from wal.DefaultWALConfig() as the flag defaults.
	defaultWALCfg := wal.DefaultWALConfig()
	f.BoolVar(&config.Pipeline.WAL.Enabled, "wal-enabled", defaultWALCfg.Enabled, "Enable Write-Ahead Log")
	f.StringVar(&config.Pipeline.WAL.Directory, "wal-directory", defaultWALCfg.Directory, "Directory for WAL segment files")
	f.Int64Var(&config.Pipeline.WAL.SegmentSize, "wal-segment-size", defaultWALCfg.SegmentSize, "Maximum size of a WAL segment file in bytes")
	f.IntVar(&config.Pipeline.WAL.MaxSegments, "wal-max-segments", defaultWALCfg.MaxSegments, "Maximum number of WAL segments to retain")
	f.DurationVar(&config.Pipeline.WAL.SyncInterval, "wal-sync-interval", defaultWALCfg.SyncInterval, "Interval for WAL fsync operations (0 for sync on every write if wal-flush-on-write is true)")
	f.StringVar(&config.Pipeline.WAL.Compression, "wal-compression", defaultWALCfg.Compression, "Compression type for WAL entries (none, snappy)")
	f.BoolVar(&config.Pipeline.WAL.FlushOnWrite, "wal-flush-on-write", defaultWALCfg.FlushOnWrite, "Flush WAL segment buffer on every write")


	// Not supported as now, will add them later
	// f.StringVar(&config.DiscoMode, "disco-mode", "", "Choose clustering discovery mode. If not set, no node discovery is performed")
	// f.StringVar(&config.DiscoKey, "disco-key", "wire", "Key prefix for cluster discovery service")
	// f.StringVar(&config.DiscoConfig, "disco-config", "", "Set discovery config, or path to cluster discovery config file")

	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n%s\n\n", desc)
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", name)
		f.PrintDefaults()
	}

	pflag.CommandLine.MarkHidden("help")

	if err := f.Parse(os.Args[1:]); err != nil {
		fmt.Printf("error when loading flags: %v\n", err)
	}

	if showVersion {
		msg := fmt.Sprintf("%s %s %s %s %s (commit %s, branch %s, compiler %s)",
			name, build.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(),
			build.Commit, build.Branch, runtime.Compiler)
		errorExit(0, msg)
	}

	// Ensure, if set explicitly, that reap times are not too low.
	f.Visit(func(f *pflag.Flag) {
		if f.Name == "raft-reap-node-timeout" || f.Name == "raft-reap-read-only-node-timeout" {
			d, err := time.ParseDuration(f.Value.String())
			if err != nil {
				errorExit(1, fmt.Sprintf("failed to parse duration: %s", err.Error()))
			}
			if d <= 0 {
				errorExit(1, fmt.Sprintf("-%s must be greater than 0", f.Name))
			}
		}
	})

	if err := config.Validate(); err != nil {
		errorExit(1, err.Error())
	}

	// Initialize koanf instance for config file loading
	k := koanf.New(".")

	// Load config files first (e.g., config.yaml, config.json)
	// The paths are taken from the --config flag, which defaults to .config/config.json
	// We should also try .config/config.yaml by default if it exists.
	// For simplicity, let's assume config.ConfigPath contains the paths to check.
	// This part needs to be robust. If multiple config files are provided, they are merged.

	// Add a common default YAML path if not explicitly given via --config
	// and if the default JSON doesn't exist but YAML does.
	// This logic can be improved. For now, just process what's in config.ConfigPath.
	loadedFromFile := false
	for _, cfgPath := range config.ConfigPath {
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			// If default path like ".config/config.json" doesn't exist, try ".config/config.yaml"
			if strings.HasSuffix(cfgPath, ".json") {
				yamlPath := strings.TrimSuffix(cfgPath, ".json") + ".yaml"
				if _, errYaml := os.Stat(yamlPath); !os.IsNotExist(errYaml) {
					cfgPath = yamlPath // Use YAML path instead
				} else {
					continue // Neither default JSON nor YAML exists, skip.
				}
			} else {
				continue // File doesn't exist, skip.
			}
		}

		var p koanf.Parser
		fileExt := filepath.Ext(cfgPath)
		switch fileExt {
		case ".yaml", ".yml":
			p = yaml.Parser()
		case ".json":
			p = json.Parser()
		default:
			fmt.Fprintf(os.Stderr, "warning: unsupported config file extension %s for %s, skipping\n", fileExt, cfgPath)
			continue
		}

		fmt.Fprintf(os.Stdout, "info: loading configuration from %s\n", cfgPath)
		if err := k.Load(file.Provider(cfgPath), p); err != nil {
			// Don't make it fatal if a config file is missing, flags can still be used.
			// But log it as a warning.
			fmt.Fprintf(os.Stderr, "warning: error loading config file %s: %v\n", cfgPath, err)
		} else {
			loadedFromFile = true
		}
	}

	// Unmarshal the loaded configuration into the Config struct.
	// This will populate fields based on the config file content.
	// Fields already set by flags (if flags were parsed into `config` before this)
	// might be overwritten if koanf's unmarshal options are set to do so,
	// or respected if koanf merges. Default is usually overwrite.
	// For WAL, we want file to take precedence, then flags as overrides.
	// The current pflag setup parses flags directly into `config`.
	// So, we unmarshal from koanf first, then pflag values will effectively override.
	// To have file values override initial defaults and flags override file values:
	// 1. Create `config` with Go defaults.
	// 2. Load from file into `config` (overwrites Go defaults).
	// 3. Parse flags into `config` (overwrites file values for flags that were set).

	// Let's re-evaluate the order.
	// The pflag package sets values in `config` directly when f.Parse() is called.
	// So, if we want file settings to be primary and flags to be overrides:
	// A. Create `config` with initial Go defaults (e.g. from wal.DefaultWALConfig for WAL part).
	// B. Parse flags into `config`. This sets values based on flag defaults or user-provided flags.
	// C. Load config file into Koanf (`k`).
	// D. Unmarshal `k` into `config`. This will overwrite values in `config` set by flags IF the keys exist in the file.
	// This means file takes precedence over flag defaults, but command-line provided flags (which changed from default)
	// would be overwritten by file if key is present. This is often desired.

	// If we want flags to ALWAYS override file:
	// 1. Load file into Koanf `k`.
	// 2. Unmarshal `k` into `config`.
	// 3. Parse flags (which will then overwrite any values in `config` that were set from the file).
	// The current code calls f.Parse() early. Let's stick to that and assume flags are overrides.
	// So, after f.Parse(), `config` has values from flags (or flag defaults).
	// Now, we need to load from file and merge carefully if we want file to take precedence for non-set flags.
	// Simplest: if koanf loads, it sets values. If flags were set, they are already in `config`.
	// We need to unmarshal from koanf into a *temporary* struct or map, then merge into `config`
	// only for fields that were NOT set by flags. This is complex.

	// Standard approach: Load files into koanf, then load flags into koanf, then unmarshal once.
	// ko.Load(posflag.Provider(f, ".", k), nil) // This would make flags override file.
	// Let's try that. The `f` flagset is already parsed. We need a new flagset for Koanf provider.

	// For now: The flags have already set their values in `config`.
	// If a config file was loaded by Koanf, we unmarshal it. This will overwrite flag-set values
	// if the keys are present in the file. This means file takes precedence.
	if loadedFromFile {
		if err := k.UnmarshalWithConf("", config, koanf. 영향UnmarshalConf{Tag: "koanf"}); err != nil {
			fmt.Fprintf(os.Stderr, "error unmarshalling config from file: %v\n", err)
			// Not returning error here, as flags might still provide a valid config.
		}
	}

	// Re-validate after potential changes from config file.
	// Especially for WAL.Directory etc. which might now be set.
	if err := config.Validate(); err != nil {
		errorExit(1, fmt.Sprintf("post-config-file validation error: %s", err.Error()))
	}


	return config, nil
}

// func initConfig(cfg *Config, ko *koanf.Koanf) error {
	// case DiscoModeEtcdKV, DiscoModeConsulKV:
	// 	if c.BootstrapExpect > 0 {
	// 		return fmt.Errorf("bootstrapping not applicable when using %s", c.DiscoMode)
	// 	}
	// case DiscoModeDNS, DiscoModeDNSSRV:
	// 	if c.BootstrapExpect == 0 && !c.RaftNonVoter {
	// 		return fmt.Errorf("bootstrap-expect value required when using %s with a voting node", c.DiscoMode)
	// 	}
	// default:
	// 	return fmt.Errorf("disco mode must be one of %s, %s, %s, or %s",
	// 		DiscoModeConsulKV, DiscoModeEtcdKV, DiscoModeDNS, DiscoModeDNSSRV)
	// }

	// Validate WAL Configuration
	if c.Pipeline.WAL.Enabled {
		if c.Pipeline.WAL.Directory == "" {
			return errors.New("WAL directory must be set if WAL is enabled")
		}
		if c.Pipeline.WAL.SegmentSize <= 0 {
			return errors.New("WAL segment_size must be positive")
		}
		if c.Pipeline.WAL.MaxSegments <= 0 {
			return errors.New("WAL max_segments must be positive")
		}
		if _, err := wal.ParseCompressionType(c.Pipeline.WAL.Compression); err != nil {
			return fmt.Errorf("invalid WAL compression type: %w", err)
		}
		// SyncInterval can be 0 (means sync on write if FlushOnWrite is true), so >= 0.
		if c.Pipeline.WAL.SyncInterval < 0 {
			return errors.New("WAL sync_interval must be non-negative")
		}
	}

	return nil
}

// Check if all the required fields are set
func (c *Config) checkRequired() error {
	// TODO: pending impl
	return nil
}

// JoinAddresses returns the join addresses set at the command line. Returns nil
// if no join addresses were set.
func (c *Config) JoinAddresses() []string {
	if c.JoinAddrs == "" {
		return nil
	}
	return strings.Split(c.JoinAddrs, ",")
}

// HTTPURL returns the fully-formed, advertised HTTP API address for this config, including
// protocol, host and port.
func (c *Config) HTTPURL() string {
	apiProto := "http"
	if c.HTTPx509Cert != "" {
		apiProto = "https"
	}
	return fmt.Sprintf("%s://%s", apiProto, c.HTTPAdv)
}

// RaftPort returns the port on which the Raft system is listening. Validate must
// have been called before calling this method.
func (c *Config) RaftPort() int {
	_, port, err := net.SplitHostPort(c.RaftAddr)
	if err != nil {
		panic("RaftAddr not valid")
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		panic("RaftAddr port not valid")
	}
	return p
}

// DiscoConfigReader returns a ReadCloser providing access to the Disco config.
// The caller must call close on the ReadCloser when finished with it. If no
// config was supplied, it returns nil.
func (c *Config) DiscoConfigReader() io.ReadCloser {
	var rc io.ReadCloser
	if c.DiscoConfig == "" {
		return nil
	}

	// Open config file. If opening fails, assume string is the literal config.
	cfgFile, err := os.Open(c.DiscoConfig)
	if err != nil {
		rc = io.NopCloser(bytes.NewReader([]byte(c.DiscoConfig)))
	} else {
		rc = cfgFile
	}
	return rc
}

// CheckFilePaths checks that all file paths in the config exist.
// Empty filepaths are ignored.
func (c *Config) CheckFilePaths() error {
	v := reflect.ValueOf(c).Elem()

	// Iterate through the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		fieldValue := v.Field(i)

		if fieldValue.Kind() != reflect.String {
			continue
		}

		if tagValue, ok := field.Tag.Lookup("filepath"); ok && tagValue == "true" {
			filePath := fieldValue.String()
			if filePath == "" {
				continue
			}
			if !fileExists(filePath) {
				return fmt.Errorf("%s does not exist", filePath)
			}
		}
	}
	return nil
}

// CheckDirPaths checks that all directory paths in the config exist and are directories.
// Empty directory paths are ignored.
func (c *Config) CheckDirPaths() error {
	v := reflect.ValueOf(c).Elem()

	// Iterate through the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		fieldValue := v.Field(i)

		if fieldValue.Kind() != reflect.String {
			continue
		}

		if tagValue, ok := field.Tag.Lookup("dirpath"); ok && tagValue == "true" {
			dirPath := fieldValue.String()
			if dirPath == "" {
				continue
			}
			if !fileExists(dirPath) {
				return fmt.Errorf("%s does not exist", dirPath)
			}
			if !isDir(dirPath) {
				return fmt.Errorf("%s is not a directory", dirPath)
			}
		}
	}
	return nil
}

type BuildInfo struct {
	Version string
	Commit  string
	Branch  string
}

// func initFlags(ko *koanf.Koanf) (*Config, error) {
func initFlags(name, desc string, build *BuildInfo) (*Config, error) {

	if pflag.Parsed() {
		return nil, fmt.Errorf("command-line flags already parsed")
	}

	config := &Config{}
	showVersion := false

	f := pflag.NewFlagSet("config", pflag.ExitOnError)

	f.String("port", "8080", "port to host the web server on")
	f.Bool("override", false, "override the command line arguments with the specified config file")

	// flags related to the raft consensus
	f.String("raft_addr", "localhost:10092", "address of the raft connection")
	f.String("http_addr", "localhost:8081", "address of the http server")
	f.String("raft_dir", "./raft_database", "address of the raft connection")
	f.String("node_id", "node0", "address of the raft connection")

	// Show version information
	f.BoolVar(&showVersion, "version", false, "Show version information and exit")

	// New configs
	f.StringSliceVar(&config.ConfigPath, "config", []string{".config/config.json"}, "path to one or more config files (will be merged in order)")
	f.StringVar(&config.NodeID, "node-id", "", "Unique ID for node. If not set, set to advertised Raft address")
	// New store related configs
	f.StringVar(&config.StoreDatabase, "store-db", "bbolt", "The backend database for the stable and the log store")

	// Raft configs
	f.StringVar(&config.DataPath, "raft-dir", "", "Raft directory")
	f.StringVar(&config.RaftAddr, RaftAddrFlag, "localhost:4002", "Raft communication bind address")
	f.StringVar(&config.RaftAdv, RaftAdvAddrFlag, "", "Advertised Raft communication address. If not set, same as Raft bind address")
	f.StringVar(&config.JoinAddrs, "join", "", "Comma-delimited list of nodes, in host:port form, through which a cluster can be joined")
	f.IntVar(&config.JoinAttempts, "join-attempts", 5, "Number of join attempts to make")
	f.DurationVar(&config.JoinInterval, "join-interval", 3*time.Second, "Period between join attempts")
	f.StringVar(&config.JoinAs, "join-as", "", "Username in authentication file to join as. If not set, joins anonymously")
	f.IntVar(&config.BootstrapExpect, "bootstrap-expect", 0, "Minimum number of nodes required for a bootstrap")
	f.DurationVar(&config.BootstrapExpectTimeout, "bootstrap-expect-timeout", 120*time.Second, "Maximum time for bootstrap process")
	f.BoolVar(&config.RaftNonVoter, "raft-non-voter", false, "Configure as non-voting node")
	f.DurationVar(&config.RaftHeartbeatTimeout, "raft-timeout", time.Second, "Raft heartbeat timeout")
	f.DurationVar(&config.RaftElectionTimeout, "raft-election-timeout", time.Second, "Raft election timeout")
	f.DurationVar(&config.RaftApplyTimeout, "raft-apply-timeout", 10*time.Second, "Raft apply timeout")
	f.Uint64Var(&config.RaftSnapThreshold, "raft-snap", 8192, "Number of outstanding log entries which triggers Raft snapshot")
	f.DurationVar(&config.RaftSnapInterval, "raft-snap-int", 10*time.Second, "Snapshot threshold check interval")
	f.DurationVar(&config.RaftLeaderLeaseTimeout, "raft-leader-lease-timeout", 0, "Raft leader lease timeout. Use 0s for Raft default")
	f.BoolVar(&config.RaftStepdownOnShutdown, "raft-shutdown-stepdown", true, "If leader, stepdown before shutting down. Enabled by default")
	f.BoolVar(&config.RaftShutdownOnRemove, "raft-remove-shutdown", false, "Shutdown Raft if node removed from cluster")
	f.BoolVar(&config.RaftClusterRemoveOnShutdown, "raft-cluster-remove-shutdown", false, "Node removes itself from cluster on graceful shutdown")
	f.StringVar(&config.RaftLogLevel, "raft-log-level", "DEBUG", "Minimum log level for Raft module")
	f.DurationVar(&config.RaftReapNodeTimeout, "raft-reap-node-timeout", 0*time.Hour, "Time after which a non-reachable voting node will be reaped. If not set, no reaping takes place")
	f.DurationVar(&config.RaftReapReadOnlyNodeTimeout, "raft-reap-read-only-node-timeout", 0*time.Hour, "Time after which a non-reachable non-voting node will be reaped. If not set, no reaping takes place")
	// Communication configs
	f.StringVar(&config.HTTPAddr, HTTPAddrFlag, "localhost:4001", "HTTP server bind address. To enable HTTPS, set X.509 certificate and key")
	f.StringVar(&config.HTTPAdv, HTTPAdvAddrFlag, "", "Advertised HTTP address. If not set, same as HTTP server bind address")
	f.StringVar(&config.HTTPAllowOrigin, "http-allow-origin", "", "Value to set for Access-Control-Allow-Origin HTTP header")
	f.StringVar(&config.HTTPx509CACert, "http-ca-cert", "", "Path to X.509 CA certificate for HTTPS")
	f.StringVar(&config.HTTPx509Cert, HTTPx509CertFlag, "", "Path to HTTPS X.509 certificate")
	f.StringVar(&config.HTTPx509Key, HTTPx509KeyFlag, "", "Path to HTTPS X.509 private key")
	f.BoolVar(&config.HTTPVerifyClient, "http-verify-client", false, "Enable mutual TLS for HTTPS")
	f.StringVar(&config.NodeX509CACert, "node-ca-cert", "", "Path to X.509 CA certificate for node-to-node encryption")
	f.StringVar(&config.NodeX509Cert, NodeX509CertFlag, "", "Path to X.509 certificate for node-to-node mutual authentication and encryption")
	f.StringVar(&config.NodeX509Key, NodeX509KeyFlag, "", "Path to X.509 private key for node-to-node mutual authentication and encryption")
	f.BoolVar(&config.NoNodeVerify, "node-no-verify", false, "Skip verification of any node-node certificate")
	f.BoolVar(&config.NodeVerifyClient, "node-verify-client", false, "Enable mutual TLS for node-to-node communication")
	f.StringVar(&config.NodeVerifyServerName, "node-verify-server-name", "", "Hostname to verify on certificate returned by a node")

	f.StringVar(&config.AuthFile, "auth", "", "Path to authentication and authorization file. If not set, not enabled")
	f.StringVar(&config.AutoBackupFile, "auto-backup", "", "Path to automatic backup configuration file. If not set, not enabled")
	f.StringVar(&config.AutoRestoreFile, "auto-restore", "", "Path to automatic restore configuration file. If not set, not enabled")
	f.DurationVar(&config.AutoVacInterval, "auto-vacuum-int", 0, "Period between automatic VACUUMs. It not set, not enabled")
	f.DurationVar(&config.AutoOptimizeInterval, "auto-optimize-int", mustParseDuration("24h"), `Period between automatic 'PRAGMA optimize'. Set to 0h to disable`)
	f.DurationVar(&config.ClusterConnectTimeout, "cluster-connect-timeout", 30*time.Second, "Timeout for initial connection to other nodes")
	f.IntVar(&config.WriteQueueCap, "write-queue-capacity", 1024, "QueuedWrites queue capacity")
	f.IntVar(&config.WriteQueueBatchSz, "write-queue-batch-size", 128, "QueuedWrites queue batch size")
	f.DurationVar(&config.WriteQueueTimeout, "write-queue-timeout", 50*time.Millisecond, "QueuedWrites queue timeout")
	f.BoolVar(&config.WriteQueueTx, "write-queue-tx", false, "Use a transaction when processing a queued write")
	// Stats configs
	f.StringVar(&config.CPUProfile, "cpu-profile", "", "Path to file for CPU profiling information")
	f.StringVar(&config.MemProfile, "mem-profile", "", "Path to file for memory profiling information")
	f.StringVar(&config.TraceProfile, "trace-profile", "", "Path to file for trace profiling information")
	// Misc configs
	f.BoolVar(&config.DebugMode, "debug", false, "run in debug mode - better logs")

	// WAL Configuration Flags
	// These provide command-line overrides for settings typically in a config file.
	// Using defaults from wal.DefaultWALConfig() as the flag defaults.
	defaultWALCfg := wal.DefaultWALConfig()
	f.BoolVar(&config.Pipeline.WAL.Enabled, "wal-enabled", defaultWALCfg.Enabled, "Enable Write-Ahead Log")
	f.StringVar(&config.Pipeline.WAL.Directory, "wal-directory", defaultWALCfg.Directory, "Directory for WAL segment files")
	f.Int64Var(&config.Pipeline.WAL.SegmentSize, "wal-segment-size", defaultWALCfg.SegmentSize, "Maximum size of a WAL segment file in bytes")
	f.IntVar(&config.Pipeline.WAL.MaxSegments, "wal-max-segments", defaultWALCfg.MaxSegments, "Maximum number of WAL segments to retain")
	f.DurationVar(&config.Pipeline.WAL.SyncInterval, "wal-sync-interval", defaultWALCfg.SyncInterval, "Interval for WAL fsync operations (0 for sync on every write if wal-flush-on-write is true)")
	f.StringVar(&config.Pipeline.WAL.Compression, "wal-compression", defaultWALCfg.Compression, "Compression type for WAL entries (none, snappy)")
	f.BoolVar(&config.Pipeline.WAL.FlushOnWrite, "wal-flush-on-write", defaultWALCfg.FlushOnWrite, "Flush WAL segment buffer on every write")


	// Not supported as now, will add them later
	// f.StringVar(&config.DiscoMode, "disco-mode", "", "Choose clustering discovery mode. If not set, no node discovery is performed")
	// f.StringVar(&config.DiscoKey, "disco-key", "wire", "Key prefix for cluster discovery service")
	// f.StringVar(&config.DiscoConfig, "disco-config", "", "Set discovery config, or path to cluster discovery config file")

	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n%s\n\n", desc)
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", name)
		f.PrintDefaults()
	}

	pflag.CommandLine.MarkHidden("help")

	if err := f.Parse(os.Args[1:]); err != nil {
		fmt.Printf("error when loading flags: %v\n", err)
	}

	if showVersion {
		msg := fmt.Sprintf("%s %s %s %s %s (commit %s, branch %s, compiler %s)",
			name, build.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(),
			build.Commit, build.Branch, runtime.Compiler)
		errorExit(0, msg)
	}

	// Ensure, if set explicitly, that reap times are not too low.
	f.Visit(func(f *pflag.Flag) {
		if f.Name == "raft-reap-node-timeout" || f.Name == "raft-reap-read-only-node-timeout" {
			d, err := time.ParseDuration(f.Value.String())
			if err != nil {
				errorExit(1, fmt.Sprintf("failed to parse duration: %s", err.Error()))
			}
			if d <= 0 {
				errorExit(1, fmt.Sprintf("-%s must be greater than 0", f.Name))
			}
		}
	})

	if err := config.Validate(); err != nil {
		errorExit(1, err.Error())
	}

	// Initialize koanf instance for config file loading
	k := koanf.New(".")

	// Load config files first (e.g., config.yaml, config.json)
	// The paths are taken from the --config flag, which defaults to .config/config.json
	// We should also try .config/config.yaml by default if it exists.
	// For simplicity, let's assume config.ConfigPath contains the paths to check.
	// This part needs to be robust. If multiple config files are provided, they are merged.

	// Add a common default YAML path if not explicitly given via --config
	// and if the default JSON doesn't exist but YAML does.
	// This logic can be improved. For now, just process what's in config.ConfigPath.
	loadedFromFile := false
	for _, cfgPath := range config.ConfigPath {
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			// If default path like ".config/config.json" doesn't exist, try ".config/config.yaml"
			if strings.HasSuffix(cfgPath, ".json") {
				yamlPath := strings.TrimSuffix(cfgPath, ".json") + ".yaml"
				if _, errYaml := os.Stat(yamlPath); !os.IsNotExist(errYaml) {
					cfgPath = yamlPath // Use YAML path instead
				} else {
					continue // Neither default JSON nor YAML exists, skip.
				}
			} else {
				continue // File doesn't exist, skip.
			}
		}

		var p koanf.Parser
		fileExt := filepath.Ext(cfgPath)
		switch fileExt {
		case ".yaml", ".yml":
			p = yaml.Parser()
		case ".json":
			p = json.Parser()
		default:
			fmt.Fprintf(os.Stderr, "warning: unsupported config file extension %s for %s, skipping\n", fileExt, cfgPath)
			continue
		}

		fmt.Fprintf(os.Stdout, "info: loading configuration from %s\n", cfgPath)
		if err := k.Load(file.Provider(cfgPath), p); err != nil {
			// Don't make it fatal if a config file is missing, flags can still be used.
			// But log it as a warning.
			fmt.Fprintf(os.Stderr, "warning: error loading config file %s: %v\n", cfgPath, err)
		} else {
			loadedFromFile = true
		}
	}

	// Unmarshal the loaded configuration into the Config struct.
	// This will populate fields based on the config file content.
	// Fields already set by flags (if flags were parsed into `config` before this)
	// might be overwritten if koanf's unmarshal options are set to do so,
	// or respected if koanf merges. Default is usually overwrite.
	// For WAL, we want file to take precedence, then flags as overrides.
	// The current pflag setup parses flags directly into `config`.
	// So, we unmarshal from koanf first, then pflag values will effectively override.
	// To have file values override initial defaults and flags override file values:
	// 1. Create `config` with Go defaults.
	// 2. Load from file into `config` (overwrites Go defaults).
	// 3. Parse flags into `config` (overwrites file values for flags that were set).

	// Let's re-evaluate the order.
	// The pflag package sets values in `config` directly when f.Parse() is called.
	// So, if we want file settings to be primary and flags to be overrides:
	// A. Create `config` with initial Go defaults (e.g. from wal.DefaultWALConfig for WAL part).
	// B. Parse flags into `config`. This sets values based on flag defaults or user-provided flags.
	// C. Load config file into Koanf (`k`).
	// D. Unmarshal `k` into `config`. This will overwrite values in `config` set by flags IF the keys exist in the file.
	// This means file takes precedence over flag defaults, but command-line provided flags (which changed from default)
	// would be overwritten by file if key is present. This is often desired.

	// If we want flags to ALWAYS override file:
	// 1. Load file into Koanf `k`.
	// 2. Unmarshal `k` into `config`.
	// 3. Parse flags (which will then overwrite any values in `config` that were set from the file).
	// The current code calls f.Parse() early. Let's stick to that and assume flags are overrides.
	// So, after f.Parse(), `config` has values from flags (or flag defaults).
	// Now, we need to load from file and merge carefully if we want file to take precedence for non-set flags.
	// Simplest: if koanf loads, it sets values. If flags were set, they are already in `config`.
	// We need to unmarshal from koanf into a *temporary* struct or map, then merge into `config`
	// only for fields that were NOT set by flags. This is complex.

	// Standard approach: Load files into koanf, then load flags into koanf, then unmarshal once.
	// ko.Load(posflag.Provider(f, ".", k), nil) // This would make flags override file.
	// Let's try that. The `f` flagset is already parsed. We need a new flagset for Koanf provider.

	// For now: The flags have already set their values in `config`.
	// If a config file was loaded by Koanf, we unmarshal it. This will overwrite flag-set values
	// if the keys are present in the file. This means file takes precedence.
	if loadedFromFile {
		if err := k.UnmarshalWithConf("", config, koanf. 영향UnmarshalConf{Tag: "koanf"}); err != nil {
			fmt.Fprintf(os.Stderr, "error unmarshalling config from file: %v\n", err)
			// Not returning error here, as flags might still provide a valid config.
		}
	}

	// Re-validate after potential changes from config file.
	// Especially for WAL.Directory etc. which might now be set.
	if err := config.Validate(); err != nil {
		errorExit(1, fmt.Sprintf("post-config-file validation error: %s", err.Error()))
	}


	return config, nil
}

// func initConfig(cfg *Config, ko *koanf.Koanf) error {
	case DiscoModeEtcdKV, DiscoModeConsulKV:
		if c.BootstrapExpect > 0 {
			return fmt.Errorf("bootstrapping not applicable when using %s", c.DiscoMode)
		}
	case DiscoModeDNS, DiscoModeDNSSRV:
		if c.BootstrapExpect == 0 && !c.RaftNonVoter {
			return fmt.Errorf("bootstrap-expect value required when using %s with a voting node", c.DiscoMode)
		}
	default:
		return fmt.Errorf("disco mode must be one of %s, %s, %s, or %s",
			DiscoModeConsulKV, DiscoModeEtcdKV, DiscoModeDNS, DiscoModeDNSSRV)
	}

	return nil
}

// Check if all the required fields are set
func (c *Config) checkRequired() error {
	// TODO: pending impl
	return nil
}

// JoinAddresses returns the join addresses set at the command line. Returns nil
// if no join addresses were set.
func (c *Config) JoinAddresses() []string {
	if c.JoinAddrs == "" {
		return nil
	}
	return strings.Split(c.JoinAddrs, ",")
}

// HTTPURL returns the fully-formed, advertised HTTP API address for this config, including
// protocol, host and port.
func (c *Config) HTTPURL() string {
	apiProto := "http"
	if c.HTTPx509Cert != "" {
		apiProto = "https"
	}
	return fmt.Sprintf("%s://%s", apiProto, c.HTTPAdv)
}

// RaftPort returns the port on which the Raft system is listening. Validate must
// have been called before calling this method.
func (c *Config) RaftPort() int {
	_, port, err := net.SplitHostPort(c.RaftAddr)
	if err != nil {
		panic("RaftAddr not valid")
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		panic("RaftAddr port not valid")
	}
	return p
}

// DiscoConfigReader returns a ReadCloser providing access to the Disco config.
// The caller must call close on the ReadCloser when finished with it. If no
// config was supplied, it returns nil.
func (c *Config) DiscoConfigReader() io.ReadCloser {
	var rc io.ReadCloser
	if c.DiscoConfig == "" {
		return nil
	}

	// Open config file. If opening fails, assume string is the literal config.
	cfgFile, err := os.Open(c.DiscoConfig)
	if err != nil {
		rc = io.NopCloser(bytes.NewReader([]byte(c.DiscoConfig)))
	} else {
		rc = cfgFile
	}
	return rc
}

// CheckFilePaths checks that all file paths in the config exist.
// Empty filepaths are ignored.
func (c *Config) CheckFilePaths() error {
	v := reflect.ValueOf(c).Elem()

	// Iterate through the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		fieldValue := v.Field(i)

		if fieldValue.Kind() != reflect.String {
			continue
		}

		if tagValue, ok := field.Tag.Lookup("filepath"); ok && tagValue == "true" {
			filePath := fieldValue.String()
			if filePath == "" {
				continue
			}
			if !fileExists(filePath) {
				return fmt.Errorf("%s does not exist", filePath)
			}
		}
	}
	return nil
}

// CheckDirPaths checks that all directory paths in the config exist and are directories.
// Empty directory paths are ignored.
func (c *Config) CheckDirPaths() error {
	v := reflect.ValueOf(c).Elem()

	// Iterate through the fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		fieldValue := v.Field(i)

		if fieldValue.Kind() != reflect.String {
			continue
		}

		if tagValue, ok := field.Tag.Lookup("dirpath"); ok && tagValue == "true" {
			dirPath := fieldValue.String()
			if dirPath == "" {
				continue
			}
			if !fileExists(dirPath) {
				return fmt.Errorf("%s does not exist", dirPath)
			}
			if !isDir(dirPath) {
				return fmt.Errorf("%s is not a directory", dirPath)
			}
		}
	}
	return nil
}

type BuildInfo struct {
	Version string
	Commit  string
	Branch  string
}

// func initFlags(ko *koanf.Koanf) (*Config, error) {
func initFlags(name, desc string, build *BuildInfo) (*Config, error) {

	if pflag.Parsed() {
		return nil, fmt.Errorf("command-line flags already parsed")
	}

	config := &Config{}
	showVersion := false

	f := pflag.NewFlagSet("config", pflag.ExitOnError)

	f.String("port", "8080", "port to host the web server on")
	f.Bool("override", false, "override the command line arguments with the specified config file")

	// flags related to the raft consensus
	f.String("raft_addr", "localhost:10092", "address of the raft connection")
	f.String("http_addr", "localhost:8081", "address of the http server")
	f.String("raft_dir", "./raft_database", "address of the raft connection")
	f.String("node_id", "node0", "address of the raft connection")

	// Show version information
	f.BoolVar(&showVersion, "version", false, "Show version information and exit")

	// New configs
	f.StringSliceVar(&config.ConfigPath, "config", []string{".config/config.json"}, "path to one or more config files (will be merged in order)")
	f.StringVar(&config.NodeID, "node-id", "", "Unique ID for node. If not set, set to advertised Raft address")
	// New store related configs
	f.StringVar(&config.StoreDatabase, "store-db", "bbolt", "The backend database for the stable and the log store")

	// Raft configs
	f.StringVar(&config.DataPath, "raft-dir", "", "Raft directory")
	f.StringVar(&config.RaftAddr, RaftAddrFlag, "localhost:4002", "Raft communication bind address")
	f.StringVar(&config.RaftAdv, RaftAdvAddrFlag, "", "Advertised Raft communication address. If not set, same as Raft bind address")
	f.StringVar(&config.JoinAddrs, "join", "", "Comma-delimited list of nodes, in host:port form, through which a cluster can be joined")
	f.IntVar(&config.JoinAttempts, "join-attempts", 5, "Number of join attempts to make")
	f.DurationVar(&config.JoinInterval, "join-interval", 3*time.Second, "Period between join attempts")
	f.StringVar(&config.JoinAs, "join-as", "", "Username in authentication file to join as. If not set, joins anonymously")
	f.IntVar(&config.BootstrapExpect, "bootstrap-expect", 0, "Minimum number of nodes required for a bootstrap")
	f.DurationVar(&config.BootstrapExpectTimeout, "bootstrap-expect-timeout", 120*time.Second, "Maximum time for bootstrap process")
	f.BoolVar(&config.RaftNonVoter, "raft-non-voter", false, "Configure as non-voting node")
	f.DurationVar(&config.RaftHeartbeatTimeout, "raft-timeout", time.Second, "Raft heartbeat timeout")
	f.DurationVar(&config.RaftElectionTimeout, "raft-election-timeout", time.Second, "Raft election timeout")
	f.DurationVar(&config.RaftApplyTimeout, "raft-apply-timeout", 10*time.Second, "Raft apply timeout")
	f.Uint64Var(&config.RaftSnapThreshold, "raft-snap", 8192, "Number of outstanding log entries which triggers Raft snapshot")
	f.DurationVar(&config.RaftSnapInterval, "raft-snap-int", 10*time.Second, "Snapshot threshold check interval")
	f.DurationVar(&config.RaftLeaderLeaseTimeout, "raft-leader-lease-timeout", 0, "Raft leader lease timeout. Use 0s for Raft default")
	f.BoolVar(&config.RaftStepdownOnShutdown, "raft-shutdown-stepdown", true, "If leader, stepdown before shutting down. Enabled by default")
	f.BoolVar(&config.RaftShutdownOnRemove, "raft-remove-shutdown", false, "Shutdown Raft if node removed from cluster")
	f.BoolVar(&config.RaftClusterRemoveOnShutdown, "raft-cluster-remove-shutdown", false, "Node removes itself from cluster on graceful shutdown")
	f.StringVar(&config.RaftLogLevel, "raft-log-level", "DEBUG", "Minimum log level for Raft module")
	f.DurationVar(&config.RaftReapNodeTimeout, "raft-reap-node-timeout", 0*time.Hour, "Time after which a non-reachable voting node will be reaped. If not set, no reaping takes place")
	f.DurationVar(&config.RaftReapReadOnlyNodeTimeout, "raft-reap-read-only-node-timeout", 0*time.Hour, "Time after which a non-reachable non-voting node will be reaped. If not set, no reaping takes place")
	// Communication configs
	f.StringVar(&config.HTTPAddr, HTTPAddrFlag, "localhost:4001", "HTTP server bind address. To enable HTTPS, set X.509 certificate and key")
	f.StringVar(&config.HTTPAdv, HTTPAdvAddrFlag, "", "Advertised HTTP address. If not set, same as HTTP server bind address")
	f.StringVar(&config.HTTPAllowOrigin, "http-allow-origin", "", "Value to set for Access-Control-Allow-Origin HTTP header")
	f.StringVar(&config.HTTPx509CACert, "http-ca-cert", "", "Path to X.509 CA certificate for HTTPS")
	f.StringVar(&config.HTTPx509Cert, HTTPx509CertFlag, "", "Path to HTTPS X.509 certificate")
	f.StringVar(&config.HTTPx509Key, HTTPx509KeyFlag, "", "Path to HTTPS X.509 private key")
	f.BoolVar(&config.HTTPVerifyClient, "http-verify-client", false, "Enable mutual TLS for HTTPS")
	f.StringVar(&config.NodeX509CACert, "node-ca-cert", "", "Path to X.509 CA certificate for node-to-node encryption")
	f.StringVar(&config.NodeX509Cert, NodeX509CertFlag, "", "Path to X.509 certificate for node-to-node mutual authentication and encryption")
	f.StringVar(&config.NodeX509Key, NodeX509KeyFlag, "", "Path to X.509 private key for node-to-node mutual authentication and encryption")
	f.BoolVar(&config.NoNodeVerify, "node-no-verify", false, "Skip verification of any node-node certificate")
	f.BoolVar(&config.NodeVerifyClient, "node-verify-client", false, "Enable mutual TLS for node-to-node communication")
	f.StringVar(&config.NodeVerifyServerName, "node-verify-server-name", "", "Hostname to verify on certificate returned by a node")

	f.StringVar(&config.AuthFile, "auth", "", "Path to authentication and authorization file. If not set, not enabled")
	f.StringVar(&config.AutoBackupFile, "auto-backup", "", "Path to automatic backup configuration file. If not set, not enabled")
	f.StringVar(&config.AutoRestoreFile, "auto-restore", "", "Path to automatic restore configuration file. If not set, not enabled")
	f.DurationVar(&config.AutoVacInterval, "auto-vacuum-int", 0, "Period between automatic VACUUMs. It not set, not enabled")
	f.DurationVar(&config.AutoOptimizeInterval, "auto-optimize-int", mustParseDuration("24h"), `Period between automatic 'PRAGMA optimize'. Set to 0h to disable`)
	f.DurationVar(&config.ClusterConnectTimeout, "cluster-connect-timeout", 30*time.Second, "Timeout for initial connection to other nodes")
	f.IntVar(&config.WriteQueueCap, "write-queue-capacity", 1024, "QueuedWrites queue capacity")
	f.IntVar(&config.WriteQueueBatchSz, "write-queue-batch-size", 128, "QueuedWrites queue batch size")
	f.DurationVar(&config.WriteQueueTimeout, "write-queue-timeout", 50*time.Millisecond, "QueuedWrites queue timeout")
	f.BoolVar(&config.WriteQueueTx, "write-queue-tx", false, "Use a transaction when processing a queued write")
	// Stats configs
	f.StringVar(&config.CPUProfile, "cpu-profile", "", "Path to file for CPU profiling information")
	f.StringVar(&config.MemProfile, "mem-profile", "", "Path to file for memory profiling information")
	f.StringVar(&config.TraceProfile, "trace-profile", "", "Path to file for trace profiling information")
	// Misc configs
	f.BoolVar(&config.DebugMode, "debug", false, "run in debug mode - better logs")

	// WAL Configuration Flags
	// These provide command-line overrides for settings typically in a config file.
	// Using defaults from wal.DefaultWALConfig() as the flag defaults.
	defaultWALCfg := wal.DefaultWALConfig()
	f.BoolVar(&config.Pipeline.WAL.Enabled, "wal-enabled", defaultWALCfg.Enabled, "Enable Write-Ahead Log")
	f.StringVar(&config.Pipeline.WAL.Directory, "wal-directory", defaultWALCfg.Directory, "Directory for WAL segment files")
	f.Int64Var(&config.Pipeline.WAL.SegmentSize, "wal-segment-size", defaultWALCfg.SegmentSize, "Maximum size of a WAL segment file in bytes")
	f.IntVar(&config.Pipeline.WAL.MaxSegments, "wal-max-segments", defaultWALCfg.MaxSegments, "Maximum number of WAL segments to retain")
	f.DurationVar(&config.Pipeline.WAL.SyncInterval, "wal-sync-interval", defaultWALCfg.SyncInterval, "Interval for WAL fsync operations (0 for sync on every write if wal-flush-on-write is true)")
	f.StringVar(&config.Pipeline.WAL.Compression, "wal-compression", defaultWALCfg.Compression, "Compression type for WAL entries (none, snappy)")
	f.BoolVar(&config.Pipeline.WAL.FlushOnWrite, "wal-flush-on-write", defaultWALCfg.FlushOnWrite, "Flush WAL segment buffer on every write")


	// Not supported as now, will add them later
	// f.StringVar(&config.DiscoMode, "disco-mode", "", "Choose clustering discovery mode. If not set, no node discovery is performed")
	// f.StringVar(&config.DiscoKey, "disco-key", "wire", "Key prefix for cluster discovery service")
	// f.StringVar(&config.DiscoConfig, "disco-config", "", "Set discovery config, or path to cluster discovery config file")

	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n%s\n\n", desc)
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", name)
		f.PrintDefaults()
	}

	pflag.CommandLine.MarkHidden("help")

	if err := f.Parse(os.Args[1:]); err != nil {
		fmt.Printf("error when loading flags: %v\n", err)
	}

	if showVersion {
		msg := fmt.Sprintf("%s %s %s %s %s (commit %s, branch %s, compiler %s)",
			name, build.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(),
			build.Commit, build.Branch, runtime.Compiler)
		errorExit(0, msg)
	}

	// Ensure, if set explicitly, that reap times are not too low.
	f.Visit(func(f *pflag.Flag) {
		if f.Name == "raft-reap-node-timeout" || f.Name == "raft-reap-read-only-node-timeout" {
			d, err := time.ParseDuration(f.Value.String())
			if err != nil {
				errorExit(1, fmt.Sprintf("failed to parse duration: %s", err.Error()))
			}
			if d <= 0 {
				errorExit(1, fmt.Sprintf("-%s must be greater than 0", f.Name))
			}
		}
	})

	if err := config.Validate(); err != nil {
		errorExit(1, err.Error())
	}

	// Initialize koanf instance for config file loading
	k := koanf.New(".")

	// Load config files first (e.g., config.yaml, config.json)
	// The paths are taken from the --config flag, which defaults to .config/config.json
	// We should also try .config/config.yaml by default if it exists.
	// For simplicity, let's assume config.ConfigPath contains the paths to check.
	// This part needs to be robust. If multiple config files are provided, they are merged.

	// Add a common default YAML path if not explicitly given via --config
	// and if the default JSON doesn't exist but YAML does.
	// This logic can be improved. For now, just process what's in config.ConfigPath.
	loadedFromFile := false
	for _, cfgPath := range config.ConfigPath {
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			// If default path like ".config/config.json" doesn't exist, try ".config/config.yaml"
			if strings.HasSuffix(cfgPath, ".json") {
				yamlPath := strings.TrimSuffix(cfgPath, ".json") + ".yaml"
				if _, errYaml := os.Stat(yamlPath); !os.IsNotExist(errYaml) {
					cfgPath = yamlPath // Use YAML path instead
				} else {
					continue // Neither default JSON nor YAML exists, skip.
				}
			} else {
				continue // File doesn't exist, skip.
			}
		}

		var p koanf.Parser
		fileExt := filepath.Ext(cfgPath)
		switch fileExt {
		case ".yaml", ".yml":
			p = yaml.Parser()
		case ".json":
			p = json.Parser()
		default:
			fmt.Fprintf(os.Stderr, "warning: unsupported config file extension %s for %s, skipping\n", fileExt, cfgPath)
			continue
		}

		fmt.Fprintf(os.Stdout, "info: loading configuration from %s\n", cfgPath)
		if err := k.Load(file.Provider(cfgPath), p); err != nil {
			// Don't make it fatal if a config file is missing, flags can still be used.
			// But log it as a warning.
			fmt.Fprintf(os.Stderr, "warning: error loading config file %s: %v\n", cfgPath, err)
		} else {
			loadedFromFile = true
		}
	}

	// Unmarshal the loaded configuration into the Config struct.
	// This will populate fields based on the config file content.
	// Fields already set by flags (if flags were parsed into `config` before this)
	// might be overwritten if koanf's unmarshal options are set to do so,
	// or respected if koanf merges. Default is usually overwrite.
	// For WAL, we want file to take precedence, then flags as overrides.
	// The current pflag setup parses flags directly into `config`.
	// So, we unmarshal from koanf first, then pflag values will effectively override.
	// To have file values override initial defaults and flags override file values:
	// 1. Create `config` with Go defaults.
	// 2. Load from file into `config` (overwrites Go defaults).
	// 3. Parse flags into `config` (overwrites file values for flags that were set).

	// Let's re-evaluate the order.
	// The pflag package sets values in `config` directly when f.Parse() is called.
	// So, if we want file settings to be primary and flags to be overrides:
	// A. Create `config` with initial Go defaults (e.g. from wal.DefaultWALConfig for WAL part).
	// B. Parse flags into `config`. This sets values based on flag defaults or user-provided flags.
	// C. Load config file into Koanf (`k`).
	// D. Unmarshal `k` into `config`. This will overwrite values in `config` set by flags IF the keys exist in the file.
	// This means file takes precedence over flag defaults, but command-line provided flags (which changed from default)
	// would be overwritten by file if key is present. This is often desired.

	// If we want flags to ALWAYS override file:
	// 1. Load file into Koanf `k`.
	// 2. Unmarshal `k` into `config`.
	// 3. Parse flags (which will then overwrite any values in `config` that were set from the file).
	// The current code calls f.Parse() early. Let's stick to that and assume flags are overrides.
	// So, after f.Parse(), `config` has values from flags (or flag defaults).
	// Now, we need to load from file and merge carefully if we want file to take precedence for non-set flags.
	// Simplest: if koanf loads, it sets values. If flags were set, they are already in `config`.
	// We need to unmarshal from koanf into a *temporary* struct or map, then merge into `config`
	// only for fields that were NOT set by flags. This is complex.

	// Standard approach: Load files into koanf, then load flags into koanf, then unmarshal once.
	// ko.Load(posflag.Provider(f, ".", k), nil) // This would make flags override file.
	// Let's try that. The `f` flagset is already parsed. We need a new flagset for Koanf provider.

	// For now: The flags have already set their values in `config`.
	// If a config file was loaded by Koanf, we unmarshal it. This will overwrite flag-set values
	// if the keys are present in the file. This means file takes precedence.
	if loadedFromFile {
		if err := k.UnmarshalWithConf("", config, koanf. 영향UnmarshalConf{Tag: "koanf"}); err != nil {
			fmt.Fprintf(os.Stderr, "error unmarshalling config from file: %v\n", err)
			// Not returning error here, as flags might still provide a valid config.
		}
	}

	// Re-validate after potential changes from config file.
	// Especially for WAL.Directory etc. which might now be set.
	if err := config.Validate(); err != nil {
		errorExit(1, fmt.Sprintf("post-config-file validation error: %s", err.Error()))
	}


	return config, nil
}

// func initConfig(cfg *Config, ko *koanf.Koanf) error {
// 	log.Info().Msg("Loading configs")
// 	for _, f := range ko.Strings("config") {
// 		log.Debug().Msgf("Reading config from %s", f)
// 		var parser koanf.Parser
// 		fileExtension := f[strings.LastIndex(f, ".")+1:]
// 		switch fileExtension {
// 		case "yaml":
// 			parser = yaml.Parser()
// 		case "json":
// 			parser = json.Parser()
// 		default:
// 			return fmt.Errorf("unsupported file extension")
// 		}
// 		log.Debug().Msgf("The config is: %v", ko.All())
// 		if err := ko.Load(file.Provider(f), parser); err != nil {
// 			log.Fatal().Msgf("error reading config: %v", err)
// 		} else {
// 			log.Trace().Msg("Successfully read the contents of the config file")
// 		}
// 	}
// 	return nil
// }

func errorExit(code int, msg string) {
	if code != 0 {
		fmt.Fprintf(os.Stderr, "fatal: ")
	}
	fmt.Fprintf(os.Stderr, "%s\n", msg)
	os.Exit(code)
}

// bothUnsetSet returns true if both a and b are unset, or both are set.
func bothUnsetSet(a, b string) bool {
	return (a == "" && b == "") || (a != "" && b != "")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}

func mustParseDuration(d string) time.Duration {
	td, err := time.ParseDuration(d)
	if err != nil {
		panic(err)
	}
	return td
}
