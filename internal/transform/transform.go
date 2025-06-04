package transform

import (
	"context"
	"fmt"

	// "github.com/elastic/go-elasticsearch/v8/typedapi/slm/executeretention"
	"github.com/rs/zerolog/log"

	"encoding/json"
	"strings"

	"github.com/tarungka/wire/internal/models"

	"github.com/apache/beam/sdks/v2/go/pkg/beam"
	"github.com/apache/beam/sdks/v2/go/pkg/beam/io/textio"

	// "github.com/apache/beam/sdks/v2/go/pkg/beam/options/jobopts"
	// "github.com/apache/beam/sdks/v2/go/pkg/beam/options/pipelineopts"
	_ "github.com/apache/beam/sdks/v2/go/pkg/beam/io/filesystem/local"
	"github.com/apache/beam/sdks/v2/go/pkg/beam/runners/prism"
)

// Transformer represents the processing pipeline
type Transformer struct {
	initalized bool

	pipeline *beam.Pipeline
	scope beam.Scope
}

func (tf *Transformer) Init() {
	if !tf.initalized {
		beam.Init()
		tf.initalized = true
	}

	tf.pipeline, tf.scope = beam.NewPipelineWithRoot()
}

// applyTransformation applies Beam transformations
func (tf *Transformer) ApplyTransformation(inputData []string) error {
	// Create a new Beam pipeline
	beam.Init()


	// Create a PCollection from inputData
	input := beam.CreateList(tf.scope, inputData)

	// Example transformation: Convert to uppercase
	transformed := beam.ParDo(tf.scope, func(line string) string {
		return fmt.Sprintf("Processed: %s", line)
	}, input)

	// Write output to a text file (Replace with your sink logic)
	outputPath := "output.txt"
	textio.Write(tf.scope, outputPath, transformed)

	// Run the pipeline
	a, err := prism.Execute(context.Background(), tf.pipeline)
	if err != nil {
		log.Fatal().Msgf("Pipeline failed: %v", err)
		return err
	}
	fmt.Sprintf("Executed: %s", a)

	log.Info().Msg("Transformation applied successfully.")
	return nil
}

// func (tf *Transformer) ApplyTransformationJobBatch(ctx context.Context, jobChannel <-chan *models.Job) <-chan any {
//
// 	beam.Init()
//
// 	batchSize := 10                           // Process jobs in batches
// 	ticker := time.NewTicker(2 * time.Second) // Periodic processing
// 	defer ticker.Stop()
//
// 	for {
// 		select {
// 		case <-ctx.Done():
// 			log.Info().Msg("Shutting down transformation pipeline...")
// 			return nil
//
// 		case <-ticker.C:
// 			var inputData []string
//
// 			// Collect jobs from the channel
// 			for i := 0; i < batchSize; i++ {
// 				select {
// 				case job := <-jobChannel:
//
// 					jobData, err := job.GetData()
// 					if err != nil {
// 						log.Err().Msg("Error when getting the data from the channel")
// 					}
// 					// This is not right to type assert to this  to a string, find a better
// 					// solution for this
// 					inputData = append(inputData, string(jobData.([]byte)))
// 				default:
// 					break
// 				}
// 			}
//
// 			if len(inputData) > 0 {
// 				log.Printf("Processing %d jobs...", len(inputData))
// 				data, err := tf.runBeamPipeline(inputData)
// 				if err != nil {
// 					log.Printf("Error processing jobs: %v", err)
// 				}
// 				<-data
// 			}
// 		}
// 	}
// }

func (tf *Transformer) ApplyTransformationJob(ctx context.Context, jobChannel <-chan *models.Job) <-chan *models.Job {
	if !tf.initalized {
		tf.Init()
	}

	outChannel := make(chan *models.Job, 5)

	go func() {
		defer close(outChannel)

		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Shutting down transformation pipeline...")
				return

			case job := <-jobChannel: // Process job as soon as it arrives
				jobData, err := job.GetData()
				if err != nil {
					log.Printf("Error retrieving job data: %v", err)
					continue
				}

				// Ensure correct type conversion
				dataStr, ok := jobData.([]byte)
				if !ok {
					log.Printf("Unexpected job data type: %T", jobData)
					continue
				}

				// Process the job immediately
				log.Printf("Processing job: %s", string(dataStr))
				data, err := tf.runBeamPipeline([]string{string(dataStr)})
				if err != nil {
					log.Printf("Error processing job: %v", err)
				}
				// log.Debug().Msgf("Processed JOB: %s", data)
				log.Debug().Msgf("Processed JOB")
				// Update the data and add it to the channel
				job.SetData(data)
				outChannel <- job
			}
		}
	}()

	return outChannel
}

// runBeamPipeline runs the Apache Beam pipeline for processing jobs.
func (tf *Transformer) runBeamPipeline(inputData []string) (any, error) {
	p := beam.NewPipeline()
	s := p.Root()
	input := beam.CreateList(s, inputData)

	// log.Debug().Msgf("In the runBeamPipeline function")

	// transformed := beam.ParDo(s, func(line string) string {
	// 	return fmt.Sprintf("Processed: %s", line)
	// }, input)

	// This is for debugging
	// outputPath := "output.txt"
	// textio.Write(s, outputPath, transformed)

	if _, err := prism.Execute(context.Background(), p); err != nil {
		return nil, fmt.Errorf("pipeline execution failed: %w", err)
	}
	// log.Debug().Msgf("executeResult: %s", executeResult)
	transformed := beam.ParDo(s, toUppercaseJSON, input)

	log.Debug().Msgf("Processed Bro! | %s", transformed)
	return transformed, nil
}

// toUppercaseJSON parses JSON and converts all string values to uppercase
func toUppercaseJSON(jsonStr string) (string, error) {
	var data map[string]any

	log.Debug().Msgf("Processing data: %s", jsonStr)

	// Parse JSON
	err := json.Unmarshal([]byte(jsonStr), &data)
	if err != nil {
		log.Printf("Failed to parse JSON: %v", err)
		return "", err
	}

	// Convert all string values to uppercase
	uppercaseJSON(data)

	// Convert back to JSON string
	updatedJSON, err := json.Marshal(data)
	if err != nil {
		log.Printf("Failed to serialize JSON: %v", err)
		return "", err
	}

	return string(updatedJSON), nil
}

// uppercaseJSON recursively converts all string values in JSON to uppercase
func uppercaseJSON(data any) {
	switch v := data.(type) {
	case map[string]any:
		for key, val := range v {
			switch valTyped := val.(type) {
			case string:
				v[key] = strings.ToUpper(valTyped)
			case map[string]any, []any:
				uppercaseJSON(valTyped)
			}
		}
	case []any:
		for _, val := range v {
			uppercaseJSON(val)
		}
	}
}

// func main() {
// 	tf := Transformer{}
// 	data := []string{"job1", "job2", "job3"}
// 	err := tf.ApplyTransformation(data)
// 	if err != nil {
// 		log.Fatalf("Error applying transformation: %v", err)
// 	}
// }
