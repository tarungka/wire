package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tarungka/wire/checkpoint"
	"github.com/tarungka/wire/engine"
	"github.com/tarungka/wire/state"
	"github.com/tarungka/wire/stream"
)

func main() {
	// Create a new state backend.
	backend := state.NewInMemoryStateBackend()

	// Create a new checkpoint manager.
	checkpointManager := checkpoint.NewCheckpointManager(backend)

	// Create a new pipeline.
	pipeline := stream.NewPipeline(
		stream.NewNumberSource(100),
		stream.NewPrintSink(),
	)

	// Add a map operator to the pipeline.
	mapOperator := stream.NewMapOperator("map", func(event stream.Event) stream.Event {
		return event.(int) * 2
	})
	pipeline.AddOperator(mapOperator)

	// Create a new checkpoint coordinator.
	eventChan := make(chan stream.Event)
	coordinator := engine.NewCheckpointCoordinator(1*time.Second, eventChan)

	// Start the pipeline.
	ctx, cancel := context.WithCancel(context.Background())
	if err := pipeline.Run(ctx); err != nil {
		panic(err)
	}

	// Start the checkpoint coordinator.
	go coordinator.Start(ctx)

	// Run the pipeline for 5 seconds.
	time.Sleep(5 * time.Second)

	// Take a checkpoint.
	checkpoint, err := checkpointManager.CreateCheckpoint([]stream.Operator{mapOperator})
	if err != nil {
		panic(err)
	}
	fmt.Printf("Created checkpoint %d\n", checkpoint.ID)

	// Cancel the context to simulate a failure.
	cancel()

	// Create a new pipeline.
	pipeline2 := stream.NewPipeline(
		stream.NewNumberSource(100),
		stream.NewPrintSink(),
	)

	// Add a map operator to the pipeline.
	mapOperator2 := stream.NewMapOperator("map", func(event stream.Event) stream.Event {
		return event.(int) * 2
	})
	pipeline2.AddOperator(mapOperator2)

	// Restore the state from the last checkpoint.
	fmt.Println("Restoring from checkpoint...")
	if err := checkpointManager.RestoreCheckpoint(checkpoint, []stream.Operator{mapOperator2}); err != nil {
		panic(err)
	}

	// Start the second pipeline.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if err := pipeline2.Run(ctx2); err != nil {
		panic(err)
	}

	// Run the second pipeline for 5 seconds.
	time.Sleep(5 * time.Second)
}
