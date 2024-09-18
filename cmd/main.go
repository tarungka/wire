package main

import (
	"fmt"
	"os"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	pipeline "github.com/tgk/wire/pipeline"
	server "github.com/tgk/wire/server"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"
)

var (
	buildString = "unknown"
	ko          = koanf.New(".")
)

func main() {

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

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

	go func(ko *koanf.Koanf) {
		log.Info().Msg("Starting the web server...")
		server.Init(ko)
		server.Run(ko)
	}(ko)
	// var done chan interface{}
	// <-done

	var allSourcesConfig []sources.SourceConfig
	var allSinksConfig []sinks.SinkConfig

	if err := ko.Unmarshal("sources", &allSourcesConfig); err != nil {
		log.Err(err).Msg("Error when un-marshaling sources")
	}
	if err := ko.Unmarshal("sinks", &allSinksConfig); err != nil {
		log.Err(err).Msg("Error when un-marshaling sinks")
	}

	var allSourceInterfaces []pipeline.DataSource
	var allSinkInterfaces []pipeline.DataSink

	for _, sourceConfig := range allSourcesConfig {
		eachSourceInterface, err := pipeline.DataSourceFactory(sourceConfig)
		if err != nil {
			log.Err(err).Send()
		}
		allSourceInterfaces = append(allSourceInterfaces, eachSourceInterface)
	}

	for _, sinkConfig := range allSinksConfig {
		eachSinkInterface, err := pipeline.DataSinkFactory(sinkConfig)
		if err != nil {
			log.Err(err).Send()
		}
		allSinkInterfaces = append(allSinkInterfaces, eachSinkInterface)
	}

	allSourcesAndSinks, err := pipeline.CreateSourcesAndSinksConfigs(allSourceInterfaces, allSinkInterfaces)
	if err != nil {
		log.Panic().Err(err).Msg("Internal server error!")
	}

	mappedDataPipelines, err := allSourcesAndSinks.GetPipelineConfigs()
	if err != nil {
		log.Panic().Err(err).Send()
	}

	for index, eachPipeline := range mappedDataPipelines {
		newPipeline := pipeline.NewDataPipeline(eachPipeline.Source, eachPipeline.Sink)
		pipelineString, err := newPipeline.Show()
		if err != nil {
			log.Err(err).Send()
		}
		log.Debug().Msgf("%d. Creating and running pipeline: %s", index, pipelineString)

		pipelineError := newPipeline.Run()
		if pipelineError != nil {
			log.Err(err).Msg("Error when running the pipeline")
		}
	}
}
