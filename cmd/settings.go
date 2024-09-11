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

func dataSourceFactory(config sources.SourceConfig) (DataSource, error) {
	sourceType := config.ConnectionType
	switch sourceType {
	case "mongodb":
		// keys := make([]string, 0, len(configs))
		// for k := range configs {
		// 	keys = append(keys, k)
		// }

		// configs := config.Config
		// uri := configs["uri"]
		// database := configs["database"]
		// collection := configs["collection"]
		// fmt.Println(uri, database, collection)

		x := &sources.MongoSource{}
		x.Init(config)
		return x, nil
		// return &sources.MongoSource{
		// 	MongoDbUri: uri,
		// 	MongoDbDb:  database,
		// 	MongoDbCol: collection,
		//     PipelineKey: config.Key,

		// }, nil
	// case "mysql":
	//     return &MySQLSource{}, nil
	// Add other sources
	default:
		return nil, fmt.Errorf("unknown source type: %s", sourceType)
	}
}

func dataSinkFactory(config sinks.SinkConfig) (DataSink, error) {
	sinkType := config.ConnectionType
	switch sinkType {
	case "elasticsearch":
		x := &sinks.ElasticSink{}
		x.Init(config)
		return x, nil
		// return &sinks.ElasticSink{}, nil
	// case "kafka":
	//     return &KafkaSink{}, nil
	// Add other sinks
	default:
		return nil, fmt.Errorf("unknown sink type: %s", sinkType)
	}
}
