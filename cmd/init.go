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
}

// BuildInfo holds version metadata populated at build time.
type BuildInfo struct {
	Version string
	Commit  string
	Branch  string
}

func initFlags(name, desc string, build *BuildInfo) (*Config, error) {

	if pflag.Parsed() {
		return nil, fmt.Errorf("command-line flags already parsed")
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

	// Transport flags
	f.StringVar(&config.ListenAddr, "listen", ":4002", "wire protocol listen address")
	f.StringVar(&config.NodeCert, "node-cert", "", "TLS certificate file")
	f.StringVar(&config.NodeKey, "node-key", "", "TLS private key file")
	f.StringVar(&config.NodeCA, "node-ca", "", "CA certificate for peer verification")
	f.BoolVar(&config.NodeVerifyClient, "node-verify-client", false, "require mutual TLS")
	f.Uint32Var(&config.MaxFrameSize, "max-frame-size", 16777216, "max wire protocol frame size")

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

	return config, nil
}

func errorExit(code int, msg string) {
	if code != 0 {
		fmt.Fprintf(os.Stderr, "fatal: ")
	}
	fmt.Fprintf(os.Stderr, "%s\n", msg)
	os.Exit(code)
}
