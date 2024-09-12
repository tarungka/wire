package main

import (
	"github.com/rs/zerolog/log"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"
)

type DataSource interface {
	Init(args sources.SourceConfig) (error)
	Connect() (error)
	Read() (<- chan []byte, error)
	Key() (string, error)
	Name() (string)
	Info() (string)
	Close() error
}

type DataSink interface {
	Init(args sinks.SinkConfig) (error)
	Connect() (error)
	Write(data <- chan []byte) error
	Key() (string, error)
	Name() (string)
	Info() (string)
	Close() error
}

type DataPipeline struct {
	Source DataSource
	Sink   DataSink
}

func (dp *DataPipeline) Run() error {

	// Connect
	if sourceConnectError := dp.Source.Connect(); sourceConnectError != nil {
		log.Err(sourceConnectError).Msg("Error when connecting to source")
		// panic(sourceConnectError)
	}

	if sinkConnectError := dp.Sink.Connect(); sinkConnectError != nil {
		log.Err(sinkConnectError).Msg("Error when connecting to sink")
		// panic(sinkConnectError)
	}


	// TODO: The code to read the initial/existing data will come here


	// TODO: This code IMO will only hold good for low throughput scenarios
	// and does not scale when there are multiple pipelines running.
    dataChannel, err := dp.Source.Read()
    if err != nil {
        return err
    }

    if err := dp.Sink.Write(dataChannel); err != nil {
        return err
    }

    return nil
}

func (dp *DataPipeline) Show() (string, error) {
	return dp.Source.Name() + " -> " + dp.Sink.Name(), nil
}

func newDataPipeline(source DataSource, sink DataSink) *DataPipeline {
	return &DataPipeline{
		Source: source,
		Sink:   sink,
	}
}
