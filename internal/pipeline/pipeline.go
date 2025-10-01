package pipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
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
		return
	}

	// Connect to sink
	if sinkConnectError := dp.Sink.Connect(ctx); sinkConnectError != nil {
		log.Err(sinkConnectError).Msg("Error when connecting to sink")
		return
	}

	initialDataChannel, err := dp.Source.LoadInitialData(ctx, &wg)
	if err != nil {
		log.Err(err).Msg("Error when loading initial data")
		return
	}

	dataChannel, err := dp.Source.Read(ctx, &wg)
	if err != nil {
		log.Err(err).Msg("Error when reading from the data source")
		return
	}

	hashFn := partitioner.HashFnv
	jobPartitioner := partitioner.NewPartitoner[*models.Job](dp.jobCount, hashFn)
	partitionedInitialDataChannels := jobPartitioner.PartitionData(initialDataChannel)
	partitionedDataChannels := jobPartitioner.PartitionData(dataChannel)

	t := &transform.Transformer{}
	t.Init()

	log.Debug().Msgf("Creating %d jobs", dp.jobCount)
	for i := range dp.jobCount {
		wg.Add(1)
		go dp.processJob(ctx, &wg, t, partitionedDataChannels[i], partitionedInitialDataChannels[i])
	}

	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()

	select {
	case <-ctx.Done():
		<-waitCh
	case <-waitCh:
	}
}

func (dp *DataPipeline) processJob(ctx context.Context, wg *sync.WaitGroup, t *transform.Transformer, dataChannel <-chan *models.Job, initialDataChannel <-chan *models.Job) {
	defer wg.Done()
	log.Debug().Msg("In a process job")

	initialTransformedChannel := toUpperCaseJSON(ctx, initialDataChannel)
	transformedChannel := toUpperCaseJSON(ctx, dataChannel)

	if err := dp.Sink.Write(ctx, transformedChannel, initialTransformedChannel); err != nil {
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

	if len(dp.operations) > 0 {
		latest := dp.operations[len(dp.operations)-1]
		latest.children = append(latest.children, &PipelineNode{node: []*PipelineOps{opsNode}})
		opsNode.parents = append(opsNode.parents, &PipelineNode{node: []*PipelineOps{latest}})
	}

	dp.operations = append(dp.operations, opsNode)
	return dp, nil
}

// Close the data pipeline
func (dp *DataPipeline) Close() bool {
	if dp.open.Load() {
		dpInfo, _ := dp.Show()
		log.Info().Msgf("Closing data pipeline: %s", dpInfo)

		dp.open.Store(false)
		if dp.cancel != nil {
			dp.cancel()
		}

		dp.Source.Disconnect()
		dp.Sink.Disconnect()
	}
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
		jobCount:   uint(runtime.NumCPU()),
		mu:         sync.RWMutex{},
		operations: []*PipelineOps{},
	}
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
