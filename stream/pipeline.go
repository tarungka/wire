package stream

import (
	"context"

)

// Pipeline is a stream processing pipeline.
type Pipeline struct {
	source    Source
	operators []Operator
	sink      Sink
}

// NewPipeline creates a new Pipeline.
func NewPipeline(source Source, sink Sink) *Pipeline {
	return &Pipeline{
		source: source,
		sink:   sink,
	}
}

// AddOperator adds an operator to the pipeline.
func (p *Pipeline) AddOperator(operator Operator) {
	p.operators = append(p.operators, operator)
}

// Run runs the pipeline.
func (p *Pipeline) Run(ctx context.Context) error {
	sourceChan, err := p.source.Open(ctx)
	if err != nil {
		return err
	}

	sinkChan, err := p.sink.Open(ctx)
	if err != nil {
		return err
	}

	go func() {
		defer p.source.Close()
		defer p.sink.Close()

		for event := range sourceChan {
			if barrier, ok := event.(checkpoint.Barrier); ok {
				for _, operator := range p.operators {
					operator.HandleBarrier(barrier.CheckpointID)
				}
				continue
			}

			processedEvent := event
			for _, operator := range p.operators {
				processedEvent = operator.Process(processedEvent)
			}
			sinkChan <- processedEvent
		}
	}()

	return nil
}
