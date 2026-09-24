package sdk

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestNamedWindowDeploymentRoundTrip(t *testing.T) {
	for _, assigner := range []WindowAssigner{TumblingWindow(10 * time.Millisecond), SlidingWindow(10*time.Millisecond, 5*time.Millisecond), SessionWindow(10 * time.Millisecond)} {
		t.Run(assigner.Type(), func(t *testing.T) {
			env := New()
			src := env.graph.addNode(&StreamNode{Type: NodeSource, Name: "source", ClassName: "source"})
			cfg := []byte("factory-settings")
			ws := (&WindowedStream{env: env, nodeID: src, assigner: assigner}).AllowedLateness(30 * time.Millisecond).SetLateOutputTag(NewOutputTag("late"))
			result := ws.ApplyNamed("window", "count-v2", cfg)
			cfg[0] = 'X'
			result.AddSinkNamed("main", "sink", nil)
			result.GetSideOutput(NewOutputTag("late")).AddSinkNamed("late-sink", "sink", nil)
			encoded, err := protocol.EncodeMsgPack(env.graph.toJobGraph(1))
			if err != nil {
				t.Fatal(err)
			}
			var graph rpc.JobGraph
			if err := protocol.DecodeMsgPack(encoded, &graph); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, op := range graph.Operators {
				if op.OperatorID != "window" {
					continue
				}
				found = true
				if op.Type != rpc.OperatorTypeWindow || op.ClassName != "count-v2" || string(op.Config) != "factory-settings" || op.LateOutputTag != "late" || op.Window == nil {
					t.Fatalf("lost deployment config: %+v", op)
				}
				w := op.Window
				if w.Kind != assigner.Type() || w.Size != assigner.Size().Milliseconds() || w.Slide != assigner.Slide().Milliseconds() || w.Gap != assigner.Gap().Milliseconds() || w.AllowedLateness != 30 {
					t.Fatalf("lost dimensions: %+v", w)
				}
			}
			if !found {
				t.Fatal("window missing")
			}
			tagged := 0
			for _, edge := range graph.Edges {
				if edge.SideOutput == "late" {
					tagged++
					if edge.SourceOperatorID != "window" || edge.TargetOperatorID != "late-sink" {
						t.Fatal(edge)
					}
				}
			}
			if tagged != 1 {
				t.Fatalf("late edges=%d", tagged)
			}
		})
	}
}
