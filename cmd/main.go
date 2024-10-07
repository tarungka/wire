package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/cluster"
	pipeline "github.com/tarungka/wire/pipeline"
	"github.com/tarungka/wire/server"
	"github.com/tarungka/wire/store"
	"github.com/tarungka/wire/tcp"
)

var (
	buildString = "unknown"
	ko          = koanf.New(".")
)

func main() {

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	// logs will be written to both server.log and stdout
	logFile, err := os.OpenFile("server.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Error().Err(err).Msg("Failed to open log file")
	}
	defer logFile.Close()

	// Create a multi-writer to write to both the console and the log file
	multi := zerolog.MultiLevelWriter(os.Stdout, logFile)

	// Set up zerolog to write to the multi-writer
	log.Logger = zerolog.New(multi).With().Timestamp().Logger()

	initFlags(ko)

	if ko.Bool("version") {
		fmt.Println(buildString)
		os.Exit(0)
	} else {
		log.Info().Str("build:", buildString).Msgf("Build Version: %s", buildString)
	}

	log.Info().Msg("Starting the application...")

	raftAddress := ko.String("raft_addr")
	muxListener, err := net.Listen("tcp", raftAddress)
	if err != nil {
		log.Fatal().Err(err).Msgf("failed to listen on %s: %s", raftAddress, err.Error())
	}
	mux, err := startNodeMux(ko, muxListener)
	if err != nil {
		log.Fatal().Msgf("failed to start node mux: %s", err.Error())
	}

	nodeVerifyServername := ko.String("node_verify_server_name")
	noNodeVerify := ko.Bool("no_node_verify")
	// Raft internode layer
	raftLn := mux.Listen(cluster.MuxRaftHeader)
	raftDialer, err := cluster.CreateRaftDialer("", "", "", nodeVerifyServername, noNodeVerify)
	if err != nil {
		log.Fatal().Msgf("failed to create Raft dialer: %s", err.Error())
	}
	raftTn := tcp.NewLayer(raftLn, raftDialer)

	log.Debug().Msgf("A raft layer is ready, will use it: %v", raftTn)

	// The store has the impl for the manager
	str, err := createStore(ko, raftTn)
	if err != nil {
		log.Fatal().Msgf("failed to create store: %s", err.Error())
	}

	// Create cluster service now, so nodes will be able to learn information about each other.
	clstrServ, err := clusterService(ko, mux.Listen(cluster.MuxClusterHeader), str)
	if err != nil {
		log.Fatal().Msgf("failed to create cluster service: %s", err.Error())
	}

	clstrClient, err := createClusterClient(ko, clstrServ)
	if err != nil {
		log.Fatal().Msgf("failed to create cluster client: %s", err.Error())
	}
	httpServ, err := startHTTPService(ko, str, clstrClient)
	if err != nil {
		log.Fatal().Msgf("failed to start HTTP server: %s", err.Error())
	}

	log.Debug().Msgf("HTTP server is not configured. %v", httpServ)

	// Creating a main context; will need to move this code up
	mainCtx := context.Background()

	// Create the cluster!
	nodes, err := str.Nodes()
	if err != nil {
		log.Fatal().Msgf("failed to get nodes %s", err.Error())
	}

	fmt.Printf("%v %v\n", mainCtx, nodes)
	// if err := createCluster(mainCtx, ko, len(nodes) > 0, clstrClient, str, httpServ, nil); err != nil {
	// 	log.Fatal().Msgf("clustering failure: %s", err.Error())
	// }

	// This way the command line arguments are overridden by the remote/other configs
	if ko.Bool("override") {
		if initError := initConfig(ko); initError != nil {
			log.Err(initError).Msg("Error when initializing the config!")
		}
	}

	done := make(chan interface{}, 1)

	var wg sync.WaitGroup

	// Run the web server
	go func(ko *koanf.Koanf) {
		log.Info().Msg("Starting the web server...")
		server.Init(ko)
		// wg is used only for creating a new pipeline
		server.Run(done, &wg, ko)
	}(ko)

	// Start pipelines that have been specified in the config file
	// var dataPipelineConfig pipeline.PipelineDataObject
	dataPipelineConfig := pipeline.GetPipelineInstance()

	allSourcesConfig, allSinksConfig, err := dataPipelineConfig.ParseConfig(ko)
	if err != nil {
		log.Err(err).Msg("Error when reading config")
	}

	for _, sourceConfig := range allSourcesConfig {
		dataPipelineConfig.AddSource(sourceConfig)
	}
	for _, sinkConfig := range allSinksConfig {
		dataPipelineConfig.AddSink(sinkConfig)
	}

	mappedDataPipelines, exists := dataPipelineConfig.GetMappedPipelines()
	if !exists {
		log.Debug().Msg("No data pipelines exist")
	}

	// Run each pipeline
	for pipelineKey, eachDataPipeline := range mappedDataPipelines {
		log.Debug().Msgf("Key: %s | Value: %v", pipelineKey, eachDataPipeline)
		newPipeline := pipeline.NewDataPipeline(eachDataPipeline.Source, eachDataPipeline.Sink)
		pipelineString, err := newPipeline.Show()
		if err != nil {
			log.Err(err).Send()
		}
		log.Debug().Msgf("Creating and running pipeline: %s", pipelineString)

		wg.Add(1)
		go newPipeline.Run(done, &wg)
	}

	// Wait for an interrupt signal (ctrl+c)
	signalChannel := make(chan os.Signal, 1)
	// TODO: Catch SIGTERM and handle it
	signal.Notify(signalChannel, os.Interrupt)
	<-signalChannel // Blocks until an interrupt signal is received

	log.Info().Msg("Process interrupted, shutting down...")

	// Close the done channel to signal all goroutines to exit
	close(done)

	// For for graceful shutdown
	wg.Wait()
}

