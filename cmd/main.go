package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	_ "go.uber.org/automaxprocs" // Apply Linux CPU quotas before starting task goroutines.
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/apiclient"
	"github.com/tarungka/wire/internal/cmd"
	"github.com/tarungka/wire/internal/config"
	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/jobcli"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/observability"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
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
	if len(os.Args) > 1 && (os.Args[1] == "jobs" || os.Args[1] == "savepoints" || os.Args[1] == "cluster") {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := jobcli.RunWithPipelineCompiler(ctx, os.Args[1:], os.Stdout, os.Stderr, compileYAMLPipeline); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

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

	// Initialize observability (OTel meter provider + Prometheus scrape
	// endpoint). Safe to call when --metrics-enabled=false; falls back to
	// a no-op meter so call sites stay clean.
	obsShutdown, err := observability.Init(mainCtx, observability.Config{
		Enabled:        cliCfg.MetricsEnabled,
		ServiceName:    "wire-" + wireCfg.Mode,
		ServiceVersion: cmd.Version,
		NodeID:         wireCfg.Node.ID,
		MetricsAddr:    cliCfg.MetricsAddr,
	}, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize observability")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := obsShutdown(shutdownCtx); err != nil {
			log.Warn().Err(err).Msg("observability shutdown error")
		}
	}()

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

	// Create leader election backend.
	var election coordinator.LeaderElection
	switch wireCfg.Election.Backend {
	case "filelock":
		httpAddr := wireCfg.HTTP.AdvAddr
		if httpAddr == "" {
			httpAddr = wireCfg.HTTP.Addr
		}
		election = coordinator.NewFileLockElection(wireCfg.Election.LockPath, httpAddr)
	case "kubernetes":
		cfg := wireCfg.Election.Kubernetes
		backend, err := coordinator.NewKubernetesLeaseElection(coordinator.KubernetesLeaseConfig{APIServer: cfg.APIServer, Namespace: cfg.Namespace, LeaseName: cfg.LeaseName, TokenFile: cfg.TokenFile, CAFile: cfg.CAFile, LeaseDuration: cfg.LeaseDuration.Duration, RenewDeadline: cfg.RenewDeadline.Duration, RetryPeriod: cfg.RetryPeriod.Duration})
		if err != nil {
			return err
		}
		election = backend
	case "noop", "":
		// Single-node mode: no election needed.
	default:
		log.Fatal().Str("backend", wireCfg.Election.Backend).Msg("unknown election backend")
	}

	// Create coordinator.
	coordCfg := coordinator.CoordinatorConfig{
		DefaultStateBackend:              &rpc.StateBackendSpec{Type: wireCfg.State.DefaultBackend, DataDir: wireCfg.State.Pebble.DataDir, MaxMemoryBytes: wireCfg.State.HashMap.MaxMemoryMB * 1024 * 1024},
		WorkerTimeout:                    wireCfg.Heartbeat.Timeout.Duration,
		HeartbeatInterval:                wireCfg.Heartbeat.Interval.Duration,
		CheckpointTimeout:                wireCfg.Checkpoint.Timeout.Duration,
		CheckpointMinPause:               wireCfg.Checkpoint.MinPause.Duration,
		CheckpointMaxConsecutiveFailures: wireCfg.Checkpoint.MaxConsecutiveFailures,
		CheckpointTolerableFailureRate:   wireCfg.Checkpoint.TolerableFailureRate,
		DataDir:                          wireCfg.Node.DataDir,
		NodeID:                           nodeID,
		ListenAddr:                       wireCfg.HTTP.Addr,
		RPCAdvertiseAddr:                 wireCfg.Node.RPCAdvertiseAddr,
		HTTPAdvertiseAddr:                wireCfg.HTTP.AdvAddr,
	}
	if coordCfg.RPCAdvertiseAddr == "" {
		coordCfg.RPCAdvertiseAddr = wireCfg.Listen
	}
	rpcTLS, err := coordinatorRPCTLS(wireCfg.NodeTLS)
	if err != nil {
		return err
	}
	// Load configured HTTPS credentials before starting any server.
	var httpTLS *tls.Config
	if wireCfg.HTTP.TLS.Cert != "" || wireCfg.HTTP.TLS.Key != "" || wireCfg.HTTP.TLS.VerifyClient || wireCfg.HTTP.TLS.CACert != "" {
		var err error
		httpTLS, err = transport.LoadTLSConfig(wireCfg.HTTP.TLS.Cert, wireCfg.HTTP.TLS.Key, wireCfg.HTTP.TLS.VerifyClient, wireCfg.HTTP.TLS.CACert)
		if err != nil {
			return fmt.Errorf("HTTP TLS: %w", err)
		}
	}
	if election != nil {
		service := coordinator.NewHAService(coordCfg, wireCfg.Listen, election, func() (coordinator.MetadataStore, error) {
			return coordinator.NewPebbleStore(wireCfg.Node.DataDir)
		}, rpcTLS, log.Logger)
		if err := service.ConfigureHTTP(httpTLS, wireCfg.Auth.File); err != nil {
			return fmt.Errorf("HA HTTP security: %w", err)
		}
		return service.Run(ctx)
	}
	store, err := coordinator.NewPebbleStore(wireCfg.Node.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	coord := coordinator.New(coordCfg, store, election, log.Logger)

	httpSrv := coordinator.NewHTTPServer(coord, wireCfg.HTTP.Addr, log.Logger, httpTLS)
	if err := httpSrv.ConfigureAuth(wireCfg.Auth.File); err != nil {
		return fmt.Errorf("HTTP authentication: %w", err)
	}

	// Create transport server for worker RPC connections.
	transportSrv := coordinator.NewTransportServer(coord, wireCfg.Listen, log.Logger, rpcTLS)

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
	taskConfig := engine.DefaultTaskSlotConfig()
	taskConfig.Checkpoint.Timeout = wireCfg.Checkpoint.Timeout.Duration
	taskConfig.Checkpoint.MinPause = wireCfg.Checkpoint.MinPause.Duration
	taskConfig.Checkpoint.TolerableFailureRate = wireCfg.Checkpoint.TolerableFailureRate
	taskConfig.Checkpoint.MaxConsecutiveFailures = wireCfg.Checkpoint.MaxConsecutiveFailures
	taskConfig.InputBufferSize = wireCfg.TaskSlot.InputBufferSize
	taskConfig.OutputBufferSize = wireCfg.TaskSlot.OutputBufferSize
	taskConfig.AlignmentBufferSize = wireCfg.TaskSlot.AlignmentBufferSize
	taskConfig.CheckpointUploadConcurrency = wireCfg.TaskSlot.CheckpointUploadConcurrency
	taskConfig.DrainTimeout = wireCfg.TaskSlot.DrainTimeout.Duration
	var replicaConfig *worker.CheckpointReplicaConfig
	if cfg := wireCfg.Worker.CheckpointReplica; cfg.ListenAddr != "" {
		replicaConfig = &worker.CheckpointReplicaConfig{ListenAddr: cfg.ListenAddr, AdvertiseAddr: cfg.AdvertiseAddr, StoreRoot: cfg.StoreRoot, ArtifactRoot: cfg.ArtifactRoot, StagingRoot: cfg.StagingRoot, Concurrency: cfg.Concurrency}
	}
	rpcTLS, err := workerRPCTLS(wireCfg.NodeTLS)
	if err != nil {
		return err
	}
	peerTLS, err := workerPeerTLS(wireCfg.Worker.PeerTLS)
	if err != nil {
		return err
	}
	w := worker.NewWithRegistry(worker.Config{
		PeerTLSConfig:        peerTLS,
		MaxFrameSize:         wireCfg.MaxFrameSize,
		EpochPath:            wireCfg.Worker.EpochPath,
		CoordinatorSeeds:     wireCfg.Worker.CoordinatorSeeds,
		DiscoverySecurity:    apiclient.Config(wireCfg.Worker.DiscoveryHTTP),
		HeartbeatInterval:    wireCfg.Heartbeat.Interval.Duration,
		HeartbeatTimeout:     wireCfg.Heartbeat.Timeout.Duration,
		HeartbeatMaxFailures: wireCfg.Heartbeat.MaxFailures,
		RPCTLSConfig:         rpcTLS,
		CheckpointReplica:    replicaConfig,
		TaskSlot:             &taskConfig,
		WorkerID:             wireCfg.Worker.WorkerID,
		CoordinatorAddr:      wireCfg.Worker.CoordinatorAddr,
		ListenAddr:           wireCfg.Worker.ListenAddr,
		TaskSlots:            wireCfg.Worker.TaskSlots,
	}, pipelineWorkerRegistry(), log.Logger)

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
