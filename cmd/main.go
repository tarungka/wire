package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	pipeline "github.com/tgk/wire/pipeline"
	"github.com/tgk/wire/server"
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

	log.Info().Msg("Starting the application")

	// This way the command line arguments are overridden by the remote/other configs
	if ko.Bool("override") {
		if initError := initConfig(ko); initError != nil {
			log.Err(initError).Msg("Error when initializing the config!")
		}
	}

	done := make(chan os.Signal, 1)
	// signal.Notify(done, os.Interrupt)

	var wg sync.WaitGroup

	// Run the web server
	go func(ko *koanf.Koanf) {
		log.Info().Msg("Starting the web server...")
		server.Init(ko)
		server.Run(done, &wg, ko)
	}(ko)

	var pipelineObject pipeline.PipelineDataObject

	allSourcesConfig, allSinksConfig, err := pipelineObject.ParseConfig(ko)
	if err != nil {
		log.Err(err).Msg("Error when reading config")
	}

	for _, sourceConfig := range allSourcesConfig {
		pipelineObject.AddSource(sourceConfig)
	}
	for _, sinkConfig := range allSinksConfig {
		pipelineObject.AddSink(sinkConfig)
	}

	mappedDataPipelines, exists := pipelineObject.GetMappedPipelines()
	if !exists {
		log.Debug().Msg("No data pipelines exist")
	}

	for k, v := range mappedDataPipelines {
		log.Debug().Msgf("Key: %s | Value: %v", k, v)
		newPipeline := pipeline.NewDataPipeline(v.Source, v.Sink)
		pipelineString, err := newPipeline.Show()
		if err != nil {
			log.Err(err).Send()
		}
		log.Debug().Msgf("Creating and running pipeline: %s", pipelineString)

		wg.Add(1)
		go newPipeline.Run(done, &wg)
	}

	// func() {
	// 	time.Sleep(5 * time.Second)
	// 	log.Trace().Msg("FIVE seconds are DONE!")
	// 	// done<-os.Interrupt
	// 	signal.Notify(done, os.Interrupt)
	// 	<-done
	// 	close(done)
	// }()

	// Wait for an interrupt signal (Ctrl+C)
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, os.Interrupt)
	<-signalChannel // Blocks until an interrupt signal is received

	fmt.Println("\nReceived interrupt signal, closing all goroutines...")

	// Close the done channel to signal all goroutines to exit
	close(done)

	wg.Wait()

	log.Info().Msg("received interrupt signal; closing client")
}
