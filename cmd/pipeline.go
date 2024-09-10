package main

import "github.com/rs/zerolog/log"

type DataSource interface {
	Connect() (error)
	Read() ([]byte, error)
	Close() error
}

type DataSink interface {
	Connect() (error)
	Write(data []byte) error
	Close() error
}

type DataPipeline struct {
	Source DataSource
	Sink   DataSink
}

func (dp *DataPipeline) Run() error {

	// Init
	if sourceConnectError := dp.Source.Connect(); sourceConnectError != nil {
		log.Err(sourceConnectError).Msg("Error when connecting to source")
		panic(sourceConnectError)
	}

	if sinkConnectError := dp.Sink.Connect(); sinkConnectError != nil {
		log.Err(sinkConnectError).Msg("Error when connecting to source")
		panic(sinkConnectError)
	}


	// TODO: The code to read the initial/existing data will come here

    data, err := dp.Source.Read()
    if err != nil {
        return err
    }

    if err := dp.Sink.Write(data); err != nil {
        return err
    }

    return nil
}

func newDataPipeline(source DataSource, sink DataSink) *DataPipeline {
	return &DataPipeline{
		Source: source,
		Sink:   sink,
	}
}
