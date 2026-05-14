package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/cmd"
	"github.com/tarungka/wire/internal/config"
	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/observability"
	"github.com/tarungka/wire/internal/worker"
)

const (
	name = "wire"
	desc = `Wire is a powerful, distributed stream processing platform designed to handle real-time data flows with exceptional efficiency. Engineered for scalability and performance, Wire simplifies stream processing, enabling seamless, fault-tolerant data pipelines for even the most demanding workloads.

Visit https://www.github.com/tarungka/wire to learn more.`

	logFilePath           = "server.log"
	shutdownTimeout       = 5 * time.Second
	fallbackCoordinatorID = "wire-node-1"
	fallbackWorkerID      = "wire-worker-1"

	modeCoordinator = "coordinator"
	modeWorker      = "worker"

	electionFileLock = "filelock"
	electionNoop     = "noop"
)

const logo = `
 __      ___________________________
/  \    /  \   \______   \_   _____/
\   \/\/   /   ||       _/|    __)_    Seamless Streaming for
 \        /|   ||    |   \|        \   Dynamic Workloads.
  \__/\  / |___||____|_  /_______  /   www.github.com/tarungka/wire
       \/              \/        \/
`

func main() {
	if err := run(); err != nil {
		log.Fatal().Err(err).Msg("wire exited with error")
	}
}

func run() error {
	build := &BuildInfo{
		Version: cmd.Version,
		Commit:  cmd.Commit,
		Branch:  cmd.Branch,
	}

	cliOpts, flagSet, err := initFlags(name, desc)
	if err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse flags: %w", err)
	}
	if cliOpts.showVersion {
		fmt.Fprintln(os.Stdout, versionString(name, build))
		return nil
	}

	fmt.Print(logo)

	wireCfg, err := config.Load(cliOpts.configPaths)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := config.ApplyFlags(&wireCfg, flagSet); err != nil {
		return fmt.Errorf("apply flag overrides: %w", err)
	}
	if err := wireCfg.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	logFile := openLogFile(logFilePath)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	logger.SetDevelopment(wireCfg.Node.Debug)
	logger.SetLogFile(logFile)
	appLog := logger.GetLogger("main")

	ctx, stopSignals := signalContext(context.Background(), appLog, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	defer stopSignals()

	if wireCfg.Node.Debug {
		appLog.Debug().Int("pid", os.Getpid()).Int("ppid", os.Getppid()).Msg("debug mode")
	}

	nodeID := resolveRuntimeNodeID(&wireCfg)
	shutdownObs, err := initObservability(ctx, appLog, &wireCfg, nodeID)
	if err != nil {
		return fmt.Errorf("init observability: %w", err)
	}
	defer shutdownObs()

	appLog.Info().Str("mode", wireCfg.Mode).Msg("Starting wire...")

	runErr := dispatch(ctx, appLog, &wireCfg)
	if errors.Is(runErr, context.Canceled) {
		return nil
	}
	return runErr
}

func dispatch(ctx context.Context, appLog zerolog.Logger, wireCfg *config.WireConfig) error {
	switch wireCfg.Mode {
	case modeWorker:
		return runWorker(ctx, appLog, wireCfg)
	case modeCoordinator, "":
		return runCoordinator(ctx, appLog, wireCfg)
	default:
		return fmt.Errorf("unsupported mode %q", wireCfg.Mode)
	}
}

