package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/cmd"
	"github.com/tarungka/wire/internal/logger"
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

	log.Debug().Msgf(mainCtx.Err().Error(), mux)
}

// TODO: move this to a different file
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
