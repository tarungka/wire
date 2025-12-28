package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/rqlite/rqlite/v8/auth"
	"github.com/rs/zerolog/log"
	aruntime "github.com/tarungka/wire/internal/analytics/runtime"
	"github.com/tarungka/wire/internal/cluster"
	"github.com/tarungka/wire/internal/cmd"
	httpd "github.com/tarungka/wire/internal/http"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/new/store"
	"github.com/tarungka/wire/internal/tcp"
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
	mainCtx, _ := CreateContext(sigCh)

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
	fmt.Println(logo)

	logger.SetDevelopment(cfg.DebugMode)
	logger.SetLogFile(logFile)

	log.Logger = logger.GetLogger("main")

	if cfg.DebugMode {
		hostName, err := os.Hostname()
		if err != nil {
			log.Debug().Err(err).Msgf("error when getting hostname: %v", err)
		}
		hostIP, err := getHostIP()
		if err != nil {
			log.Debug().Err(err).Msgf("error when getting host IP: %v", err)
		}
		log.Debug().Msgf("PID: %v | PPID: %v | Host ID: %v | Host IP: %v", os.Getpid(), os.Getppid(), hostName, hostIP)
	}

	log.Info().Msg("Starting the application...")

	// Create internode network mux and configure.
	muxListener, err := net.Listen("tcp", cfg.RaftAddr)
	if err != nil {
		log.Fatal().Err(err).Msgf("failed to listen on %s: %s", cfg.RaftAddr, err.Error())
	}
	log.Debug().Msgf("listener mux address is: %s", cfg.RaftAddr)
	mux, err := startNodeMux(cfg, muxListener)
	if err != nil {
		log.Fatal().Msgf("failed to start node mux: %s", err.Error())
	}
	log.Debug().Msgf("node mux started")

	// Raft internode layer
	raftLn, err := mux.Listen(cluster.MuxRaftHeader)
	if err != nil {
		log.Fatal().Msgf("failed to listen for Raft: %s", err.Error())
	}
	raftDialer, err := cluster.CreateRaftDialer("", "", "", cfg.NodeVerifyServerName, cfg.NoNodeVerify)
	if err != nil {
		log.Fatal().Msgf("failed to create Raft dialer: %s", err.Error())
	}
	raftTn, err := tcp.NewLayer(raftLn, raftDialer)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create raft layer")
	}
	log.Debug().Msgf("raft layer is ready")

	// Create the store
	str, err := createStore(cfg, raftTn)
	if err != nil {
		log.Fatal().Msgf("failed to create store: %s", err.Error())
	}
	log.Debug().Msgf("store created")

	// Create cluster service now, so nodes will be able to learn information about each other.
	clstrLn, err := mux.Listen(cluster.MuxClusterHeader)
	if err != nil {
		log.Fatal().Msgf("failed to listen for Cluster: %s", err.Error())
	}
	clstrServ, err := clusterService(cfg, clstrLn, str, str)
	if err != nil {
		log.Fatal().Msgf("failed to create cluster service: %s", err.Error())
	}
	log.Debug().Msgf("created the cluster service")

	clstrClient, err := createClusterClient(cfg, clstrServ)
	if err != nil {
		log.Fatal().Msgf("failed to create cluster client: %s", err.Error())
	}

	// Create analytical engine components
	workerManager := aruntime.NewWorkerManager(mainCtx, cfg.NodeID)
	clstrServ.WorkerManager = workerManager

	// Create analytics data plane
	analyticsLn, err := mux.Listen(aruntime.MuxAnalyticsHeader)
	if err != nil {
		log.Fatal().Msgf("failed to listen for analytics: %s", err.Error())
	}
	analyticsDialer := tcp.NewDialer(aruntime.MuxAnalyticsHeader, nil)
	dataPlane := aruntime.NewDataPlane(analyticsLn, analyticsDialer, workerManager)
	go dataPlane.Start()

	// JobManager runs on all nodes but is only active on the leader.
	jobManager := aruntime.NewJobManager(mainCtx, cfg.NodeID, []string{cfg.RaftAdv}, workerManager)

	// Create the HTTP service.
	httpServ, err := startHTTPService(cfg, str, mainCtx, clstrClient, jobManager)
	if err != nil {
		log.Fatal().Msgf("failed to start HTTP server: %s", err.Error())
	}

	// Now, open the store
	if err := str.Open(); err != nil {
		log.Fatal().Msgf("failed to open store: %s", err.Error())
	}

	// Create the cluster!
	nodes, err := str.Nodes()
	if err != nil {
		log.Fatal().Msgf("failed to get nodes %s", err.Error())
	}
	log.Debug().Msgf("the number of nodes are: %d", len(nodes))
	for idx, eachNode := range nodes {
		log.Debug().Msgf("%d. Node information is: %v", idx, eachNode)
	}

	if err := createCluster(mainCtx, cfg, len(nodes) > 0, clstrClient, str, nil, nil); err != nil {
		log.Fatal().Msgf("clustering failure: %s", err.Error())
	}

	<-mainCtx.Done()

	log.Info().Msg("Process interrupted, shutting down...")

	// Stop the HTTP server and other network access first so clients get notification as soon as
	// possible that the node is going away.
	httpServ.Close()
	clstrServ.Close()

	if cfg.RaftStepdownOnShutdown {
		if str.IsLeader() {
			log.Info().Msgf("stepping down as Leader before shutdown")
		}
		str.Stepdown(true)
	}
	log.Debug().Msgf("closing mux listener listening on %s", muxListener.Addr().String())
	muxListener.Close()

	if err := str.Close(true); err != nil {
		log.Info().Msgf("failed to close store: %s", err.Error())
	}
}

