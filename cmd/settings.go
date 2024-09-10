package main

import (
	"fmt"

	sinks "github.com/tgk/wire/sinks"
	sources "github.com/tgk/wire/sources"
)

func dataSourceFactory(sourceType string) (DataSource, error) {
    switch sourceType {
    case "mongo":
        return &sources.MongoSource{}, nil
    // case "mysql":
    //     return &MySQLSource{}, nil
    // Add other sources
    default:
        return nil, fmt.Errorf("unknown source type: %s", sourceType)
    }
}

func dataSinkFactory(sinkType string) (DataSink, error) {
    switch sinkType {
    case "elasticsearch":
        return &sinks.ElasticSink{}, nil
    // case "kafka":
    //     return &KafkaSink{}, nil
    // Add other sinks
    default:
        return nil, fmt.Errorf("unknown sink type: %s", sinkType)
    }
}