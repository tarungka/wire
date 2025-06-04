package pipeline

import (
	"context"
	"sync"

	"github.com/tarungka/wire/internal/models"
	"github.com/tarungka/wire/sinks"
	"github.com/tarungka/wire/sources"
)

type DataSource interface {

	// Parse and configure the Source
	Init(args sources.SourceConfig) error

	// Connect to the Source
	Connect(context.Context) error

	// Load all initial data from the source
	// There are exceptions to this, i.e kafka
	// LoadInitialData(context.Context, *sync.WaitGroup) (<-chan []byte, error)
	LoadInitialData(context.Context, *sync.WaitGroup) (<-chan *models.Job, error)

	// Read is responsible to create a write only channel that is accessible to
	// downstream stages and is the owner of the channel
	Read(context.Context, *sync.WaitGroup) (<-chan *models.Job, error)

	// Get the key
	Key() (string, error)

	// Name of the Source
	Name() string

	// Info about he Source
	Info() string

	// Disconnect the application from the source
	Disconnect() error
}

type DataSink interface {

	// Parse and configure the Sink
	Init(args sinks.SinkConfig) error

	// Connect to the Sink
	Connect(context.Context) error

	// Write is responsible to read data from the upstream input channel and
	// write data to the sink
	Write(context.Context, *sync.WaitGroup, <-chan *models.Job, <-chan *models.Job) error
	// Write(context.Context, *sync.WaitGroup, interface{}, <-chan *models.Job) error

	// Get the key
	Key() (string, error)

	// Name of the Sink
	Name() string

	// Info about he Sink
	Info() string

	// Disconnect the application from the sink
	Disconnect() error
}