// startNodeMux starts the TCP mux on the given listener, which should be already
// bound to the relevant interface.
func startNodeMux(cfg *Config, ln net.Listener) (*tcp.Mux, error) {
	var err error
	adv := tcp.NameAddress{
		Address: cfg.RaftAdv,
	}

	log.Debug().Msgf("advertised mux address is: %s", cfg.RaftAdv)

	var mux *tcp.Mux
	if cfg.NodeX509Cert != "" {
		/*
			mux, err = tcp.NewTLSMux(ln, adv, cfg.NodeX509Cert, cfg.NodeX509Key, cfg.NodeX509CACert,
				cfg.NoNodeVerify, cfg.NodeVerifyClient)
		*/
		return nil, fmt.Errorf("TLS mux not supported yet")
	} else {
		mux, err = tcp.NewMux(ln, adv)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create node-to-node mux: %s", err.Error())
	}
	go mux.Serve()
	return mux, nil
}

func clusterService(cfg *Config, ln net.Listener, db cluster.Database, mgr cluster.Manager) (*cluster.Service, error) {
	c := cluster.New(ln, db, mgr)
	c.SetAPIAddr(cfg.HTTPAddr)
	c.EnableHTTPS(cfg.HTTPx509Cert != "" && cfg.HTTPx509Key != "")
	if err := c.Open(); err != nil {
		return nil, err
	}
	return c, nil
}

func createClusterClient(cfg *Config, clstr *cluster.Service) (*cluster.Client, error) {
	var dialerTLSConfig *tls.Config
	clstrDialer := tcp.NewDialer(cluster.MuxClusterHeader, dialerTLSConfig)
	clstrClient := cluster.NewClient(clstrDialer, 10*time.Second)
	if err := clstrClient.SetLocal(cfg.RaftAdv, clstr); err != nil {
		return nil, fmt.Errorf("failed to set cluster client local parameters: %s", err.Error())
	}
	return clstrClient, nil
}

func createStore(cfg *Config, ly *tcp.Layer) (*store.NodeStore, error) {
	str, err := store.New(ly, &store.Config{
		Dir:           cfg.DataPath,
		ID:            cfg.NodeID,
		StoreDatabase: cfg.StoreDatabase,
	})
	if err != nil {
		return nil, err
	}

	if store.IsNewNode(cfg.DataPath) {
		log.Printf("no preexisting node state detected in %s, node may be bootstrapping", cfg.DataPath)
	} else {
		log.Printf("preexisting node state detected in %s", cfg.DataPath)
	}

	return str, nil
}