// startNodeMux starts the TCP mux on the given listener, which should be already
// bound to the relevant interface.
func startNodeMux(ko *koanf.Koanf, ln net.Listener) (*tcp.Mux, error) {
	raftAddress := ko.String("raft_addr")
	var err error
	adv := tcp.NameAddress{
		Address: raftAddress,
	}

	var mux *tcp.Mux
	if ko.String("NodeX509Cert") != "" {
		// TODO: Implement this later
		// var b strings.Builder
		// b.WriteString(fmt.Sprintf("enabling node-to-node encryption with cert: %s, key: %s",
		// 	cfg.NodeX509Cert, cfg.NodeX509Key))
		// if cfg.NodeX509CACert != "" {
		// 	b.WriteString(fmt.Sprintf(", CA cert %s", cfg.NodeX509CACert))
		// }
		// if cfg.NodeVerifyClient {
		// 	b.WriteString(", mutual TLS enabled")
		// } else {
		// 	b.WriteString(", mutual TLS disabled")
		// }
		// log.Println(b.String())
		// mux, err = tcp.NewTLSMux(ln, adv, cfg.NodeX509Cert, cfg.NodeX509Key, cfg.NodeX509CACert,
		// 	cfg.NoNodeVerify, cfg.NodeVerifyClient)
	} else {
		mux, err = tcp.NewMux(ln, adv)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create node-to-node mux: %s", err.Error())
	}
	go mux.Serve()
	return mux, nil
}

func clusterService(ko *koanf.Koanf, ln net.Listener, mgr cluster.Manager) (*cluster.Service, error) {
	c := cluster.New(ln, mgr)
	apiAddress := ko.String("http_adv")
	c.SetAPIAddr(apiAddress)
	// TODO: support HTTP over SSL
	c.EnableHTTPS(false) // Conditions met for an HTTPS API
	if err := c.Open(); err != nil {
		return nil, err
	}
	return c, nil
}

func createClusterClient(ko *koanf.Koanf, clstr *cluster.Service) (*cluster.Client, error) {
	var dialerTLSConfig *tls.Config
	// TODO: Dialer over SSL
	// var err error
	// if cfg.NodeX509Cert != "" || cfg.NodeX509CACert != "" {
	// 	dialerTLSConfig, err = rtls.CreateClientConfig(cfg.NodeX509Cert, cfg.NodeX509Key,
	// 		cfg.NodeX509CACert, cfg.NodeVerifyServerName, cfg.NoNodeVerify)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("failed to create TLS config for cluster dialer: %s", err.Error())
	// 	}
	// }

	// clusterConnectTimeout := ko.Int64("cluster_connect_timeout")
	raftAddress := ko.String("raft_addr")
	clstrDialer := tcp.NewDialer(cluster.MuxClusterHeader, dialerTLSConfig)
	clstrClient := cluster.NewClient(clstrDialer, 10*time.Second)
	if err := clstrClient.SetLocal(raftAddress, clstr); err != nil {
		return nil, fmt.Errorf("failed to set cluster client local parameters: %s", err.Error())
	}
	return clstrClient, nil
}

