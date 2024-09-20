package pipeline

import (
	"github.com/rs/zerolog/log"
)

type DataPipelineObject struct {
	key    string
	Source DataSource
	Sink   DataSink
}

func (d *DataPipelineObject) SetSource(source DataSource) {
	log.Trace().Msgf("Setting source %s", source.Info())
	d.Source = source
}
func (d *DataPipelineObject) SetSink(sink DataSink) {
	log.Trace().Msgf("Setting sink %s", sink.Info())
	d.Sink = sink

	log.Debug().Msgf("DataPipelineObject: %v", d)
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
				response = append(response, DataPipelineObject{srcKey, src, snk})
			}
		}
	}
	c.mappedConf = response
	return response, nil
}
