package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/cmd"
	"github.com/tarungka/wire/internal/config"
	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/worker"
)

// Need to make up my mind on some of these:
// The high-performance, distributed stream processing platform.
// Seamless Streaming for Dynamic Workloads.
// There is a new line at the start of this logo

const logo = `
 __      ___________________________
/  \    /  \   \______   \_   _____/
\   \/\/   /   ||       _/|    __)_    Seamless Streaming for
 \        /|   ||    |   \|        \   Dynamic Workloads.
  \__/\  / |___||____|_  /_______  /   www.github.com/tarungka/wire
       \/              \/        \/
`

const name = `wire`
const desc = `Wire is a powerful, distributed stream processing platform designed to handle real-time data flows with exceptional efficiency. Engineered for scalability and performance, Wire simplifies stream processing, enabling seamless, fault-tolerant data pipelines for even the most demanding workloads.

Visit https://www.github.com/tarungka/wire to learn more.`

func main() {

	// Handle signals first, so signal handling is established before anything else.
	sigCh := HandleSignals(syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	// Main context
	mainCtx, mainCancel := CreateContext(sigCh)
	defer mainCancel()

	// Setup logging
	// logs will be written to both server.log and stdout
	logFile, err := os.OpenFile("server.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("failed to create log file")
	}
	defer func() { _ = logFile.Close() }()

	cliCfg, flagSet, err := initFlags(name, desc, &BuildInfo{
		Version: cmd.Version,
		Commit:  cmd.Commit,
		Branch:  cmd.Branch,
	})
	if err != nil {
		fmt.Printf("failed to parse command-line flags: %s", err.Error())
	}
	fmt.Print(logo)

	// Load config files, apply CLI flag overrides, and validate.
	wireCfg, err := config.Load(cliCfg.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
	if err := config.ApplyFlags(&wireCfg, flagSet); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
	if err := wireCfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}

	logger.SetDevelopment(wireCfg.Node.Debug)
	logger.SetLogFile(logFile)

	log.Logger = logger.GetLogger("main")

	if wireCfg.Node.Debug {
		log.Debug().Msgf("PID: %v | PPID: %v", os.Getpid(), os.Getppid())
	}

	if cliCfg.ProfileExtra {
		runtime.SetMutexProfileFraction(1)
		runtime.SetBlockProfileRate(1)
		log.Info().Msg("mutex and block contention profiling enabled (full rate)")
	}

	log.Info().Msg("Starting wire...")

	var runErr error
	switch wireCfg.Mode {
	case "worker":
		runErr = runWorker(mainCtx, &wireCfg, log.Logger)
	default:
		runErr = runCoordinator(mainCtx, &wireCfg, log.Logger)
	}

	if runErr != nil && runErr != context.Canceled {
		log.Fatal().Err(runErr).Msg("wire exited with error")
	}

	log.Info().Msg("Shutting down.")
}

func runCoordinator(ctx context.Context, wireCfg *config.WireConfig, _ zerolog.Logger) error {
	// Resolve coordinator node ID.
	nodeID := wireCfg.Node.ID
	if nodeID == "" {
		nodeID, _ = os.Hostname()
		if nodeID == "" {
			nodeID = "wire-node-1"
		}
	}

	// Create metadata store (PebbleDB).
	store, err := coordinator.NewPebbleStore(wireCfg.Node.DataDir)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open coordinator metadata store")
	}
	defer func() { _ = store.Close() }()

	// Create leader election backend.
	var election coordinator.LeaderElection
	switch wireCfg.Election.Backend {
	case "filelock":
		election = coordinator.NewFileLockElection(wireCfg.Election.LockPath, wireCfg.HTTP.Addr)
	case "noop", "":
		// Single-node mode: no election needed.
	default:
		log.Fatal().Str("backend", wireCfg.Election.Backend).Msg("unknown election backend")
	}

	// Create coordinator.
	coordCfg := coordinator.CoordinatorConfig{
		DataDir:    wireCfg.Node.DataDir,
		NodeID:     nodeID,
		ListenAddr: wireCfg.HTTP.Addr,
	}
	coord := coordinator.New(coordCfg, store, election, log.Logger)

	// Create HTTP server.
	httpSrv := coordinator.NewHTTPServer(coord, wireCfg.HTTP.Addr, log.Logger)

	// Create transport server for worker RPC connections.
	transportSrv := coordinator.NewTransportServer(coord, wireCfg.Listen, log.Logger)

	// Start everything in an errgroup.
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return coord.Run(gCtx)
	})

	g.Go(func() error {
		err := httpSrv.ListenAndServe()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	})

	g.Go(func() error {
		return transportSrv.ListenAndServe(gCtx)
	})

	g.Go(func() error {
		<-gCtx.Done()
		log.Info().Msg("Shutting down...")
		_ = coord.Shutdown(context.Background())
		_ = httpSrv.Shutdown(context.Background())
		_ = transportSrv.Shutdown(context.Background())
		return nil
	})

	return g.Wait()
}

func runWorker(ctx context.Context, wireCfg *config.WireConfig, _ zerolog.Logger) error {
	w := worker.New(worker.Config{
		WorkerID:        wireCfg.Worker.WorkerID,
		CoordinatorAddr: wireCfg.Worker.CoordinatorAddr,
		ListenAddr:      wireCfg.Worker.ListenAddr,
		TaskSlots:       wireCfg.Worker.TaskSlots,
	}, log.Logger)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return w.Run(gCtx)
	})

	g.Go(func() error {
		<-gCtx.Done()
		log.Info().Msg("Shutting down worker...")
		return w.Shutdown(context.Background())
	})

	return g.Wait()
}
