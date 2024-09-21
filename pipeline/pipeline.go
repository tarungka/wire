package pipeline

import (
	"context"
	"os"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"
)

type DataSource interface {

	// Parse and configure the Source
	Init(args sources.SourceConfig) error

	// Connect to the source
	Connect(context.Context) error

	// Read is responsible to create a write only channel that is accessible to
	// downstream stages and is the owner of the channel
	Read(context.Context, <-chan interface{}, *sync.WaitGroup) (<-chan []byte, error)

	// Get the key
	Key() (string, error)

	// Name of the Source
	Name() string

	// Info about he Source
	Info() string

	// Disconnect the application from the source
	Disconnect() error
}

type DataSink interface {
	Init(args sinks.SinkConfig) error
	Connect(context.Context) error
	Write(<-chan interface{}, *sync.WaitGroup, <-chan []byte) error
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

func (dp *DataPipeline) Run(done <-chan os.Signal, wg *sync.WaitGroup) {

	defer func() {
		log.Trace().Msgf("The RUN function is done/returning.[%v]", dp.Sink.Info())
		wg.Done()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	dp.cancel = cancel

	// Connect
	if sourceConnectError := dp.Source.Connect(ctx); sourceConnectError != nil {
		log.Err(sourceConnectError).Msg("Error when connecting to source")
	}

	if sinkConnectError := dp.Sink.Connect(ctx); sinkConnectError != nil {
		log.Err(sinkConnectError).Msg("Error when connecting to sink")
	}

	// TODO: The code to read the initial/existing data will come here

	// TODO: This code IMO will only hold good for low throughput scenarios
	// and does not scale when there are multiple pipelines running.
	dataChannel, err := dp.Source.Read(ctx, dp.done, wg)
	if err != nil {
		log.Err(err).Msg("Error when reading from the data source")
	}

	wg.Add(1)
	// Not going to send the context to the Sink as I only want to close the
	// sink when the upstream channel is closed and not when the context is invalidated
	// or closed/timed out.
	if err := dp.Sink.Write(dp.done, wg, dataChannel); err != nil {
		log.Err(err).Msg("Error when writing to the data sink")
	}

	// TODO: Why exactly am I blocking this function here?
	<-done
	log.Info().Msg("The RUN function is done!")
	dp.Close() // the context is cancelled in here
	// cancel()

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
	close(dp.done)

	// Cancel the context
	dp.cancel()

	dp.Source.Disconnect()
	dp.Sink.Disconnect()
	return false
}

func NewDataPipeline(source DataSource, sink DataSink) *DataPipeline {
	dataPipeline := &DataPipeline{
		Source: source,
		Sink:   sink,
	}
	dataPipeline.Init()

	// TODO: Remove this, code is only for testing
	// go func() {
	// 	time.Sleep(3 * time.Second)
	// 	dataPipeline.Close()
	// }()

	return dataPipeline
}
