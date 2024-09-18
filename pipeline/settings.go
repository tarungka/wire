package pipeline

import (
	"fmt"

	"github.com/rs/zerolog/log"
	sinks "github.com/tgk/wire/sinks"
	sources "github.com/tgk/wire/sources"
)

type DataPipelineObject struct {
	Source DataSource
	Sink   DataSink
}
type Config struct {
	Sources    []DataSource
	Sinks      []DataSink
	mappedConf []DataPipelineObject
}

func (c *Config) set(programSources []DataSource, programSinks []DataSink) error {
	c.Sources = programSources
	c.Sinks = programSinks
	return nil
}

func (c *Config) get() ([]DataSource, []DataSink, error) {
	return c.Sources, c.Sinks, nil
}

// func (c *Config) Key() (string, error) {
// 	return c.Key()
// }

func (c *Config) GetPipelineConfigs() ([]DataPipelineObject, error) {
	log.Trace().Msg("Creating pipeline configs")
	var response []DataPipelineObject
	log.Trace().Msgf("The number of sources and sinks are: %v, %v", len(c.Sources), len(c.Sinks))
	for _, src := range c.Sources {
		for _, snk := range c.Sinks {
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
				response = append(response, DataPipelineObject{src, snk})
			}
		}
	}
	c.mappedConf = response
	return response, nil
}

func CreateSourcesAndSinksConfigs(programSources []DataSource, programSinks []DataSink) (*Config, error) {
	return &Config{
		Sources: programSources,
		Sinks:   programSinks,
	}, nil
}

// TODO: Move this to source dir
func DataSourceFactory(config sources.SourceConfig) (DataSource, error) {
	sourceType := config.ConnectionType
	log.Debug().Msgf("Creating and allocating object for source: %s", sourceType)
	switch sourceType {
	case "mongodb":
		x := &sources.KafkaSource{}
		x.Init(config)
		return x, nil
	case "kafka":
		x := &sources.KafkaSource{}
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
func DataSinkFactory(config sinks.SinkConfig) (DataSink, error) {
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
