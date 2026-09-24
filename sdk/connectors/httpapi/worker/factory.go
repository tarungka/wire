// Package worker registers HTTP API connectors in a public SDK worker registry.
package worker

import (
	"context"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/sdk"
	"github.com/tarungka/wire/sdk/connectors/httpapi"
)

// Register installs source and sink factories under the class "http-api".
// Call before starting workers. Each factory creates a fresh, unopened connector.
func Register(registry *sdk.WorkerRegistry) {
	registry.RegisterSource("http-api", SourceFactory())
	registry.RegisterSink("http-api", SinkFactory())
}

func SourceFactory() sdk.WorkerSourceFactory {
	return func(_ context.Context, data []byte, _ sdk.WorkerTaskContext) (sdk.Source, error) {
		var cfg httpapi.SourceConfig
		if err := protocol.DecodeMsgPack(data, &cfg); err != nil {
			return nil, err
		}
		return httpapi.NewSource(cfg)
	}
}

func SinkFactory() sdk.WorkerSinkFactory {
	return func(_ context.Context, data []byte, _ sdk.WorkerTaskContext) (sdk.Sink, error) {
		var cfg httpapi.SinkConfig
		if err := protocol.DecodeMsgPack(data, &cfg); err != nil {
			return nil, err
		}
		return httpapi.NewSink(cfg)
	}
}

// EncodeSourceConfig validates and serializes configuration for AddSourceNamed.
// It does not open a listener. Credentials remain sensitive configuration bytes.
func EncodeSourceConfig(cfg httpapi.SourceConfig) ([]byte, error) {
	if _, err := httpapi.NewSource(cfg); err != nil {
		return nil, err
	}
	return protocol.EncodeMsgPack(cfg)
}

// EncodeSinkConfig validates and serializes configuration for AddSinkNamed.
// It does not send HTTP requests. Credentials remain sensitive configuration bytes.
func EncodeSinkConfig(cfg httpapi.SinkConfig) ([]byte, error) {
	sink, err := httpapi.NewSink(cfg)
	if err != nil {
		return nil, err
	}
	if err := sink.Close(); err != nil {
		return nil, err
	}
	return protocol.EncodeMsgPack(cfg)
}
