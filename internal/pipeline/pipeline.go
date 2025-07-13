package pipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/models"
	"github.com/tarungka/wire/internal/partitioner"
	"github.com/tarungka/wire/internal/transform"
)

type Operation interface {
	ID() string
	Process(ctx context.Context, in <-chan *models.Job) <-chan *models.Job
}

type PipelineNode struct {
	node []*PipelineOps
}

type PipelineOps struct {
	id        string
	operation Operation

	parents  []*PipelineNode // We store parents to for backtracking
	children []*PipelineNode
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
	pipelineDone chan any
	// Mutex
	mu sync.RWMutex

	operations []*PipelineOps

	// Only needed when debugging
	counter uint64
}

func (dp *DataPipeline) Init() error {
	// dp.pipelineDone = make(chan interface{})
	return nil
}

func (dp *DataPipeline) incrementCounter() {
	dp.mu.Lock()
	defer dp.mu.Unlock()
	dp.counter += 1
}

func (dp *DataPipeline) getCounterValue() uint64 {
	dp.mu.RLock()
	defer dp.mu.RUnlock()
	return dp.counter
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
func (dp *DataPipeline) Run(pctx context.Context) {

	defer func() {
		log.Trace().Msgf("The RUN function is done/returning.[%v]", dp.Sink.Info())
	}()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(pctx) // create a new context with the parent context
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

	initialDataChannel, err := dp.Source.LoadInitialData(ctx, &wg)
	if err != nil {
		log.Err(err).Msg("Error when loading initial data")
	}

	// TODO: This code IMO will only hold good for low throughput scenarios
	// and does not scale when there are multiple pipelines running.
	dataChannel, err := dp.Source.Read(ctx, &wg)
	if err != nil {
		log.Err(err).Msg("Error when reading from the data source")
		return
	}

	// TODO: abstract this out of there, create a default hash function
	// and make this overrideable
	hashFn := partitioner.HashFnv

	// TODO: Implement code make the channel to a job and process the job
	// Partition the data into multiple jobs (channel)
	jobPartitioner := partitioner.NewPartitoner[*models.Job](dp.jobCount, hashFn)

	partitionedInitialDataChannels := jobPartitioner.PartitionData(initialDataChannel)
	partitionedDataChannels := jobPartitioner.PartitionData(dataChannel)

	jobPartitioner.Examine()

	// for i := 0; i < jobCount; i++ {
	// 	log.Debug().Printf("Channel [%v] = %v\n", i, partitionedInitialDataChannels[i])
	// 	fmt.Printf("|Channel [%v] = %v\n", i, partitionedDataChannels[i])
	// }

	t := &transform.Transformer{}
	t.Init()

	log.Debug().Msgf("Creating %d jobs", dp.jobCount)
	for i := range dp.jobCount {
		wg.Add(1)

		go dp.processJob(ctx, &wg, t, partitionedDataChannels[i], partitionedInitialDataChannels[i])
	}

	<-ctx.Done()
	wg.Wait()  // Wait till you finish reading and writing all the data
	dp.Close() // the pipeline context is cancelled in here
}

// Process job as of now only writes the data to the sink in a non deterministic manner
// i.e the writes can be in a different order to the reads
func (dp *DataPipeline) processJob(ctx context.Context, wg *sync.WaitGroup, t *transform.Transformer, dataChannel <-chan *models.Job, initialDataChannel <-chan *models.Job) {
	log.Debug().Msg("In a process job")
	log.Debug().Msgf("The wg and dataChannel are: %v | %v", wg, len(dataChannel))

	// TODO: need to add code to transform the input to the expected output
	// transform.ApplyTransformation()
	initialTransformedChannel := toUpperCaseJSON(ctx, initialDataChannel)
	// transformedChannel := t.ApplyTransformationJob(ctx, dataChannel)
	//
	// go dp.writeToFile(fmt.Sprintf("tests/test/initialTransformedChannel_%d.json", dp.getCounterValue()), initialTransformedChannel)
	// go dp.writeToFile(fmt.Sprintf("tests/test/transformedChannel_%d.json", dp.getCounterValue()), transformedChannel)

	// dp.incrementCounter()

	// TODO: wg.Done is called in Write, not very readable code, need to refactor this
	if err := dp.Sink.Write(ctx, wg, initialTransformedChannel, dataChannel); err != nil {
		log.Err(err).Msg("Error when writing to the data sink")
	}
}

func (dp *DataPipeline) writeToFile(fileName string, ch <-chan *models.Job) {
	file, err := os.Create(fileName) // Create or overwrite the file
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create file")
		return
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	encoder := json.NewEncoder(writer) // JSON encoder

	for job := range ch {
		// Encode job as a single-line JSON
		err := encoder.Encode(job)
		if err != nil {
			log.Err(err).Msg("Failed to write JSON to file")
		}
	}

	log.Debug().Msg(fmt.Sprintf("Finished writing jobs to %s", fileName))
}

// Key returns the key for the pipeline
func (dp *DataPipeline) Key() string {
	if dp.open.Load() {
		return ""
	}
	return dp.key
}

// Shows the `source name` -> `sink name`
func (dp *DataPipeline) Show() (string, error) {
	return dp.Source.Name() + " -> " + dp.Sink.Name(), nil
}

func (dp *DataPipeline) AddOperation(op Operation) (*DataPipeline, error) {
	dp.mu.Lock()
	defer dp.mu.Unlock()

	opsNode := &PipelineOps{
		id:        op.ID(),
		operation: op,
	}

	// TODO: update the parents and the children
	if len(dp.operations) > 0 {
		// do something
		latest := dp.operations[len(dp.operations)-1]
		latest.children = append(latest.children, &PipelineNode{node: []*PipelineOps{opsNode}})
		opsNode.parents = append(opsNode.parents, &PipelineNode{node: []*PipelineOps{latest}})
	}

	dp.operations = append(dp.operations, opsNode)
	return dp, nil
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
		Source:     source,
		Sink:       sink,
		open:       atomic.Bool{},
		cancel:     nil,
		key:        "",
		jobCount:   1,
		mu:         sync.RWMutex{},
		operations: []*PipelineOps{},
	}
	// dataPipeline.Init() // does nothing as of now

	// TODO: Remove this, code is only for testing
	// go func() {
	// 	time.Sleep(3 * time.Second)
	// 	dataPipeline.Close()
	// }()

	return dataPipeline
}

