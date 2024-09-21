// TODO: Horrible file name, change it later
package pipeline

import (
	"fmt"
	"sync"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog/log"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"
)

type DataPipelineConfig struct {
	allSourceInterfaces []DataSource
	allSinkInterfaces   []DataSink
	srcIndexMap         map[string][]int // key : [indices of where the source is in the allSourceInterfaces]
	snkIndexMap         map[string][]int // key : [indices of where the sink is in the allSinkInterfaces]
	// Keys can have the value false when a key is created and then deleted
	keys                map[string]bool         // Keys: ifExists
	mappedDataPipelines map[string]DataPipeline // Mapping of {key: DataPipeline}
}

var (
	pipelineInstance *DataPipelineConfig
	once             sync.Once
)

func (p *DataPipelineConfig) addKey(s string) {

	if p.keys == nil {
		p.keys = make(map[string]bool)
	}

	if !p.keys[s] {
		p.keys[s] = true
	}
}

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

func (p *DataPipelineConfig) mapSource(source DataSource) {

	log.Trace().Msg("Mapping source")

	if p.mappedDataPipelines == nil {
		log.Debug().Msg("mappedDataPipelines is nil, making it")
		p.mappedDataPipelines = make(map[string]DataPipeline)
	}

	key, _ := source.Key()

	if value, exists := p.mappedDataPipelines[key]; exists {
		log.Debug().Msgf("Mapped source key(%s) exists, updating it", key)
		value.SetSource(source)            // Updated the local copy
		p.mappedDataPipelines[key] = value // Updating the main object
		return
	}

	log.Debug().Msgf("Mapped source key(%s) does NOT exists, creating it", key)
	data := DataPipeline{
		key:    key,
		Source: source,
		Sink:   nil,
	}
	p.mappedDataPipelines[key] = data
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

func (p *DataPipelineConfig) AddSource(src sources.SourceConfig) error {

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
	p.mapSource(source)
	return nil
}

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

func (p *DataPipelineConfig) GetMappedPipelines() (map[string]DataPipeline, bool) {
	exists := p.mappedDataPipelines != nil
	return p.mappedDataPipelines, exists
}

func (p *DataPipelineConfig) Close(key string) (bool, error) {

	if p.keys[key] {
		// dataPipeline := p.mappedDataPipelines[key]
		// dataPipeline.Close()
		// dataPipeline.

		return true, nil
	}

	return false, fmt.Errorf("key does not exist")
}

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

// TODO: Move this to source dir
func DataSourceFactory(config sources.SourceConfig) (DataSource, error) {
	sourceType := config.ConnectionType
	log.Debug().Msgf("Creating and allocating object for source: %s", sourceType)
	switch sourceType {
	case "mongodb":
		x := &sources.MongoSource{}
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

// TODO: Move this to sink dir
func DataSinkFactory(config sinks.SinkConfig) (DataSink, error) {
	sinkType := config.ConnectionType
	log.Debug().Msgf("Creating and allocating object for sink: %s", sinkType)
	switch sinkType {
	case "elasticsearch":
		x := &sinks.ElasticSink{}
		x.Init(config)
		return x, nil
	case "kafka":
		x := &sinks.KafkaSink{}
		x.Init(config)
		return x, nil
	default:
		return nil, fmt.Errorf("unknown sink type: %s", sinkType)
	}
}

func GetPipelineInstance() *DataPipelineConfig {
	once.Do(func() {
		pipelineInstance = &DataPipelineConfig{}
	})

	return pipelineInstance
}
