// TODO: Horrible file name, change it later
package pipeline

import (
	"fmt"
	"sync"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/sinks"
	"github.com/tarungka/wire/sources"
)

// currently working on adding metadata to the running pipelines
// and their statuses for diagnosis

type DataPipelineConfig struct {
	allSourceInterfaces []DataSource            // All the data sources in an array
	allSinkInterfaces   []DataSink              // All the data sinks in an array
	srcIndexMap         map[string][]int        // key : [indices of where the source is in the allSourceInterfaces]
	snkIndexMap         map[string][]int        // key : [indices of where the sink is in the allSinkInterfaces]
	keys                map[string]bool         // Keys: ifExists; value can be false only when key is deleted after creation
	mappedDataPipelines map[string]DataPipeline // Mapping of {key: DataPipeline}
}

// PipelineConfig struct as defined in the issue
type PipelineConfig struct {
	Name        string `yaml:"name" json:"name"`
	Parallelism int    `yaml:"parallelism" json:"parallelism" validate:"min=1,max=1000"`
	BufferSize  int    `yaml:"buffer_size" json:"buffer_size" validate:"min=1,max=10000"`
}

type DataPipelineManager struct {
	activePipelines uint32              // current actively running pipelines
	configs         *DataPipelineConfig // will probably move this over to badger

}

var (
	pipelineInstance *DataPipelineConfig // holds the singleton value
	once             sync.Once           // Setting the scope of this to the global package
)

// Add a new key to the config, if exists does nothing
func (p *DataPipelineConfig) addKey(s string) {
	if p.keys == nil {
		p.keys = make(map[string]bool)
	}

	if !p.keys[s] {
		p.keys[s] = true
	}
}

// Parse the config and return array or sources and sinks
func (p *DataPipelineConfig) ParseConfig(ko *koanf.Koanf) ([]sources.SourceConfig, []sinks.SinkConfig, error) {
	var allSourcesConfig []sources.SourceConfig
	var allSinksConfig []sinks.SinkConfig

	if err := ko.Unmarshal("sources", &allSourcesConfig); err != nil {
		log.Err(err).Msg("Error when un-marshaling sources")
		return nil, nil, err
	}
	if err := ko.Unmarshal("sinks", &allSinksConfig); err != nil {
		log.Err(err).Msg("Error when un-marshaling sinks")
		return nil, nil, err
	}

	return allSourcesConfig, allSinksConfig, nil
}

// Parse the config and read the sources and sinks into the pipeline config
func (p *DataPipelineConfig) Config(ko *koanf.Koanf) (bool, error) {
	var pipelineConfigs []PipelineConfig
	if err := ko.Unmarshal("pipelines", &pipelineConfigs); err != nil {
		log.Err(err).Msg("Error when un-marshaling pipeline configs")
		// We can decide to return error or proceed without pipeline-specific configs
	}

	// Create a map for quick lookup of pipeline configurations by name
	pipelineConfigMap := make(map[string]PipelineConfig)
	for _, pc := range pipelineConfigs {
		pipelineConfigMap[pc.Name] = pc
	}

	var allSourcesConfig []sources.SourceConfig
	var allSinksConfig []sinks.SinkConfig

	if err := ko.Unmarshal("sources", &allSourcesConfig); err != nil {
		log.Err(err).Msg("Error when un-marshaling sources")
		return false, err
	}
	if err := ko.Unmarshal("sinks", &allSinksConfig); err != nil {
		log.Err(err).Msg("Error when un-marshaling sinks")
		return false, err
	}

	for _, sourceConfig := range allSourcesConfig {
		// Here we assume that sourceConfig.Name can be used to find a matching PipelineConfig
		// This might need adjustment based on how pipeline names are associated with sources/sinks
		pipelineName := sourceConfig.Name // Or some other field that links source to pipeline config
		parallelism := 1                   // Default parallelism
		if pc, ok := pipelineConfigMap[pipelineName]; ok {
			parallelism = pc.Parallelism
		}
		p.AddSource(sourceConfig, parallelism)
	}
	for _, sinkConfig := range allSinksConfig {
		// Sinks might not directly determine parallelism, it's more a pipeline-level attribute.
		// The current AddSink doesn't take parallelism. This logic assumes that the DataPipeline
		// object is primarily configured via AddSource or a separate pipeline construction step.
		// If a sink needs to be associated with a pipeline's parallelism, this needs more thought.
		p.AddSink(sinkConfig)
	}

	// This part of the logic might need to be more sophisticated.
	// Currently, mappedDataPipelines are created/updated in AddSource/AddSink.
	// We need to ensure the parallelism setting from PipelineConfig is applied to the correct DataPipeline.
	// One way is to pass parallelism to NewDataPipeline, which is called within mapSource/mapSink indirectly or directly.

	// Let's adjust mapSource and potentially mapSink to handle parallelism.
	// The issue is that DataPipeline objects are created inside mapSource/mapSink.
	// We need to ensure the parallelism value reaches NewDataPipeline.

	// For simplicity, let's assume that a pipeline is uniquely identified by a key (often derived from source/sink name)
	// and that PipelineConfig's Name field matches this key.
	for key, dp := range p.mappedDataPipelines {
		if pc, ok := pipelineConfigMap[key]; ok {
			// If NewDataPipeline was already called (e.g. in mapSource/mapSink),
			// we might need to update the existing DataPipeline object's parallelism.
			// Or, ensure NewDataPipeline is called with the correct parallelism.
			// The current structure creates DataPipeline in mapSource/mapSink without parallelism.
			// This needs careful refactoring.

			// A temporary approach: if dp.parallelism is still default (1), update it.
			// This assumes NewDataPipeline sets a default that we can check.
			// And that this Config method is called after initial mapping.
			if dp.parallelism == 1 && pc.Parallelism > 0 { // Check if default and new value is valid
				// This direct modification is tricky because DataPipeline is a value in the map.
				// Need to update the map value itself.
				updatedDp := dp
				updatedDp.parallelism = pc.Parallelism
				p.mappedDataPipelines[key] = updatedDp
				log.Debug().Msgf("Updated parallelism for pipeline %s to %d", key, pc.Parallelism)
			}
		}
	}

	return true, nil
}

