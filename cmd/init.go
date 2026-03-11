package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/pflag"
)

// Config represents the configuration as set by command-line flags.
type Config struct {
	// ConfigPath is the path to the config file. May not be set.
	ConfigPath []string

	// DebugMode enables additional logs and other metadata to be printed.
	DebugMode bool

	// Mode selects the operating mode: "coordinator" or "worker".
	Mode string

	// ListenAddr is the wire protocol listen address.
	ListenAddr string

	// NodeCert is the path to the TLS certificate file.
	NodeCert string

	// NodeKey is the path to the TLS private key file.
	NodeKey string

	// NodeCA is the path to the CA certificate for peer verification.
	NodeCA string

	// NodeVerifyClient enables mutual TLS (require client certificates).
	NodeVerifyClient bool

	// MaxFrameSize is the maximum wire protocol frame size in bytes.
	MaxFrameSize uint32

	// CoordinatorDataDir is the directory for coordinator metadata (PebbleDB).
	CoordinatorDataDir string

	// CoordinatorNodeID is the unique identifier for this coordinator node.
	CoordinatorNodeID string

	// HTTPListenAddr is the HTTP API listen address.
	HTTPListenAddr string

	// ElectionBackend selects the leader election backend ("noop" or "filelock").
	ElectionBackend string

	// ElectionLockPath is the path to the lock file for the filelock election backend.
	ElectionLockPath string

	// CoordinatorAddr is the coordinator address for worker mode.
	CoordinatorAddr string

	// WorkerID is the unique identifier for this worker node.
	WorkerID string

	// WorkerListenAddr is the worker's data-plane listen address.
	WorkerListenAddr string

	// TaskSlots is the number of task slots available on this worker.
	TaskSlots int
}

// BuildInfo holds version metadata populated at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Branch  string
}

func initFlags(name, desc string, build *BuildInfo) (*Config, *pflag.FlagSet, error) {

	if pflag.Parsed() {
		return nil, nil, fmt.Errorf("command-line flags already parsed")
	}

	config := &Config{}
	showVersion := false

	f := pflag.NewFlagSet("config", pflag.ExitOnError)

	// Show version information
	f.BoolVar(&showVersion, "version", false, "Show version information and exit")

	// Config file
	f.StringSliceVar(&config.ConfigPath, "config", []string{".config/config.json"}, "path to one or more config files (will be merged in order)")

	// Misc configs
	f.BoolVar(&config.DebugMode, "debug", false, "run in debug mode - better logs")
	f.StringVar(&config.Mode, "mode", "coordinator", "operating mode: coordinator or worker")

	// Transport flags
	f.StringVar(&config.ListenAddr, "listen", ":4002", "wire protocol listen address")
	f.StringVar(&config.NodeCert, "node-cert", "", "TLS certificate file")
	f.StringVar(&config.NodeKey, "node-key", "", "TLS private key file")
	f.StringVar(&config.NodeCA, "node-ca", "", "CA certificate for peer verification")
	f.BoolVar(&config.NodeVerifyClient, "node-verify-client", false, "require mutual TLS")
	f.Uint32Var(&config.MaxFrameSize, "max-frame-size", 16777216, "max wire protocol frame size")

	// Coordinator flags
	f.StringVar(&config.CoordinatorDataDir, "coordinator-data-dir", "data/coordinator", "coordinator metadata storage directory")
	f.StringVar(&config.CoordinatorNodeID, "node-id", "", "coordinator node ID (defaults to hostname)")
	f.StringVar(&config.HTTPListenAddr, "http-listen", ":4001", "HTTP API listen address")
	f.StringVar(&config.ElectionBackend, "election-backend", "noop", "leader election backend (noop, filelock)")
	f.StringVar(&config.ElectionLockPath, "election-lock-path", "data/coordinator/leader.lock", "file path for filelock election backend")

	// Worker flags
	f.StringVar(&config.CoordinatorAddr, "coordinator-addr", "", "coordinator address to connect to (worker mode)")
	f.StringVar(&config.WorkerID, "worker-id", "", "worker node ID (defaults to hostname)")
	f.StringVar(&config.WorkerListenAddr, "worker-listen", ":4003", "worker data-plane listen address")
	f.IntVar(&config.TaskSlots, "task-slots", 4, "number of task slots (worker mode)")

	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n%s\n\n", desc)
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", name)
		f.PrintDefaults()
	}

	_ = pflag.CommandLine.MarkHidden("help")

	if err := f.Parse(os.Args[1:]); err != nil {
		fmt.Printf("error when loading flags: %v\n", err)
	}

	if showVersion {
		msg := fmt.Sprintf("%s %s %s %s %s (commit %s, branch %s, compiler %s)",
			name, build.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(),
			build.Commit, build.Branch, runtime.Compiler)
		errorExit(0, msg)
	}

	return config, f, nil
}

func errorExit(code int, msg string) {
	if code != 0 {
		fmt.Fprintf(os.Stderr, "fatal: ")
	}
	fmt.Fprintf(os.Stderr, "%s\n", msg)
	os.Exit(code)
}
