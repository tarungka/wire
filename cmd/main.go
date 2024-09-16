package main

import (
	"fmt"
	"os"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

	if initError := initConfig(ko); initError != nil {
		log.Err(initError).Msg("Error when initializing the config!")
	}

	var allSourcesConfig []sources.SourceConfig
	var allSinksConfig []sinks.SinkConfig

	ko.Unmarshal("sources", &allSourcesConfig)
	ko.Unmarshal("sinks", &allSinksConfig)

	var allSourceInterfaces []DataSource
	var allSinkInterfaces []DataSink

	for _, sourceConfig := range allSourcesConfig {
		eachSourceInterface, err := dataSourceFactory(sourceConfig)
		if err != nil {
			log.Err(err).Send()
		}
		allSourceInterfaces = append(allSourceInterfaces, eachSourceInterface)
	}

	for _, sinkConfig := range allSinksConfig {
		eachSinkInterface, err := dataSinkFactory(sinkConfig)
		if err != nil {
			log.Err(err).Send()
		}
		allSinkInterfaces = append(allSinkInterfaces, eachSinkInterface)
	}

	allSourcesAndSinks, err := createSourcesAndSinksConfigs(allSourceInterfaces, allSinkInterfaces)
	if err != nil {
		log.Panic().Err(err).Msg("Internal server error!")
	}

	mappedDataPipelines, err := allSourcesAndSinks.getPipelineConfigs()
	if err != nil {
		log.Panic().Err(err).Send()
	}

	for index, pipeline := range mappedDataPipelines {
		newPipeline := newDataPipeline(pipeline.source, pipeline.sink)
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