func startHTTPService(cfg *Config, str *store.NodeStore, ctx context.Context, cltr *cluster.Client, jm *aruntime.JobManager) (*httpd.Service, error) {
	s := httpd.New(cfg.HTTPAddr, str, cltr, nil, jm)

	s.CACertFile = cfg.HTTPx509CACert
	s.CertFile = cfg.HTTPx509Cert
	s.KeyFile = cfg.HTTPx509Key
	s.ClientVerify = cfg.HTTPVerifyClient
	s.DefaultQueueCap = cfg.WriteQueueCap
	s.DefaultQueueBatchSz = cfg.WriteQueueBatchSz
	s.DefaultQueueTimeout = cfg.WriteQueueTimeout
	s.DefaultQueueTx = cfg.WriteQueueTx
	s.BuildInfo = map[string]interface{}{
		"commit":             cmd.Commit,
		"branch":             cmd.Branch,
		"version":            cmd.Version,
		"compiler_toolchain": runtime.Compiler,
		"compiler_command":   cmd.CompilerCommand,
		"build_time":         cmd.Buildtime,
	}
	s.SetAllowOrigin(cfg.HTTPAllowOrigin)
	return s, s.Start(ctx)
}

func createCluster(ctx context.Context, cfg *Config, hasPeers bool, client *cluster.Client, str *store.NodeStore,
	httpServ *httpd.Service, credStr *auth.CredentialsStore) error {
	joins := cfg.JoinAddresses()
	if err := networkCheckJoinAddrs(joins); err != nil {
		return err
	}
	if joins == nil && cfg.DiscoMode == "" && !hasPeers {
		if cfg.RaftNonVoter {
			return fmt.Errorf("cannot create a new non-voting node without joining it to an existing cluster")
		}

		log.Info().Msg("bootstrapping single new node")
		newServer := store.NewServer(str.ID(), cfg.RaftAdv, true)
		if err := str.Bootstrap(newServer); err != nil {
			return fmt.Errorf("failed to bootstrap single new node: %s", err.Error())
		}
		return nil
	}

	bootDoneFn := func() bool {
		leader, _ := str.LeaderAddr()
		return leader != ""
	}
	clusterSuf := cluster.VoterSuffrage(!cfg.RaftNonVoter)
	log.Debug().Msgf("the suffrage of the node in the cluster is: %v", clusterSuf)

	joiner := cluster.NewJoiner(client, cfg.JoinAttempts, cfg.JoinInterval)
	joiner.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
	if joins != nil && cfg.BootstrapExpect == 0 {
		log.Debug().Msgf("joining a cluster with no min quorum")
		j, err := joiner.Do(ctx, joins, str.ID(), cfg.RaftAdv, clusterSuf)
		if err != nil {
			return fmt.Errorf("failed to join cluster: %s", err.Error())
		}
		log.Info().Msgf("successfully joined cluster at %v", j)
		return nil
	}

	if joins != nil && cfg.BootstrapExpect > 0 {
		bs := cluster.NewBootstrapper(cluster.NewAddressProviderString(joins), client)
		bs.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
		return bs.Boot(ctx, str.ID(), cfg.RaftAdv, clusterSuf, bootDoneFn, cfg.BootstrapExpectTimeout)
	}

	if cfg.DiscoMode == "" {
		return nil
	}

	log.Printf("discovery mode: %s", cfg.DiscoMode)
	switch cfg.DiscoMode {
	default:
		return fmt.Errorf("invalid disco mode %s", cfg.DiscoMode)
	}
}

func networkCheckJoinAddrs(joinAddrs []string) error {
	if len(joinAddrs) > 0 {
		log.Debug().Msg("checking that supplied join addresses don't serve HTTP(S)")
		if addr, ok := httpd.AnyServingHTTP(joinAddrs); ok {
			return fmt.Errorf("join address %s appears to be serving HTTP when it should be Raft", addr)
		}
	}
	log.Printf("none of the nodes %v are serving HTTP", joinAddrs)
	return nil
}

func getHostIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("error getting IP addresses: %v", err)
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("error getting IP address")
}