func runCoordinator(ctx context.Context, appLog zerolog.Logger, wireCfg *config.WireConfig) error {
	nodeID := resolveCoordinatorNodeID(wireCfg.Node.ID)
	advertiseAddr := coordinatorAdvertiseAddr(wireCfg.HTTP)

	store, err := coordinator.NewPebbleStore(wireCfg.Node.DataDir)
	if err != nil {
		return fmt.Errorf("open coordinator metadata store: %w", err)
	}
	defer func() { _ = store.Close() }()

	election, err := newLeaderElection(wireCfg.Election, advertiseAddr)
	if err != nil {
		return err
	}

	coord := coordinator.New(coordinator.CoordinatorConfig{
		DataDir:    wireCfg.Node.DataDir,
		NodeID:     nodeID,
		ListenAddr: advertiseAddr,
	}, store, election, appLog)

	httpSrv := coordinator.NewHTTPServer(coord, wireCfg.HTTP.Addr, appLog)
	transportSrv := coordinator.NewTransportServer(coord, wireCfg.Listen, appLog)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error { return coord.Run(gCtx) })

	g.Go(func() error {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})

	g.Go(func() error { return transportSrv.ListenAndServe(gCtx) })

	g.Go(func() error {
		<-gCtx.Done()
		appLog.Info().Msg("Shutting down coordinator...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		logShutdownError(appLog, "coordinator", coord.Shutdown(shutdownCtx))
		logShutdownError(appLog, "http server", httpSrv.Shutdown(shutdownCtx))
		logShutdownError(appLog, "transport server", transportSrv.Shutdown(shutdownCtx))
		return nil
	})

	return g.Wait()
}

func runWorker(ctx context.Context, appLog zerolog.Logger, wireCfg *config.WireConfig) error {
	w := worker.New(worker.Config{
		WorkerID:        wireCfg.Worker.WorkerID,
		CoordinatorAddr: wireCfg.Worker.CoordinatorAddr,
		ListenAddr:      wireCfg.Worker.ListenAddr,
		TaskSlots:       wireCfg.Worker.TaskSlots,
	}, appLog)

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error { return w.Run(gCtx) })

	g.Go(func() error {
		<-gCtx.Done()
		appLog.Info().Msg("Shutting down worker...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return w.Shutdown(shutdownCtx)
	})

	return g.Wait()
}

func newLeaderElection(cfg config.ElectionConfig, advertiseAddr string) (coordinator.LeaderElection, error) {
	switch cfg.Backend {
	case electionFileLock:
		return coordinator.NewFileLockElection(cfg.LockPath, advertiseAddr), nil
	case electionNoop, "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown election backend %q", cfg.Backend)
	}
}

func resolveCoordinatorNodeID(configured string) string {
	if configured != "" {
		return configured
	}
	if hostname, _ := os.Hostname(); hostname != "" {
		return hostname
	}
	return fallbackCoordinatorID
}

func resolveRuntimeNodeID(wireCfg *config.WireConfig) string {
	if wireCfg.Mode == modeWorker {
		if wireCfg.Worker.WorkerID != "" {
			return wireCfg.Worker.WorkerID
		}
		if hostname, _ := os.Hostname(); hostname != "" {
			return hostname
		}
		return fallbackWorkerID
	}
	return resolveCoordinatorNodeID(wireCfg.Node.ID)
}

func coordinatorAdvertiseAddr(cfg config.HTTPConfig) string {
	if cfg.AdvAddr != "" {
		return cfg.AdvAddr
	}
	return cfg.Addr
}

func logShutdownError(appLog zerolog.Logger, component string, err error) {
	if err != nil {
		appLog.Warn().Err(err).Str("component", component).Msg("shutdown error")
	}
}

func openLogFile(path string) *os.File {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to open log file %q: %v\n", path, err)
		return nil
	}
	return f
}

func initObservability(ctx context.Context, appLog zerolog.Logger, wireCfg *config.WireConfig, nodeID string) (func(), error) {
	shutdown, err := observability.Init(ctx, observability.Config{
		Enabled:        wireCfg.Observability.Enabled,
		ServiceName:    "wire-" + wireCfg.Mode,
		ServiceVersion: cmd.Version,
		NodeID:         nodeID,
		MetricsAddr:    wireCfg.Observability.MetricsAddr,
	}, appLog)
	if err != nil {
		return nil, err
	}
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := shutdown(shutdownCtx); err != nil {
			appLog.Warn().Err(err).Msg("observability shutdown error")
		}
	}, nil
}
