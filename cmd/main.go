package main

import (
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	pipeline "github.com/tgk/wire/pipeline"
	server "github.com/tgk/wire/server"
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
	signal.Notify(done, os.Interrupt)


	// Run the web server
	go func(ko *koanf.Koanf) {
		log.Info().Msg("Starting the web server...")
		server.Init(ko)
		server.Run(done, ko)
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

	for k,v := range mappedDataPipelines {
		log.Debug().Msgf("Key: %s | Value: %v", k, v)
		newPipeline := pipeline.NewDataPipeline(v.Source, v.Sink)
		pipelineString, err := newPipeline.Show()
		if err != nil {
			log.Err(err).Send()
		}
		log.Debug().Msgf("Creating and running pipeline: %s", pipelineString)

		go newPipeline.Run(done)
	}

	func (){
		time.Sleep(10*time.Second)
		close(done)
	}()

	<-done
	log.Info().Msg("1.received interrupt signal; closing client")
	// close(done)
	// time.Sleep(10 * time.Second)
	go func() {
		log.Info().Msg("2.received interrupt signal; closing client")
		defer close(done)
	}()

	// sigs := make(chan os.Signal, 2)
	// signal.Notify(sigs, os.Interrupt)

	// <-sigs // Wait in def until some signal comes your way
	// log.Info().Msg("received interrupt signal; closing client")
	// done := make(chan struct{})
	// go func() {
	// 	defer close(done)
	// }()

	// select {
	// case <-sigs: // If this is received twice
	// 	log.Info().Msg("received second interrupt signal; quitting without waiting for graceful close")
	// case <-done:
	// }

}