func (p *DataPipelineConfig) mapSource(source DataSource, parallelism int) {

	log.Trace().Msg("Mapping source")

	if p.mappedDataPipelines == nil {
		log.Debug().Msg("mappedDataPipelines is nil, making it")
		p.mappedDataPipelines = make(map[string]DataPipeline)
	}

	key, _ := source.Key()

	if value, exists := p.mappedDataPipelines[key]; exists {
		log.Debug().Msgf("Mapped source key(%s) exists, updating it", key)
		value.SetSource(source) // Updated the local copy
		// If parallelism is provided and different from existing, update it.
		// This assumes DataPipeline struct has a parallelism field.
		if parallelism > 0 && value.parallelism != parallelism {
			log.Debug().Msgf("Updating parallelism for existing pipeline %s from %d to %d", key, value.parallelism, parallelism)
			value.parallelism = parallelism
		}
		p.mappedDataPipelines[key] = value // Updating the main object
		return
	}

	log.Debug().Msgf("Mapped source key(%s) does NOT exists, creating it", key)
	// Create DataPipeline with provided parallelism
	data := NewDataPipeline(source, nil, parallelism) // Assuming NewDataPipeline signature is NewDataPipeline(source, sink, parallelism)
	data.key = key                                     // Set key if not set by NewDataPipeline or if specific keying is needed
	p.mappedDataPipelines[key] = *data                 // Store the actual DataPipeline object
}

func (p *DataPipelineConfig) mapSink(sink DataSink) {

	log.Trace().Msg("Mapping sink")

	if p.mappedDataPipelines == nil {
		log.Debug().Msg("mappedDataPipelines is nil, making it")
		p.mappedDataPipelines = make(map[string]DataPipeline)
	}

	key, _ := sink.Key()
	log.Debug().Msgf("mappedDataPipeline: %v", p.mappedDataPipelines)
	if value, exists := p.mappedDataPipelines[key]; exists {
		log.Debug().Msgf("Mapped sink key(%s) exists, updating it", key)

		// log.Debug().Msgf("Value before: %v", value)
		value.SetSink(sink)
		// log.Debug().Msgf("Value after: %v", value)

		// TODO: Understand better and change this approach, maybe
		// As I have created a local copy of the data and then made edits on it
		// I have to update the old data with the new data
		p.mappedDataPipelines[key] = value
		return
	}

	log.Debug().Msgf("Mapped sink key(%s) does NOT exists, creating it", key)
	data := DataPipeline{
		key:    key,
		Source: nil,
		Sink:   sink,
	}
	p.mappedDataPipelines[key] = data
}

