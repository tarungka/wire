package pipeline

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/sinks"
	"github.com/tarungka/wire/sources"
)

type DataSource interface {

	// Parse and configure the Source
	Init(args sources.SourceConfig) error

	// Connect to the Source
	Connect(context.Context) error

	// Load all initial data from the source
	// There are exceptions to this, i.e kafka
	LoadInitialData(context.Context, <-chan interface{}, *sync.WaitGroup) (<-chan []byte, error)

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

	// Parse and configure the Sink
	Init(args sinks.SinkConfig) error

	// Connect to the Sink
	Connect(context.Context) error

	// Write is responsible to read data from the upstream input channel and
	// write data to the sink
	Write(<-chan interface{}, *sync.WaitGroup, <-chan []byte, <-chan []byte) error

	// Get the key
	Key() (string, error)

	// Name of the Sink
	Name() string

	// Info about he Sink
	Info() string

	// Disconnect the application from the sink
	Disconnect() error
}

type DataPipeline struct {
	Source       DataSource
	Sink         DataSink
	cancel       context.CancelFunc
	key          string
	pipelineDone chan interface{}
}

func (dp *DataPipeline) Init() error {
	// dp.pipelineDone = make(chan interface{})
	return nil
}

// Set the source of the data pipeline
func (d *DataPipeline) SetSource(source DataSource) {
	log.Trace().Msgf("Setting source %s", source.Info())
	d.Source = source
}

// Set the sink of the data pipeline
func (d *DataPipeline) SetSink(sink DataSink) {
	log.Trace().Msgf("Setting sink %s", sink.Info())
	d.Sink = sink

	log.Debug().Msgf("DataPipelineObject: %v", d)
}

// Run the data pipeline, connects to the source and sink. Reads data from the source
// then writes the data to the sink.
func (dp *DataPipeline) Run(done <-chan interface{}, wg *sync.WaitGroup) {

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
	initialDataChannel, err := dp.Source.LoadInitialData(ctx, done, wg)
	if err != nil {
		log.Err(err).Msg("Error when loading initial data")
	}

	// TODO: This code IMO will only hold good for low throughput scenarios
	// and does not scale when there are multiple pipelines running.
	dataChannel, err := dp.Source.Read(ctx, done, wg)
	if err != nil {
		log.Err(err).Msg("Error when reading from the data source")
		return
	}

	wg.Add(1)
	// Not going to send the context to the Sink as I only want to close the
	// sink when the upstream channel is closed and not when the context is invalidated
	// or closed/timed out.
	if err := dp.Sink.Write(done, wg, dataChannel, initialDataChannel); err != nil {
		log.Err(err).Msg("Error when writing to the data sink")
	}

	<-done
	dp.Close() // the context is cancelled in here
}

// Shows the `source name` -> `sink name`
func (dp *DataPipeline) Show() (string, error) {
	return dp.Source.Name() + " -> " + dp.Sink.Name(), nil
}

// Close the data pipeline
func (dp *DataPipeline) Close() bool {
	dpInfo, _ := dp.Show()
	log.Info().Msgf("Closing data pipeline: %s", dpInfo)
	// close(dp.pipelineDone)

	// Cancel the context
	dp.cancel()

	dp.Source.Disconnect()
	dp.Sink.Disconnect()
	return false
}

// Create a new DataPipeline and initialize it
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
