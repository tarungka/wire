package coordinator

import (
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestSubmitWatermarksRejectsBeforeReservation(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	for _, mode := range []string{"unknown", "negative", "non-source"} {
		graph := linearGraph()
		index := 0
		cfg := &rpc.WatermarkConfig{Strategy: "monotonic"}
		switch mode {
		case "unknown":
			cfg.Strategy = "unknown"
		case "negative":
			cfg.EmitInterval = -1
		case "non-source":
			index = 1
		}
		graph.Operators[index].Watermark = cfg
		if _, err := c.SubmitJob("watermark", 1, encode(t, graph)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	if len(c.ListJobs(nil)) != 0 {
		t.Fatal("invalid graph persisted")
	}
	graph := linearGraph()
	graph.Operators[0].Watermark = &rpc.WatermarkConfig{Strategy: "ingestion-time"}
	if _, err := c.SubmitJob("watermark", 1, encode(t, graph)); err != nil {
		t.Fatal(err)
	}
}