func createStore(ko *koanf.Koanf, ln *tcp.Layer) (*store.Store, error) {
	dbConf := store.NewDBConfig()
	dbConf.OnDiskPath = ko.String("OnDiskPath")
	dbConf.FKConstraints = ko.Bool("FKConstraints")
	// dbConf.Extensions = extensions

	str := store.New(ln, &store.Config{
		DBConf: dbConf,
		Dir:    ko.String("DataPath"),
		ID:     ko.String("NodeID"),
	})

	// Set optional parameters on store.
	str.RaftLogLevel = ko.String("RaftLogLevel")
	str.ShutdownOnRemove = ko.Bool("RaftShutdownOnRemove")
	str.SnapshotThreshold = uint64(ko.Int64("RaftSnapThreshold"))
	str.SnapshotThresholdWALSize = uint64(ko.Int64("RaftSnapThresholdWALSize"))
	str.SnapshotInterval = time.Duration(ko.Int64("RaftSnapInterval")) * time.Second
	str.LeaderLeaseTimeout = time.Duration(ko.Int64("RaftLeaderLeaseTimeout")) * time.Second
	str.HeartbeatTimeout = time.Duration(ko.Int64("RaftHeartbeatTimeout")) * time.Second
	str.ElectionTimeout = time.Duration(ko.Int64("RaftElectionTimeout")) * time.Second
	str.ApplyTimeout = time.Duration(ko.Int64("RaftApplyTimeout")) * time.Second
	str.BootstrapExpect = ko.Int("BootstrapExpect")
	str.ReapTimeout = time.Duration(ko.Int64("RaftReapNodeTimeout")) * time.Second
	str.ReapReadOnlyTimeout = time.Duration(ko.Int64("RaftReapReadOnlyNodeTimeout")) * time.Second
	str.AutoVacInterval = time.Duration(ko.Int64("AutoVacInterval")) * time.Second
	str.AutoOptimizeInterval = time.Duration(ko.Int64("AutoOptimizeInterval")) * time.Second

	if store.IsNewNode(ko.String("DataPath")) {
		log.Printf("no preexisting node state detected in %s, node may be bootstrapping", ko.String("DataPath"))
	} else {
		log.Printf("preexisting node state detected in %s", ko.String("DataPath"))
	}

	return str, nil
}

// func startHTTPService(ko *koanf.Koanf, str *store.Store, cltr *cluster.Client, credStr *auth.CredentialsStore) (string, error) {
func startHTTPService(ko *koanf.Koanf, str *store.Store, cltr *cluster.Client) (string, error) {
	return "", nil
	// Create HTTP server and load authentication information.
	// s := httpd.New(cfg.HTTPAddr, str, cltr, credStr)

	// s.CACertFile = cfg.HTTPx509CACert
	// s.CertFile = cfg.HTTPx509Cert
	// s.KeyFile = cfg.HTTPx509Key
	// s.ClientVerify = cfg.HTTPVerifyClient
	// s.DefaultQueueCap = cfg.WriteQueueCap
	// s.DefaultQueueBatchSz = cfg.WriteQueueBatchSz
	// s.DefaultQueueTimeout = cfg.WriteQueueTimeout
	// s.DefaultQueueTx = cfg.WriteQueueTx
	// s.BuildInfo = map[string]interface{}{
	// 	"commit":             cmd.Commit,
	// 	"branch":             cmd.Branch,
	// 	"version":            cmd.Version,
	// 	"compiler_toolchain": runtime.Compiler,
	// 	"compiler_command":   cmd.CompilerCommand,
	// 	"build_time":         cmd.Buildtime,
	// }
	// s.SetAllowOrigin(cfg.HTTPAllowOrigin)
	// return s, s.Start()
}

// TODO: This code needs major rework, will work on this later
// func createCluster(ctx context.Context, cfg *Config, hasPeers bool, client *cluster.Client, str *store.Store,
// 	httpServ *httpd.Service, credStr *auth.CredentialsStore) error {
// 	joins := cfg.JoinAddresses()
// 	if err := networkCheckJoinAddrs(joins); err != nil {
// 		return err
// 	}
// 	if joins == nil && cfg.DiscoMode == "" && !hasPeers {
// 		if cfg.RaftNonVoter {
// 			return fmt.Errorf("cannot create a new non-voting node without joining it to an existing cluster")
// 		}

// 		// Brand new node, told to bootstrap itself. So do it.
// 		log.Info().Msg("bootstrapping single new node")
// 		if err := str.Bootstrap(store.NewServer(str.ID(), cfg.RaftAdv, true)); err != nil {
// 			return fmt.Errorf("failed to bootstrap single new node: %s", err.Error())
// 		}
// 		return nil
// 	}

// 	// Prepare definition of being part of a cluster.
// 	bootDoneFn := func() bool {
// 		leader, _ := str.LeaderAddr()
// 		return leader != ""
// 	}
// 	clusterSuf := cluster.VoterSuffrage(!cfg.RaftNonVoter)

