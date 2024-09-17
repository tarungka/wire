package main

import (
	"fmt"

	"github.com/rs/zerolog/log"
	sinks "github.com/tgk/wire/sinks"
	sources "github.com/tgk/wire/sources"
)

type dataPipelineObject struct {
	source DataSource
	sink   DataSink
}
type Config struct {
	sources    []DataSource
	sinks      []DataSink
	mappedConf []dataPipelineObject
}

func (c *Config) set(programSources []DataSource, programSinks []DataSink) error {
	c.sources = programSources
	c.sinks = programSinks
	return nil
}

func (c *Config) get() ([]DataSource, []DataSink, error) {
	return c.sources, c.sinks, nil
}

// func (c *Config) Key() (string, error) {
// 	return c.Key()
// }

func (c *Config) getPipelineConfigs() ([]dataPipelineObject, error) {
	log.Trace().Msg("Creating pipeline configs")
	var response []dataPipelineObject
	for _, src := range c.sources {
		for _, snk := range c.sinks {
			srcKey, err := src.Key()
			if err != nil {
				log.Panic().Err(err).Send()
			}
			snkKey, err := snk.Key()
			if err != nil {
				log.Panic().Err(err).Send()
			}
			if srcKey == snkKey {
				log.Trace().Msgf("Source:[%s] -> Sink:[%s]", src.Info(), snk.Info())
				response = append(response, dataPipelineObject{src, snk})
			}
		}
	}
	c.mappedConf = response
	return response, nil
}

func createSourcesAndSinksConfigs(programSources []DataSource, programSinks []DataSink) (*Config, error) {
	return &Config{
		sources: programSources,
		sinks:   programSinks,
	}, nil
}

// TODO: Move this to source dir
func dataSourceFactory(config sources.SourceConfig) (DataSource, error) {
	sourceType := config.ConnectionType
	log.Debug().Msgf("Creating and allocating object for source: %s", sourceType)
	switch sourceType {
	case "mongodb":
		x := &sources.MongoSource{}
		x.Init(config)
		return x, nil
	// case "mysql":
	//     return &MySQLSource{}, nil
	// Add other sources
	default:
		return nil, fmt.Errorf("unknown source type: %s", sourceType)
	}
}

// TODO: Move this to sink dir
func dataSinkFactory(config sinks.SinkConfig) (DataSink, error) {
	sinkType := config.ConnectionType
	log.Debug().Msgf("Creating and allocating object for sink: %s", sinkType)
	switch sinkType {
	case "elasticsearch":
		x := &sinks.ElasticSink{}
		x.Init(config)
		return x, nil
	case "kafka":
		x := &sinks.KafkaSink{}
		x.Init(config)
		return x, nil
	default:
		return nil, fmt.Errorf("unknown sink type: %s", sinkType)
	}
}
