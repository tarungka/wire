package pipeline

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/operators"
	"github.com/tarungka/wire/internal/transport"
)

// LoopbackEdge connects two TaskSlots via a loopback TCP FrameStream pair.
type LoopbackEdge struct {
	serverMux *transport.Mux
	clientMux *transport.Mux
	Writer    *transport.FrameStream // writer side (upstream slot output)
	Reader    *transport.FrameStream // reader side (downstream slot input)
}

// newLoopbackEdge creates a connected (writer, reader) FrameStream pair
// over loopback TCP. The handshake is performed automatically.
func newLoopbackEdge(ctx context.Context) (*LoopbackEdge, error) {
	sCfg := transport.DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	serverMux := transport.NewMux(sCfg)

	if err := serverMux.Listen(ctx); err != nil {
		return nil, fmt.Errorf("loopback edge: listen: %w", err)
	}
	addr := serverMux.ListenAddr()

	cCfg := transport.DefaultConfig()
	clientMux := transport.NewMux(cCfg)

	writer, err := clientMux.Dial(ctx, addr)
	if err != nil {
		serverMux.Close()
		return nil, fmt.Errorf("loopback edge: dial: %w", err)
	}

	reader, err := serverMux.Accept(ctx)
	if err != nil {
		writer.Close()
		clientMux.Close()
		serverMux.Close()
		return nil, fmt.Errorf("loopback edge: accept: %w", err)
	}

	if _, err := reader.ReceiveHandshake(); err != nil {
		reader.Close()
		writer.Close()
		clientMux.Close()
		serverMux.Close()
		return nil, fmt.Errorf("loopback edge: handshake: %w", err)
	}

	return &LoopbackEdge{
		serverMux: serverMux,
		clientMux: clientMux,
		Writer:    writer,
		Reader:    reader,
	}, nil
}

// Close tears down both muxes and their streams.
func (e *LoopbackEdge) Close() {
	e.Writer.Close()
	e.Reader.Close()
	e.clientMux.Close()
	e.serverMux.Close()
}

// Pipeline holds task slots and their connecting edges.
type Pipeline struct {
	Slots []*engine.TaskSlot
	Edges []*LoopbackEdge
}

// Run launches all task slots via errgroup and blocks until completion.
// On exit it closes all loopback edges.
func (p *Pipeline) Run(ctx context.Context) error {
	defer func() {
		for _, e := range p.Edges {
			e.Close()
		}
	}()

	g, gctx := errgroup.WithContext(ctx)
	for i, slot := range p.Slots {
		slot := slot
		i := i
		g.Go(func() error {
			err := slot.Run(gctx)
			if err != nil {
				return fmt.Errorf("slot[%d]: %w", i, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// BuildDemoPipeline creates a 3-slot pipeline:
//
//	GeneratorSource → ToUpperMap → StdoutSink
//
// connected by loopback TCP edges.
func BuildDemoPipeline(ctx context.Context, eventCount int) (*Pipeline, error) {
	cfg := engine.DefaultTaskSlotConfig()

	// Create two loopback edges.
	edge1, err := newLoopbackEdge(ctx)
	if err != nil {
		return nil, fmt.Errorf("edge1: %w", err)
	}

	edge2, err := newLoopbackEdge(ctx)
	if err != nil {
		edge1.Close()
		return nil, fmt.Errorf("edge2: %w", err)
	}

	// Slot 0: Source → edge1
	source := operators.NewGeneratorSource(eventCount)
	slot0 := engine.NewTaskSlot(
		cfg,
		nil,                                    // no input streams
		[]*transport.FrameStream{edge1.Writer}, // output to edge1
		nil,                                    // no operators (pass-through)
		source,
	)

	// Slot 1: edge1 → ToUpperMap → edge2
	slot1 := engine.NewTaskSlot(
		cfg,
		[]*transport.FrameStream{edge1.Reader}, // input from edge1
		[]*transport.FrameStream{edge2.Writer}, // output to edge2
		[]engine.Operator{&operators.ToUpperMap{}},
		nil, // not a source
	)

	// Slot 2: edge2 → StdoutSink (terminal)
	slot2 := engine.NewTaskSlot(
		cfg,
		[]*transport.FrameStream{edge2.Reader}, // input from edge2
		nil,                                    // no outputs
		[]engine.Operator{operators.NewStdoutSink(nil)},
		nil, // not a source
	)

	return &Pipeline{
		Slots: []*engine.TaskSlot{slot0, slot1, slot2},
		Edges: []*LoopbackEdge{edge1, edge2},
	}, nil
}