// Add a source to the pipeline config
func (p *DataPipelineConfig) AddSource(src sources.SourceConfig, parallelism int) error {

	log.Trace().Msg("Creating a source")

	if p.srcIndexMap == nil {
		p.srcIndexMap = make(map[string][]int)
	}

	source, err := DataSourceFactory(src)
	if err != nil {
		log.Err(err).Msg("Error when creation Data Source Object")
		return err
	}
	key, _ := source.Key()
	p.srcIndexMap[key] = append(p.srcIndexMap[key], len(p.allSourceInterfaces))
	p.allSourceInterfaces = append(p.allSourceInterfaces, source)
	p.addKey(key)
	p.mapSource(source, parallelism) // Pass parallelism to mapSource
	return nil
}

// Add a sink to the pipeline config
func (p *DataPipelineConfig) AddSink(snk sinks.SinkConfig) error {

	if p.snkIndexMap == nil {
		p.snkIndexMap = make(map[string][]int)
	}

	sink, err := DataSinkFactory(snk)
	if err != nil {
		log.Err(err).Msg("Error when creation Data Source Object")
		return err
	}
	key, _ := sink.Key()
	p.snkIndexMap[key] = append(p.snkIndexMap[key], len(p.allSinkInterfaces))
	p.allSinkInterfaces = append(p.allSinkInterfaces, sink)
	p.addKey(key)
	// log.Debug().Msgf("Before mappedDataPipeline: %s", p.mappedDataPipelines)
	p.mapSink(sink)
	// log.Debug().Msgf("After mappedDataPipeline: %s", p.mappedDataPipelines)
	return nil
}

// Gets all the pipelines in the format [{key : DataPipeline},...]
func (p *DataPipelineConfig) GetMappedPipelines() (map[string]DataPipeline, bool) {
	exists := p.mappedDataPipelines != nil
	return p.mappedDataPipelines, exists
}

// [NOT IMPLEMENTED] Close a pipeline given a key, all sources and sinks are
// also closed
func (p *DataPipelineConfig) Close(key string) (bool, error) {

	if p.keys[key] {
		// dataPipeline := p.mappedDataPipelines[key]
		// dataPipeline.Close()
		// dataPipeline.

		return true, nil
	}

	return false, fmt.Errorf("key does not exist")
}

// Information about all the configs
func (p *DataPipelineConfig) Info() {

	fmt.Printf("Keys\n")
	for k := range p.keys {
		fmt.Printf("%v ", k)
	}

	fmt.Printf("\nMaps\n")

	for k, v := range p.srcIndexMap {
		fmt.Printf("%v %v\n", k, v)
	}
	for k, v := range p.snkIndexMap {
		fmt.Printf("%v %v\n", k, v)
	}

	fmt.Printf("Interfaces\n")
	for _, src := range p.allSourceInterfaces {
		fmt.Printf("%s", src.Info())
	}
	fmt.Println("")
	for _, snk := range p.allSinkInterfaces {
		fmt.Printf("%s", snk.Info())
	}

	fmt.Printf("\nMaps\n")
	for k, v := range p.mappedDataPipelines {
		fmt.Printf("%s| %v %v\n", k, v.Source, v.Sink)
		// fmt.Printf("%s| %v ; %v", k, v.Source.Info(), v.Sink.Info())
	}
}

// Return the correct data source interface and initializes it given a config
func DataSourceFactory(config sources.SourceConfig) (DataSource, error) {
	sourceType := config.ConnectionType
	log.Debug().Msgf("Creating and allocating object for source: %s", sourceType)
	switch sourceType {
	case "mongodb":
		x := sources.NewMongoSource()
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

// Return the correct data sink interface and initializes it given a config
func DataSinkFactory(config sinks.SinkConfig) (DataSink, error) {
	sinkType := config.ConnectionType
	log.Debug().Msgf("Creating and allocating object for sink: %s", sinkType)
	switch sinkType {
	// case "elasticsearch":
	// 	x := &sinks.ElasticSink{}
	// 	x.Init(config)
	// 	return x, nil
	case "kafka":
		x := &sinks.KafkaSink{}
		x.Init(config)
		return x, nil
	case "file":
		x := &sinks.FileSink{}
		x.Init(config)
		return x, nil
	default:
		return nil, fmt.Errorf("unknown sink type: %s", sinkType)
	}
}

// This is a singleton implementation of the Data pipeline config.
// This will change a lot when made into a distributed architecture
func GetPipelineInstance() *DataPipelineConfig {
	once.Do(func() {
		pipelineInstance = &DataPipelineConfig{}
	})

	return pipelineInstance
}
