package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/cmd"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/pipeline"
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
	fmt.Print(logo)

	logger.SetDevelopment(cfg.DebugMode)
	logger.SetLogFile(logFile)

	log.Logger = logger.GetLogger("main")

	if cfg.DebugMode {
		log.Debug().Msgf("PID: %v | PPID: %v", os.Getpid(), os.Getppid())
	}

	log.Info().Msg("Starting wire...")

	var pl *pipeline.Pipeline
	if cfg.InputFile != "" {
		info, err := os.Stat(cfg.InputFile)
		if err != nil {
			log.Fatal().Str("path", cfg.InputFile).Msg("--input file does not exist")
		}
		if info.IsDir() {
			log.Fatal().Str("path", cfg.InputFile).Msg("--input path is a directory, expected a JSONL file")
		}
		fmt.Printf("Starting JSONL pipeline: %s → TumblingWindowSink (window=%s)\n", cfg.InputFile, cfg.WindowSize)
		fmt.Println("---")
		pl, err = pipeline.BuildJSONLPipeline(mainCtx, pipeline.JSONLPipelineConfig{
			InputPath:  cfg.InputFile,
			OutputPath: cfg.OutputFile,
			WindowSize: cfg.WindowSize,
		})
	} else {
		fmt.Println("Starting demo pipeline: GeneratorSource → ToUpperMap → StdoutSink")
		fmt.Println("---")
		pl, err = pipeline.BuildDemoPipeline(mainCtx, cfg.EventCount)
	}
	if err != nil {
		log.Fatal().Err(err).Msg("failed to build pipeline")
	}

	if err := pl.Run(mainCtx); err != nil {
		log.Fatal().Err(err).Msg("pipeline failed")
	}

	fmt.Println("---")
	fmt.Println("Pipeline complete.")
}
