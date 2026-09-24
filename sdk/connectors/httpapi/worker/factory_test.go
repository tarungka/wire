package worker_test

import (
	"encoding/binary"
	"testing"

	"github.com/tarungka/wire/sdk"
	"github.com/tarungka/wire/sdk/connectors/httpapi"
	httpworker "github.com/tarungka/wire/sdk/connectors/httpapi/worker"
)

func TestPublicHTTPFactoriesPreserveConnectorContracts(t *testing.T) {
	registry := sdk.NewWorkerRegistry()
	httpworker.Register(registry)
	sourceConfig, err := httpworker.EncodeSourceConfig(httpapi.SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	factory := httpworker.SourceFactory()
	first, err := factory(t.Context(), sourceConfig, sdk.WorkerTaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := factory(t.Context(), sourceConfig, sdk.WorkerTaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first.(*httpapi.Source).Address() != "" {
		t.Fatal("factory reused or opened source")
	}
	if _, ok := first.(sdk.PreOpenCheckpointedSource); !ok {
		t.Fatal("source lost pre-open restore contract")
	}
	checkpointed, ok := first.(sdk.CheckpointedSource)
	if !ok {
		t.Fatal("source lost checkpoint contract")
	}
	offset := make([]byte, 8)
	binary.BigEndian.PutUint64(offset, 42)
	if err := checkpointed.RestoreOffset(t.Context(), offset); err != nil {
		t.Fatal(err)
	}
	saved, err := checkpointed.Checkpoint(1)
	if err != nil || binary.BigEndian.Uint64(saved) != 42 {
		t.Fatalf("offset=%v err=%v", saved, err)
	}
	sinkConfig, err := httpworker.EncodeSinkConfig(httpapi.SinkConfig{URL: "https://example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := httpworker.SinkFactory()(t.Context(), sinkConfig, sdk.WorkerTaskContext{})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if _, ok := sink.(sdk.BatchSink); !ok {
		t.Fatal("sink lost explicit batch contract")
	}
}

func TestPublicHTTPFactoriesRejectInvalidConfig(t *testing.T) {
	if _, err := httpworker.EncodeSourceConfig(httpapi.SourceConfig{Address: "127.0.0.1:0"}); err == nil {
		t.Fatal("insecure source accepted")
	}
	if _, err := httpworker.EncodeSinkConfig(httpapi.SinkConfig{URL: "http://example.invalid"}); err == nil {
		t.Fatal("insecure sink accepted")
	}
	for _, data := range [][]byte{nil, []byte("malformed")} {
		if _, err := httpworker.SourceFactory()(t.Context(), data, sdk.WorkerTaskContext{}); err == nil {
			t.Fatal("invalid source bytes accepted")
		}
		if _, err := httpworker.SinkFactory()(t.Context(), data, sdk.WorkerTaskContext{}); err == nil {
			t.Fatal("invalid sink bytes accepted")
		}
	}
}