// 	joiner := cluster.NewJoiner(client, cfg.JoinAttempts, cfg.JoinInterval)
// 	joiner.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
// 	if joins != nil && cfg.BootstrapExpect == 0 {
// 		// Explicit join operation requested, so do it.
// 		j, err := joiner.Do(ctx, joins, str.ID(), cfg.RaftAdv, clusterSuf)
// 		if err != nil {
// 			return fmt.Errorf("failed to join cluster: %s", err.Error())
// 		}
// 		log.Info().Msg("successfully joined cluster at", j)
// 		return nil
// 	}

// 	if joins != nil && cfg.BootstrapExpect > 0 {
// 		// Bootstrap with explicit join addresses requests.
// 		bs := cluster.NewBootstrapper(cluster.NewAddressProviderString(joins), client)
// 		bs.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
// 		return bs.Boot(ctx, str.ID(), cfg.RaftAdv, clusterSuf, bootDoneFn, cfg.BootstrapExpectTimeout)
// 	}

// 	if cfg.DiscoMode == "" {
// 		// No more clustering techniques to try. Node will just sit, probably using
// 		// existing Raft state.
// 		return nil
// 	}

// 	// DNS-based discovery requested. It's OK to proceed with this even if this node
// 	// is already part of a cluster. Re-joining and re-notifying other nodes will be
// 	// ignored when the node is already part of the cluster.
// 	log.Printf("discovery mode: %s", cfg.DiscoMode)
// 	switch cfg.DiscoMode {
// 	case DiscoModeDNS, DiscoModeDNSSRV:
// 		rc := cfg.DiscoConfigReader()
// 		defer func() {
// 			if rc != nil {
// 				rc.Close()
// 			}
// 		}()

// 		var provider interface {
// 			cluster.AddressProvider
// 			httpd.StatusReporter
// 		}
// 		if cfg.DiscoMode == DiscoModeDNS {
// 			dnsCfg, err := dns.NewConfigFromReader(rc)
// 			if err != nil {
// 				return fmt.Errorf("error reading DNS configuration: %s", err.Error())
// 			}
// 			provider = dns.NewWithPort(dnsCfg, cfg.RaftPort())

// 		} else {
// 			dnssrvCfg, err := dnssrv.NewConfigFromReader(rc)
// 			if err != nil {
// 				return fmt.Errorf("error reading DNS configuration: %s", err.Error())
// 			}
// 			provider = dnssrv.New(dnssrvCfg)
// 		}

// 		bs := cluster.NewBootstrapper(provider, client)
// 		bs.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
// 		httpServ.RegisterStatus("disco", provider)
// 		return bs.Boot(ctx, str.ID(), cfg.RaftAdv, clusterSuf, bootDoneFn, cfg.BootstrapExpectTimeout)

// 	case DiscoModeEtcdKV, DiscoModeConsulKV:
// 		discoService, err := createDiscoService(cfg, str)
// 		if err != nil {
// 			return fmt.Errorf("failed to start discovery service: %s", err.Error())
// 		}
// 		// Safe to start reporting before doing registration. If the node hasn't bootstrapped
// 		// yet, or isn't leader, reporting will just be a no-op until something changes.
// 		go discoService.StartReporting(cfg.NodeID, cfg.HTTPURL(), cfg.RaftAdv)
// 		httpServ.RegisterStatus("disco", discoService)

// 		if hasPeers {
// 			log.Printf("preexisting node configuration detected, not registering with discovery service")
// 			return nil
// 		}
// 		log.Info().Msg("no preexisting nodes, registering with discovery service")

// 		leader, addr, err := discoService.Register(str.ID(), cfg.HTTPURL(), cfg.RaftAdv)
// 		if err != nil {
// 			return fmt.Errorf("failed to register with discovery service: %s", err.Error())
// 		}
// 		if leader {
// 			log.Info().Msg("node registered as leader using discovery service")
// 			if err := str.Bootstrap(store.NewServer(str.ID(), str.Addr(), true)); err != nil {
// 				return fmt.Errorf("failed to bootstrap single new node: %s", err.Error())
// 			}
// 		} else {
// 			for {
// 				log.Printf("discovery service returned %s as join address", addr)
// 				if j, err := joiner.Do(ctx, []string{addr}, str.ID(), cfg.RaftAdv, clusterSuf); err != nil {
// 					log.Printf("failed to join cluster at %s: %s", addr, err.Error())

// 					time.Sleep(time.Second)
// 					_, addr, err = discoService.Register(str.ID(), cfg.HTTPURL(), cfg.RaftAdv)
// 					if err != nil {
// 						log.Printf("failed to get updated leader: %s", err.Error())
// 					}
// 					continue
// 				} else {
// 					log.Info().Msg("successfully joined cluster at", j)
// 					break
// 				}
// 			}
// 		}

// 	default:
// 		return fmt.Errorf("invalid disco mode %s", cfg.DiscoMode)
// 	}
// 	return nil
// }