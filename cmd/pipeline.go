package main

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"
)

type DataSource interface {
	Init(args sources.SourceConfig) error
	Connect(context.Context) (error)
	Read(context.Context, <-chan interface{}) (<-chan []byte, error)
	Key() (string, error)
	Name() string
	Info() string
	Disconnect() error
}

type DataSink interface {
	Init(args sinks.SinkConfig) error
	Connect(context.Context) error
	Write(<-chan interface{}, <-chan []byte) error
	Key() (string, error)
	Name() string
	Info() string
	Disconnect() error
}

type DataPipeline struct {
	Source DataSource
	Sink   DataSink
	done   chan interface{}
	cancel context.CancelFunc
}

func (dp *DataPipeline) Run() error {

	ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()

	// Connect
	sourceConnectError := dp.Source.Connect(ctx)
	if sourceConnectError != nil {
		log.Err(sourceConnectError).Msg("Error when connecting to source")
		// panic(sourceConnectError)
	}
	dp.cancel = cancel

	if sinkConnectError := dp.Sink.Connect(ctx); sinkConnectError != nil {
		log.Err(sinkConnectError).Msg("Error when connecting to sink")
		// panic(sinkConnectError)
	}

	// TODO: The code to read the initial/existing data will come here

	// TODO: This code IMO will only hold good for low throughput scenarios
	// and does not scale when there are multiple pipelines running.
	dataChannel, err := dp.Source.Read(ctx, dp.done)
	if err != nil {
		return err
	}


	// Not going to send the context to the Sink as I only want to close the
	// sink when the upstream channel is closed and not when the context is invalidated
	// or closed/timed out.
	if err := dp.Sink.Write(dp.done, dataChannel); err != nil {
		return err
	}

	return nil
}

func (dp *DataPipeline) Show() (string, error) {
	return dp.Source.Name() + " -> " + dp.Sink.Name(), nil
}

func (dp *DataPipeline) Init() error {
	dp.done = make(chan interface{})
	return nil
}

func (dp *DataPipeline) Close() bool {
	dpInfo, _ := dp.Show()
	log.Info().Msgf("Closing data pipeline: %s", dpInfo)
	dp.Source.Disconnect()
	dp.Sink.Disconnect()
	close(dp.done)

	// Cancel the context
	dp.cancel()
	return false
}

func newDataPipeline(source DataSource, sink DataSink) *DataPipeline {
	dataPipeline := &DataPipeline{
		Source: source,
		Sink:   sink,
	}
	dataPipeline.Init()

	go func() {
		time.Sleep(1 * time.Second)
		dataPipeline.Close()
	}()

	return dataPipeline
}
