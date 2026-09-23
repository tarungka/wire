package sdk

import (
	"testing"
	"time"
)

func TestAllowedLatenessUnitsAndValidation(t *testing.T) {
	for _, value := range []any{30 * time.Second, int64(30000), 30000} {
		ws := (&WindowedStream{}).AllowedLateness(value)
		if ws.allowedLateness != 30000 {
			t.Fatalf("%v -> %d", value, ws.allowedLateness)
		}
	}
	for _, value := range []any{-1, int64(-1), -time.Second, time.Microsecond, "30s"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("accepted %v", value)
				}
			}()
			(&WindowedStream{}).AllowedLateness(value)
		}()
	}
}

func TestYAMLAllowedLateness(t *testing.T) {
	for _, kind := range []string{"tumbling-window", "sliding-window", "session-window"} {
		for _, value := range []any{"0s", "30s", "1h", "-1ms", "1us", 30, "bad"} {
			cfg := map[string]any{"aggregation": "count", "allowed_lateness": value}
			switch kind {
			case "tumbling-window":
				cfg["size"] = "10s"
			case "sliding-window":
				cfg["size"] = "10s"
				cfg["slide"] = "5s"
			case "session-window":
				cfg["gap"] = "10s"
			}
			node := &StreamNode{}
			err := compilePipelineTransform(node, pipelineOperator{Type: kind, Config: cfg}, nil)
			valid := value == "0s" || value == "30s" || value == "1h"
			if (err == nil) != valid {
				t.Fatalf("kind=%s lateness=%v err=%v", kind, value, err)
			}
			if valid {
				want, _ := time.ParseDuration(value.(string))
				if node.AllowedLateness != want.Milliseconds() {
					t.Fatalf("lateness=%d", node.AllowedLateness)
				}
			}
		}
	}
}