func toUpperCaseJSON(ctx context.Context, in <-chan *models.Job) <-chan *models.Job {
	out := make(chan *models.Job)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				log.Logger.Warn().Msg("Context cancelled in toUpperCaseJSON")
				return
			case job, ok := <-in:
				if !ok {
					return
				}

				data, err := job.GetData()
				if err != nil {
					log.Logger.Err(err).Msg("Error when getting the data!")
					continue
				}

				switch typed := data.(type) {
				case map[string]any, []any:
					log.Logger.Debug().Msgf("The input function data is: %v", typed)
					uppercaseJSON(typed)
					log.Logger.Debug().Msgf("The OUTPUT function data is: %v", typed)
					job.SetData(typed)
				}

				select {
				case out <- job:
				case <-ctx.Done():
					log.Logger.Warn().Msg("Context cancelled while sending job to output")
					return
				}
			}
		}
	}()
	return out
}

func uppercaseJSON(data any) {
	switch v := data.(type) {
	case map[string]any:
		for key, val := range v {
			switch valTyped := val.(type) {
			case string:
				v[key] = strings.ToUpper(valTyped)
			case map[string]any, []any:
				uppercaseJSON(valTyped)
			}
		}
	case []any:
		for _, val := range v {
			uppercaseJSON(val)
		}
	}
}
