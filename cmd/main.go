package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/cmd"
	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/logger"
	"golang.org/x/sync/errgroup"
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
	defer logFile.Close()

	cfg, err := initFlags(name, desc, &BuildInfo{
		Version: cmd.Version,
		Commit:  cmd.Commit,
		Branch:  cmd.Branch,
	})
	if err != nil {
		fmt.Printf("failed to parse command-line flags: %s", err.Error())
	}
	fmt.Print(logo)

	logger.SetDevelopment(cfg.DebugMode)
	logger.SetLogFile(logFile)

	log.Logger = logger.GetLogger("main")

	if cfg.DebugMode {
		log.Debug().Msgf("PID: %v | PPID: %v", os.Getpid(), os.Getppid())
	}

	log.Info().Msg("Starting wire...")

	// Resolve coordinator node ID.
	nodeID := cfg.CoordinatorNodeID
	if nodeID == "" {
		nodeID, _ = os.Hostname()
		if nodeID == "" {
			nodeID = "wire-node-1"
		}
	}

	// Create metadata store (PebbleDB).
	store, err := coordinator.NewPebbleStore(cfg.CoordinatorDataDir)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open coordinator metadata store")
	}
	defer store.Close()

	// Create leader election backend.
	var election coordinator.LeaderElection
	switch cfg.ElectionBackend {
	case "filelock":
		election = coordinator.NewFileLockElection(cfg.ElectionLockPath, cfg.HTTPListenAddr)
	case "noop", "":
		// Single-node mode: no election needed.
	default:
		log.Fatal().Str("backend", cfg.ElectionBackend).Msg("unknown election backend")
	}

	// Create coordinator.
	coordCfg := coordinator.CoordinatorConfig{
		DataDir:    cfg.CoordinatorDataDir,
		NodeID:     nodeID,
		ListenAddr: cfg.HTTPListenAddr,
	}
	coord := coordinator.New(coordCfg, store, election, log.Logger)

	// Create HTTP server.
	httpSrv := coordinator.NewHTTPServer(coord, cfg.HTTPListenAddr, log.Logger)

	// Start everything in an errgroup.
	g, gCtx := errgroup.WithContext(mainCtx)

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
		<-gCtx.Done()
		log.Info().Msg("Shutting down...")
		coord.Shutdown(context.Background())
		httpSrv.Shutdown(context.Background())
		return nil
	})

	if err := g.Wait(); err != nil && err != context.Canceled {
		log.Fatal().Err(err).Msg("wire exited with error")
	}

	log.Info().Msg("Shutting down.")
}
