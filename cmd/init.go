package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/pflag"
)

// BuildInfo holds version metadata populated at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Branch  string
}

type cliOptions struct {
	configPaths []string
	showVersion bool
}

func initFlags(name, desc string) (*cliOptions, *pflag.FlagSet, error) {
	opts := &cliOptions{}

	f := pflag.NewFlagSet(name, pflag.ContinueOnError)
	f.SetOutput(os.Stderr)

	// Values for config-backed flags are intentionally local sinks. After
	// parsing, config.ApplyFlags reads changed values from the FlagSet and
	// overlays them onto config.WireConfig.
	var (
		debugMode          bool
		mode               string
		listenAddr         string
		nodeCert           string
		nodeKey            string
		nodeCA             string
		nodeVerifyClient   bool
		coordinatorDataDir string
		coordinatorNodeID  string
		httpListenAddr     string
		electionBackend    string
		electionLockPath   string
		coordinatorAddr    string
		workerID           string
		workerListenAddr   string
		taskSlots          int
		metricsEnabled     bool
		metricsAddr        string
	)

	// Show version information.
	f.BoolVar(&opts.showVersion, "version", false, "show version information and exit")

	// Config file.
	f.StringSliceVar(&opts.configPaths, "config", []string{".config/config.json"}, "path to one or more config files (will be merged in order)")

	// Misc configs.
	f.BoolVar(&debugMode, "debug", false, "run in debug mode - better logs")
	f.StringVar(&mode, "mode", "coordinator", "operating mode: coordinator or worker")

	// Transport flags.
	f.StringVar(&listenAddr, "listen", ":4002", "wire protocol listen address")
	f.StringVar(&nodeCert, "node-cert", "", "TLS certificate file")
	f.StringVar(&nodeKey, "node-key", "", "TLS private key file")
	f.StringVar(&nodeCA, "node-ca", "", "CA certificate for peer verification")
	f.BoolVar(&nodeVerifyClient, "node-verify-client", false, "require mutual TLS")

	// Coordinator flags.
	f.StringVar(&coordinatorDataDir, "coordinator-data-dir", "data/coordinator", "coordinator metadata storage directory")
	f.StringVar(&coordinatorNodeID, "node-id", "", "coordinator node ID (defaults to hostname)")
	f.StringVar(&httpListenAddr, "http-listen", ":4001", "HTTP API listen address")
	f.StringVar(&electionBackend, "election-backend", "noop", "leader election backend (noop, filelock)")
	f.StringVar(&electionLockPath, "election-lock-path", "data/coordinator/leader.lock", "file path for filelock election backend")

	// Worker flags.
	f.StringVar(&coordinatorAddr, "coordinator-addr", "", "coordinator address to connect to (worker mode)")
	f.StringVar(&workerID, "worker-id", "", "worker node ID (defaults to hostname)")
	f.StringVar(&workerListenAddr, "worker-listen", ":4003", "worker data-plane listen address")
	f.IntVar(&taskSlots, "task-slots", 4, "number of task slots (worker mode)")

	// Observability flags.
	f.BoolVar(&metricsEnabled, "metrics-enabled", true, "expose Prometheus /metrics scrape endpoint")
	f.StringVar(&metricsAddr, "metrics-addr", ":9090", "bind address for the Prometheus /metrics scrape endpoint")

	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n%s\n\n", desc)
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", name)
		f.PrintDefaults()
	}

	if err := f.Parse(os.Args[1:]); err != nil {
		return nil, nil, err
	}

	return opts, f, nil
}

func versionString(name string, build *BuildInfo) string {
	return fmt.Sprintf("%s %s %s %s %s (commit %s, branch %s, compiler %s)",
		name, build.Version, runtime.GOOS, runtime.GOARCH, runtime.Version(),
		build.Commit, build.Branch, runtime.Compiler)
}
