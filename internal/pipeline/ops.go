package pipeline

import (
	"context"

	"github.com/tarungka/wire/internal/models"
)

type TransformFunc func(ctx context.Context, in <-chan *models.Job) <-chan *models.Job

type PipelineData struct {
	out <-chan *models.Job
}

func Transform(fn TransformFunc) func(p *PipelineData) *PipelineData {
	return func(p *PipelineData) *PipelineData {
		ctx := context.Background()
		return &PipelineData{out: fn(ctx, p.out)}
	}
}

func Map(mapper func(*models.Job) *models.Job) TransformFunc {
	return func(ctx context.Context, in <-chan *models.Job) <-chan *models.Job {
		out := make(chan *models.Job)
		go func() {
			defer close(out)
			for job := range in {
				out <- mapper(job)
			}
		}()
		return out
	}
}
