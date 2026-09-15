package rpc

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestWatermarkConfigurationValidationAndRoundTrip(t *testing.T) {
	for _, strategy := range []string{"bounded-ooo", "monotonic", "ingestion-time"} {
		cfg := WatermarkConfig{Strategy: strategy, EmitInterval: 200 * time.Millisecond, IdleTimeout: time.Minute}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		original := OperatorDescriptor{OperatorID: "source", Type: OperatorTypeSource, Watermark: &cfg}
		data, err := protocol.EncodeMsgPack(original)
		if err != nil {
			t.Fatal(err)
		}
		var decoded OperatorDescriptor
		if err := protocol.DecodeMsgPack(data, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Watermark == nil || *decoded.Watermark != cfg {
			t.Fatalf("lost configuration: %+v", decoded)
		}
	}
	for _, cfg := range []WatermarkConfig{{Strategy: "unknown"}, {Strategy: "bounded-ooo", MaxOOO: watermarkDuration(-1)}, {Strategy: "monotonic", MaxOOO: watermarkDuration(time.Second)}, {Strategy: "ingestion-time", EmitInterval: -1}, {Strategy: "bounded-ooo", IdleTimeout: -1}} {
		if cfg.Validate() == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
}

func watermarkDuration(value time.Duration) *time.Duration { return &value }
