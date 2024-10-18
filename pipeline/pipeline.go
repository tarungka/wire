package pipeline

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/partitioner"
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
	LoadInitialData(context.Context, *sync.WaitGroup) (<-chan []byte, error)

	// Read is responsible to create a write only channel that is accessible to
	// downstream stages and is the owner of the channel
	Read(context.Context, *sync.WaitGroup) (<-chan []byte, error)

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
	Write(context.Context, *sync.WaitGroup, <-chan []byte, <-chan []byte) error

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

	// pipeline is running
	open atomic.Bool
	// A data source object
	Source DataSource
	// A data sink object
	Sink DataSink
	// context for the datapipeline
	cancel context.CancelFunc
	// Unique identifier for the data pipeline
	key string
	// Num jobs
	jobCount uint
	// To shutdown only the pipeline
	pipelineDone chan interface{}
	// Mutex
	mu sync.Mutex
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
func (dp *DataPipeline) Run(pctx context.Context, wg *sync.WaitGroup) {

	defer func() {
		log.Trace().Msgf("The RUN function is done/returning.[%v]", dp.Sink.Info())
		wg.Done()
	}()

	ctx, cancel := context.WithCancel(pctx)
	dp.cancel = cancel

	dp.open.Store(true) // pipeline is running

	// Connect to source
	if sourceConnectError := dp.Source.Connect(ctx); sourceConnectError != nil {
		log.Err(sourceConnectError).Msg("Error when connecting to source")
	}

	// Connect to sink
	if sinkConnectError := dp.Sink.Connect(ctx); sinkConnectError != nil {
		log.Err(sinkConnectError).Msg("Error when connecting to sink")
	}

	initialDataChannel, err := dp.Source.LoadInitialData(ctx, wg)
	if err != nil {
		log.Err(err).Msg("Error when loading initial data")
	}

	// TODO: This code IMO will only hold good for low throughput scenarios
	// and does not scale when there are multiple pipelines running.
	dataChannel, err := dp.Source.Read(ctx, wg)
	if err != nil {
		log.Err(err).Msg("Error when reading from the data source")
		return
	}

	// TODO: abstract this out of there, create a default hash function
	// and make this overrideable
	hashFn := partitioner.HashFnv

	// TODO: Implement code make the channel to a job and process the job
	// Partition the data into multiple jobs (channel)
	jobCount := 5 // Number of concurrent jobs

	jobPartitioner := partitioner.NewPartitoner(jobCount, hashFn) // data type is []byte or uint8

	partitionedInitialDataChannels := jobPartitioner.PartitionData(initialDataChannel)
	partitionedDataChannels := jobPartitioner.PartitionData(dataChannel)

	// jobPartitioner.Examine()

	// for i := 0; i < jobCount; i++ {
	// 	fmt.Printf("Channel [%v] = %v\n", i, partitionedInitialDataChannels[i])
	// 	fmt.Printf("|Channel [%v] = %v\n", i, partitionedDataChannels[i])
	// }

	for i := 0; i < jobCount; i++ {
		wg.Add(1)
		go dp.processJob(ctx, wg, partitionedDataChannels[i], partitionedInitialDataChannels[i])
	}

	<-ctx.Done()
	dp.Close() // the context is cancelled in here
}

// Process job as of now only writes the data to the sink in a non deterministic manner
// i.e the writes can be in a different order to the reads
func (dp *DataPipeline) processJob(ctx context.Context, wg *sync.WaitGroup, dataChannel <-chan []byte, initialDataChannel <-chan []byte) {
	// defer wg.Done()

	// TODO: wg.Done is called in Write, not very readable code, need to refactor this
	if err := dp.Sink.Write(ctx, wg, dataChannel, initialDataChannel); err != nil {
		log.Err(err).Msg("Error when writing to the data sink")
	}
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

	dp.open.Store(false)

	// Cancel the context
	dp.cancel()

	dp.Source.Disconnect()
	dp.Sink.Disconnect()
	return false
}

// Create a new DataPipeline and initialize it
func NewDataPipeline(source DataSource, sink DataSink) *DataPipeline {
	dataPipeline := &DataPipeline{
		Source:   source,
		Sink:     sink,
		open:     atomic.Bool{},
		cancel:   nil,
		key:      "",
		jobCount: 4,
		mu:       sync.Mutex{},
	}
	// dataPipeline.Init() // does nothing as of now

	// TODO: Remove this, code is only for testing
	// go func() {
	// 	time.Sleep(3 * time.Second)
	// 	dataPipeline.Close()
	// }()

	return dataPipeline
}
