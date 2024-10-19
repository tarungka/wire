package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/knadh/koanf/v2"
	"github.com/rqlite/rqlite/v8/auth"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/cluster"
	"github.com/tarungka/wire/internal/cmd"
	httpd "github.com/tarungka/wire/internal/http"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/store"
	"github.com/tarungka/wire/internal/tcp"
)

var (
	ko = koanf.New(".")
)

// Need to make up my mind on some of these:
// The high-performance, distributed stream processing platform.
// Seamless Streaming for Dynamic Workloads.
// There is a new line at the start of this logo

const logo = `
 __      __ ________________________
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
	raftLn := mux.Listen(cluster.MuxRaftHeader)
	raftDialer, err := cluster.CreateRaftDialer("", "", "", cfg.NodeVerifyServerName, cfg.NoNodeVerify)
	if err != nil {
		log.Fatal().Msgf("failed to create Raft dialer: %s", err.Error())
	}
	raftTn := tcp.NewLayer(raftLn, raftDialer)
	log.Debug().Msgf("raft layer is ready")

	// Create the store
	str, err := createStore(cfg, raftTn)
	if err != nil {
		log.Fatal().Msgf("failed to create store: %s", err.Error())
	}
	log.Debug().Msgf("store created")

	// Create cluster service now, so nodes will be able to learn information about each other.
	clstrLn := mux.Listen(cluster.MuxClusterHeader)
	clstrServ, err := clusterService(cfg, clstrLn, str)
	if err != nil {
		log.Fatal().Msgf("failed to create cluster service: %s", err.Error())
	}
	log.Debug().Msgf("created the cluster service")

	clstrClient, err := createClusterClient(cfg, clstrServ)
	if err != nil {
		log.Fatal().Msgf("failed to create cluster client: %s", err.Error())
	}

	// Create the HTTP service.
	//
	// We want to start the HTTP server as soon as possible, so the node is responsive and external
	// systems can see that it's running. We still have to open the Store though, so the node won't
	// be able to do much until that happens however.
	httpServ, err := startHTTPService(cfg, str, mainCtx, clstrClient)
	if err != nil {
		log.Fatal().Msgf("failed to start HTTP server: %s", err.Error())
	}

	// Now, open the store
	if err := str.Open(); err != nil {
		log.Fatal().Msgf("failed to open store: %s", err.Error())
	}

	// Register remaining status providers.
	if err := httpServ.RegisterStatus("cluster", clstrServ); err != nil {
		log.Fatal().Msgf("failed to register cluster status provider: %s", err.Error())
	}
	if err := httpServ.RegisterStatus("network", tcp.NetworkReporter{}); err != nil {
		log.Fatal().Msgf("failed to register network status provider: %s", err.Error())
	}
	if err := httpServ.RegisterStatus("mux", mux); err != nil {
		log.Fatal().Msgf("failed to register mux status provider: %s", err.Error())
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

	if err := createCluster(mainCtx, cfg, len(nodes) > 0, clstrClient, str, httpServ, nil); err != nil {
		log.Fatal().Msgf("clustering failure: %s", err.Error())
	}

	<-mainCtx.Done()

	log.Info().Msg("Process interrupted, shutting down...")

	// Stop the HTTP server and other network access first so clients get notification as soon as
	// possible that the node is going away.
	httpServ.Close()
	clstrServ.Close()

	if cfg.RaftClusterRemoveOnShutdown {
		remover := cluster.NewRemover(clstrClient, 5*time.Second, str)
		// TODO: not support TLS for now, will work on it later
		// remover.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
		log.Info().Msgf("initiating removal of this node from cluster before shutdown")
		if err := remover.Do(cfg.NodeID, true); err != nil {
			log.Error().Msgf("failed to remove this node from cluster before shutdown: %s", err.Error())
		}
		log.Info().Msgf("removed this node successfully from cluster before shutdown")
	}

	if cfg.RaftStepdownOnShutdown {
		if str.IsLeader() {
			// Don't log a confusing message if (probably) not Leader
			log.Info().Msgf("stepping down as Leader before shutdown")
		}
		// Perform a stepdown, ignore any errors.
		str.Stepdown(true)
	}
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
		// TODO: Implement this later
		var b strings.Builder
		b.WriteString(fmt.Sprintf("enabling node-to-node encryption with cert: %s, key: %s",
			cfg.NodeX509Cert, cfg.NodeX509Key))
		if cfg.NodeX509CACert != "" {
			b.WriteString(fmt.Sprintf(", CA cert %s", cfg.NodeX509CACert))
		}
		if cfg.NodeVerifyClient {
			b.WriteString(", mutual TLS enabled")
		} else {
			b.WriteString(", mutual TLS disabled")
		}
		mux, err = tcp.NewTLSMux(ln, adv, cfg.NodeX509Cert, cfg.NodeX509Key, cfg.NodeX509CACert,
			cfg.NoNodeVerify, cfg.NodeVerifyClient)
	} else {
		mux, err = tcp.NewMux(ln, adv)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create node-to-node mux: %s", err.Error())
	}
	go mux.Serve()
	return mux, nil
}

func clusterService(cfg *Config, ln net.Listener, mgr cluster.Manager) (*cluster.Service, error) {
	c := cluster.New(ln, mgr)
	c.SetAPIAddr(cfg.HTTPAddr)
	// TODO: support HTTP over SSL
	c.EnableHTTPS(cfg.HTTPx509Cert != "" && cfg.HTTPx509Key != "") // Conditions met for an HTTPS API
	if err := c.Open(); err != nil {
		return nil, err
	}
	return c, nil
}

func createClusterClient(cfg *Config, clstr *cluster.Service) (*cluster.Client, error) {
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

	clstrDialer := tcp.NewDialer(cluster.MuxClusterHeader, dialerTLSConfig)
	clstrClient := cluster.NewClient(clstrDialer, 10*time.Second)
	if err := clstrClient.SetLocal(cfg.RaftAdv, clstr); err != nil {
		return nil, fmt.Errorf("failed to set cluster client local parameters: %s", err.Error())
	}
	return clstrClient, nil
}

func createStore(cfg *Config, ln *tcp.Layer) (*store.Store, error) {
	str := store.New(ln, &store.Config{
		Dir: cfg.DataPath,
		ID:  cfg.NodeID,
	})

	// Set optional parameters on store.
	str.RaftLogLevel = cfg.RaftLogLevel
	str.ShutdownOnRemove = cfg.RaftShutdownOnRemove
	str.SnapshotThreshold = cfg.RaftSnapThreshold
	str.SnapshotInterval = cfg.RaftSnapInterval
	str.LeaderLeaseTimeout = cfg.RaftLeaderLeaseTimeout
	str.HeartbeatTimeout = cfg.RaftHeartbeatTimeout
	str.ElectionTimeout = cfg.RaftElectionTimeout
	str.ApplyTimeout = cfg.RaftApplyTimeout
	str.BootstrapExpect = cfg.BootstrapExpect
	str.ReapTimeout = cfg.RaftReapNodeTimeout
	str.ReapReadOnlyTimeout = cfg.RaftReapReadOnlyNodeTimeout
	str.AutoVacInterval = cfg.AutoVacInterval
	str.AutoOptimizeInterval = cfg.AutoOptimizeInterval

	if store.IsNewNode(cfg.DataPath) {
		log.Printf("no preexisting node state detected in %s, node may be bootstrapping", cfg.DataPath)
	} else {
		log.Printf("preexisting node state detected in %s", cfg.DataPath)
	}

	return str, nil
}

func startHTTPService(cfg *Config, str *store.Store, ctx context.Context ,cltr *cluster.Client) (*httpd.Service, error) {
	// Create HTTP server and load authentication information.
	s := httpd.New(cfg.HTTPAddr, str, cltr, nil)

	// TODO: Need to support HTTPS
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

func createCluster(ctx context.Context, cfg *Config, hasPeers bool, client *cluster.Client, str *store.Store,
	httpServ *httpd.Service, credStr *auth.CredentialsStore) error {
	joins := cfg.JoinAddresses()
	if err := networkCheckJoinAddrs(joins); err != nil {
		return err
	}
	// When this is a single node cluster
	if joins == nil && cfg.DiscoMode == "" && !hasPeers {
		if cfg.RaftNonVoter {
			return fmt.Errorf("cannot create a new non-voting node without joining it to an existing cluster")
		}

		// Brand new node, told to bootstrap itself. So do it.
		log.Info().Msg("bootstrapping single new node")
		newServer := store.NewServer(str.ID(), cfg.RaftAdv, true)
		if err := str.Bootstrap(newServer); err != nil {
			return fmt.Errorf("failed to bootstrap single new node: %s", err.Error())
		}
		return nil
	}

	// Prepare definition of being part of a cluster.
	bootDoneFn := func() bool {
		leader, _ := str.LeaderAddr()
		return leader != ""
	}
	clusterSuf := cluster.VoterSuffrage(!cfg.RaftNonVoter) // The suffrage of the node in the cluster
	log.Debug().Msgf("the suffrage of the node in the cluster is: %v", clusterSuf)

	joiner := cluster.NewJoiner(client, cfg.JoinAttempts, cfg.JoinInterval)
	joiner.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs)) // This is not necessary for now as we do not support TLS
	// If there is NO min quorum required to create a cluster
	if joins != nil && cfg.BootstrapExpect == 0 {
		// Explicit join operation requested, so do it.
		log.Debug().Msgf("joining a cluster with no min quorum")
		j, err := joiner.Do(ctx, joins, str.ID(), cfg.RaftAdv, clusterSuf)
		if err != nil {
			return fmt.Errorf("failed to join cluster: %s", err.Error())
		}
		log.Info().Msgf("successfully joined cluster at %v", j)
		return nil
	}

	// If there is a min quorum required to create a cluster
	if joins != nil && cfg.BootstrapExpect > 0 {
		// Bootstrap with explicit join addresses requests.
		bs := cluster.NewBootstrapper(cluster.NewAddressProviderString(joins), client)
		bs.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
		return bs.Boot(ctx, str.ID(), cfg.RaftAdv, clusterSuf, bootDoneFn, cfg.BootstrapExpectTimeout)
	}

	if cfg.DiscoMode == "" {
		// No more clustering techniques to try. Node will just sit, probably using
		// existing Raft state.
		return nil
	}

	// DNS-based discovery requested. It's OK to proceed with this even if this node
	// is already part of a cluster. Re-joining and re-notifying other nodes will be
	// ignored when the node is already part of the cluster.
	log.Printf("discovery mode: %s", cfg.DiscoMode)
	switch cfg.DiscoMode {
	// TODO: need to impl this
	// case DiscoModeDNS, DiscoModeDNSSRV:
	// 	rc := cfg.DiscoConfigReader()
	// 	defer func() {
	// 		if rc != nil {
	// 			rc.Close()
	// 		}
	// 	}()

	// 	var provider interface {
	// 		cluster.AddressProvider
	// 		httpd.StatusReporter
	// 	}
	// 	if cfg.DiscoMode == DiscoModeDNS {
	// 		dnsCfg, err := dns.NewConfigFromReader(rc)
	// 		if err != nil {
	// 			return fmt.Errorf("error reading DNS configuration: %s", err.Error())
	// 		}
	// 		provider = dns.NewWithPort(dnsCfg, cfg.RaftPort())

	// 	} else {
	// 		dnssrvCfg, err := dnssrv.NewConfigFromReader(rc)
	// 		if err != nil {
	// 			return fmt.Errorf("error reading DNS configuration: %s", err.Error())
	// 		}
	// 		provider = dnssrv.New(dnssrvCfg)
	// 	}

	// 	bs := cluster.NewBootstrapper(provider, client)
	// 	bs.SetCredentials(cluster.CredentialsFor(credStr, cfg.JoinAs))
	// 	httpServ.RegisterStatus("disco", provider)
	// 	return bs.Boot(ctx, str.ID(), cfg.RaftAdv, clusterSuf, bootDoneFn, cfg.BootstrapExpectTimeout)

	// case DiscoModeEtcdKV, DiscoModeConsulKV:
	// 	discoService, err := createDiscoService(cfg, str)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to start discovery service: %s", err.Error())
	// 	}
	// 	// Safe to start reporting before doing registration. If the node hasn't bootstrapped
	// 	// yet, or isn't leader, reporting will just be a no-op until something changes.
	// 	go discoService.StartReporting(cfg.NodeID, cfg.HTTPURL(), cfg.RaftAdv)
	// 	httpServ.RegisterStatus("disco", discoService)

	// 	if hasPeers {
	// 		log.Printf("preexisting node configuration detected, not registering with discovery service")
	// 		return nil
	// 	}
	// 	log.Println("no preexisting nodes, registering with discovery service")

	// 	leader, addr, err := discoService.Register(str.ID(), cfg.HTTPURL(), cfg.RaftAdv)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to register with discovery service: %s", err.Error())
	// 	}
	// 	if leader {
	// 		log.Println("node registered as leader using discovery service")
	// 		if err := str.Bootstrap(store.NewServer(str.ID(), str.Addr(), true)); err != nil {
	// 			return fmt.Errorf("failed to bootstrap single new node: %s", err.Error())
	// 		}
	// 	} else {
	// 		for {
	// 			log.Printf("discovery service returned %s as join address", addr)
	// 			if j, err := joiner.Do(ctx, []string{addr}, str.ID(), cfg.RaftAdv, clusterSuf); err != nil {
	// 				log.Printf("failed to join cluster at %s: %s", addr, err.Error())

	// 				time.Sleep(time.Second)
	// 				_, addr, err = discoService.Register(str.ID(), cfg.HTTPURL(), cfg.RaftAdv)
	// 				if err != nil {
	// 					log.Printf("failed to get updated leader: %s", err.Error())
	// 				}
	// 				continue
	// 			} else {
	// 				log.Println("successfully joined cluster at", j)
	// 				break
	// 			}
	// 		}
	// 	}

	default:
		return fmt.Errorf("invalid disco mode %s", cfg.DiscoMode)
	}
	return nil
}

func joinAddresses(joinAddrs string) ([]string, error) {
	if joinAddrs == "" {
		return nil, nil
	}
	addrs := strings.Split(joinAddrs, ",")
	for i := range addrs {
		if _, _, err := net.SplitHostPort(addrs[i]); err != nil {
			return nil, fmt.Errorf("%s is an invalid join address", addrs[i])

		}
	}
	return strings.Split(joinAddrs, ","), nil
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
