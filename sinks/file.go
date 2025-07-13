package sinks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/models"
)

type FileSink struct {
	pipelineKey            string
	pipelineName           string
	pipelineConnectionType string

	// File details
	filePath string
	file     *os.File
}

func (f *FileSink) Init(args SinkConfig) error {
	f.pipelineKey = args.Key
	f.pipelineName = args.Name
	f.pipelineConnectionType = args.ConnectionType

	if args.Config["file_path"] == "" {
		log.Error().Msg("Missing file_path in config")
		return fmt.Errorf("missing file_path")
	}

	f.filePath = args.Config["file_path"]
	return nil
}

// func (f *FileSink) Connect(ctx context.Context) error {
// 	log.Trace().Str("file_path", f.filePath).Msg("Opening file for writing")
// 	file, err := os.OpenFile(f.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
// 	if err != nil {
// 		log.Err(err).Msg("Failed to open file")
// 		return err
// 	}
// 	f.file = file
// 	return nil
// }

func (f *FileSink) Connect(ctx context.Context) error {
	log.Trace().Str("file_path", f.filePath).Msg("Preparing to open file for writing")

	// Ensure parent directory exists
	dir := filepath.Dir(f.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Err(err).Str("directory", dir).Msg("Failed to create parent directories")
		return fmt.Errorf("failed to create parent directories: %w", err)
	}

	// Warn if the file already exists
	if _, err := os.Stat(f.filePath); err == nil {
		log.Warn().Str("file_path", f.filePath).Msg("File already exists; appending to it")
	}

	// Open the file for appending
	file, err := os.OpenFile(f.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Err(err).Str("file_path", f.filePath).Msg("Failed to open file")
		return fmt.Errorf("failed to open file: %w", err)
	}

	f.file = file
	return nil
}

func (f *FileSink) Write(ctx context.Context, wg *sync.WaitGroup, dataChan <-chan *models.Job, initialDataChan <-chan *models.Job) error {
	// defer func() {
	// 	// log.Trace().Msg("Exiting file sink write goroutine")
	// 	wg.Done()
	// }()

	ctx, cancel := context.WithCancel(ctx)

	go func() {
		defer cancel()
		for {
			select {
			case job, ok := <-dataChan:
				if !ok {
					log.Debug().Msg("Data channel closed")
					return
				}
				f.writeToFile(ctx, job)

			case job, ok := <-initialDataChan:
				if !ok {
					continue
				}
				f.writeToFile(ctx, job)
			}
		}
	}()

	return nil
}

func (f *FileSink) writeToFile(ctx context.Context, job *models.Job) {
	data, err := job.GetData()
	if err != nil {
		log.Err(err).Msg("No data in job")
		return
	}
	dataBytes, ok := data.([]byte)
	if !ok {
		log.Error().Msg("Failed to convert job data to bytes")
		return
	}

	if _, err := f.file.Write(append(dataBytes, '\n')); err != nil {
		log.Err(err).Msg("Failed to write to file")
		return
	}
	log.Debug().Msgf("Job written to file: %s", f.filePath)
}

func (f *FileSink) Disconnect() error {
	log.Info().Msg("Closing file sink")
	if err := f.file.Close(); err != nil {
		log.Err(err).Msg("Failed to close file")
		return err
	}
	return nil
}

func (f *FileSink) Key() (string, error) {
	if f.pipelineKey == "" {
		return "", fmt.Errorf("no pipeline key is set")
	}
	return f.pipelineKey, nil
}

func (f *FileSink) Name() string { return f.pipelineName }

func (f *FileSink) Info() string {
	return fmt.Sprintf("Key:%s|Name:%s|Type:%s", f.pipelineKey, f.pipelineName, f.pipelineConnectionType)
}
