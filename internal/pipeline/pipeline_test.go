package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tarungka/wire/internal/models"
	"github.com/tarungka/wire/sinks"
	"github.com/tarungka/wire/sources"
)

// MockDataSource is a mock implementation of the DataSource interface
type MockDataSource struct {
	mock.Mock
}

func (m *MockDataSource) Init(args sources.SourceConfig) error {
	return m.Called(args).Error(0)
}

func (m *MockDataSource) Connect(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockDataSource) LoadInitialData(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error) {
	args := m.Called(ctx, wg)
	return args.Get(0).(<-chan *models.Job), args.Error(1)
}

func (m *MockDataSource) Read(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error) {
	args := m.Called(ctx, wg)
	return args.Get(0).(<-chan *models.Job), args.Error(1)
}

func (m *MockDataSource) Key() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockDataSource) Name() string {
	return m.Called().String(0)
}

func (m *MockDataSource) Info() string {
	return m.Called().String(0)
}

func (m *MockDataSource) Disconnect() error {
	return m.Called().Error(0)
}

// MockDataSink is a mock implementation of the DataSink interface
type MockDataSink struct {
	mock.Mock
}

func (m *MockDataSink) Init(args sinks.SinkConfig) error {
	return m.Called(args).Error(0)
}

func (m *MockDataSink) Connect(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockDataSink) Write(ctx context.Context, wg *sync.WaitGroup, data <-chan *models.Job, initialData <-chan *models.Job) error {
	return m.Called(ctx, wg, data, initialData).Error(0)
}

func (m *MockDataSink) Key() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *MockDataSink) Name() string {
	return m.Called().String(0)
}

func (m *MockDataSink) Info() string {
	return m.Called().String(0)
}

func (m *MockDataSink) Disconnect() error {
	return m.Called().Error(0)
}

// Helper function to create channels with data for testing
func createJobChannel(jobs []*models.Job) chan *models.Job {
	jobChan := make(chan *models.Job, len(jobs))
	for _, job := range jobs {
		jobChan <- job
	}
	close(jobChan)
	return jobChan
}

// TestNewDataPipeline tests the data pipeline creation and initialization
func TestNewDataPipeline(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	// Set up expectations for info logging
	mockSource.On("Info").Return("MockSource")
	mockSink.On("Info").Return("MockSink")
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	assert.NotNil(t, pipeline)
	assert.Equal(t, mockSource, pipeline.Source)
	assert.Equal(t, mockSink, pipeline.Sink)
	assert.Equal(t, uint(4), pipeline.jobCount)
	assert.False(t, pipeline.open.Load())
	
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_SetSource tests the SetSource method
func TestDataPipeline_SetSource(t *testing.T) {
	pipeline := &DataPipeline{}
	mockSource := new(MockDataSource)
	
	mockSource.On("Info").Return("TestSource")
	
	pipeline.SetSource(mockSource)
	
	assert.Equal(t, mockSource, pipeline.Source)
	mockSource.AssertExpectations(t)
}

// TestDataPipeline_SetSink tests the SetSink method
func TestDataPipeline_SetSink(t *testing.T) {
	mockSource := new(MockDataSource)
	pipeline := &DataPipeline{Source: mockSource}
	mockSink := new(MockDataSink)
	
	mockSink.On("Info").Return("TestSink")
	
	pipeline.SetSink(mockSink)
	
	assert.Equal(t, mockSink, pipeline.Sink)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_Show tests the Show method
func TestDataPipeline_Show(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	mockSource.On("Name").Return("TestSource")
	mockSink.On("Name").Return("TestSink")
	
	result, err := pipeline.Show()
	
	assert.NoError(t, err)
	assert.Equal(t, "TestSource -> TestSink", result)
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_Key tests the Key method
func TestDataPipeline_Key(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	// Test when pipeline is closed
	pipeline.open.Store(false)
	pipeline.key = "test-key"
	
	assert.Equal(t, "test-key", pipeline.Key())
	
	// Test when pipeline is open
	pipeline.open.Store(true)
	
	assert.Equal(t, "", pipeline.Key())
}

// TestDataPipeline_Run tests the Run method with successful connections
func TestDataPipeline_Run(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	// Create sample data channels
	initialDataChan := make(chan *models.Job)
	dataChan := make(chan *models.Job)
	
	mockSource.On("Connect", mock.Anything).Return(nil)
	mockSink.On("Connect", mock.Anything).Return(nil)
	mockSource.On("LoadInitialData", mock.Anything, mock.Anything).Return((<-chan *models.Job)(initialDataChan), nil)
	mockSource.On("Read", mock.Anything, mock.Anything).Return((<-chan *models.Job)(dataChan), nil)
	mockSink.On("Write", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockSource.On("Info").Return("MockSource").Maybe()
	mockSink.On("Info").Return("MockSink").Maybe()
	mockSource.On("Name").Return("MockSource").Maybe()
	mockSink.On("Name").Return("MockSink").Maybe()
	mockSource.On("Disconnect").Return(nil)
	mockSink.On("Disconnect").Return(nil)
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	ctx, cancel := context.WithCancel(context.Background())
	
	// Run pipeline in a goroutine
	go pipeline.Run(ctx)
	
	// Give it a moment to start up
	time.Sleep(50 * time.Millisecond)
	
	// Verify pipeline is running
	assert.True(t, pipeline.open.Load())
	
	// Clean up
	cancel()
	time.Sleep(50 * time.Millisecond)
	
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_Run_SourceConnectError tests handling of source connection errors
func TestDataPipeline_Run_SourceConnectError(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	connectErr := errors.New("source connection error")
	
	mockSource.On("Connect", mock.Anything).Return(connectErr)
	mockSink.On("Connect", mock.Anything).Return(nil)
	mockSource.On("LoadInitialData", mock.Anything, mock.Anything).Return(make(chan *models.Job), nil)
	mockSource.On("Read", mock.Anything, mock.Anything).Return(make(chan *models.Job), nil)
	mockSink.On("Write", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockSource.On("Info").Return("MockSource").Maybe()
	mockSink.On("Info").Return("MockSink").Maybe()
	mockSource.On("Name").Return("MockSource").Maybe()
	mockSink.On("Name").Return("MockSink").Maybe()
	mockSource.On("Disconnect").Return(nil)
	mockSink.On("Disconnect").Return(nil)
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Run pipeline in a goroutine
	go pipeline.Run(ctx)
	
	// Give it a moment to start up and process the error
	time.Sleep(50 * time.Millisecond)
	
	// Verify pipeline state (should still be running despite the error)
	assert.True(t, pipeline.open.Load())
	
	// Clean up
	pipeline.Close()
	
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_Run_SinkConnectError tests handling of sink connection errors
func TestDataPipeline_Run_SinkConnectError(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	connectErr := errors.New("sink connection error")
	
	mockSource.On("Connect", mock.Anything).Return(nil)
	mockSink.On("Connect", mock.Anything).Return(connectErr)
	mockSource.On("LoadInitialData", mock.Anything, mock.Anything).Return(make(chan *models.Job), nil)
	mockSource.On("Read", mock.Anything, mock.Anything).Return(make(chan *models.Job), nil)
	mockSink.On("Write", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockSource.On("Info").Return("MockSource").Maybe()
	mockSink.On("Info").Return("MockSink").Maybe()
	mockSource.On("Name").Return("MockSource").Maybe()
	mockSink.On("Name").Return("MockSink").Maybe()
	mockSource.On("Disconnect").Return(nil)
	mockSink.On("Disconnect").Return(nil)
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Run pipeline in a goroutine
	go pipeline.Run(ctx)
	
	// Give it a moment to start up and process the error
	time.Sleep(50 * time.Millisecond)
	
	// Verify pipeline state (should still be running despite the error)
	assert.True(t, pipeline.open.Load())
	
	// Clean up
	pipeline.Close()
	
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_Run_LoadInitialDataError tests handling of LoadInitialData errors
func TestDataPipeline_Run_LoadInitialDataError(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	loadErr := errors.New("initial data loading error")
	
	mockSource.On("Connect", mock.Anything).Return(nil)
	mockSink.On("Connect", mock.Anything).Return(nil)
	mockSource.On("LoadInitialData", mock.Anything, mock.Anything).Return(make(chan *models.Job), loadErr)
	mockSource.On("Read", mock.Anything, mock.Anything).Return(make(chan *models.Job), nil)
	mockSink.On("Write", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockSource.On("Info").Return("MockSource").Maybe()
	mockSink.On("Info").Return("MockSink").Maybe()
	mockSource.On("Name").Return("MockSource").Maybe()
	mockSink.On("Name").Return("MockSink").Maybe()
	mockSource.On("Disconnect").Return(nil)
	mockSink.On("Disconnect").Return(nil)
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Run pipeline in a goroutine
	go pipeline.Run(ctx)
	
	// Give it a moment to start up and process the error
	time.Sleep(50 * time.Millisecond)
	
	// Verify pipeline state (should still be running despite the error)
	assert.True(t, pipeline.open.Load())
	
	// Clean up
	pipeline.Close()
	
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_Run_ReadError tests handling of Read errors
func TestDataPipeline_Run_ReadError(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	readErr := errors.New("data read error")
	
	mockSource.On("Connect", mock.Anything).Return(nil)
	mockSink.On("Connect", mock.Anything).Return(nil)
	mockSource.On("LoadInitialData", mock.Anything, mock.Anything).Return(make(chan *models.Job), nil)
	mockSource.On("Read", mock.Anything, mock.Anything).Return(make(chan *models.Job), readErr)
	mockSource.On("Info").Return("MockSource").Maybe()
	mockSink.On("Info").Return("MockSink").Maybe()
	mockSource.On("Name").Return("MockSource").Maybe()
	mockSink.On("Name").Return("MockSink").Maybe()
	mockSource.On("Disconnect").Return(nil)
	mockSink.On("Disconnect").Return(nil)
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Run pipeline in a goroutine
	go pipeline.Run(ctx)
	
	// Give it a moment to start up and process the error
	time.Sleep(50 * time.Millisecond)
	
	// Verify pipeline state (should have closed due to the read error)
	assert.False(t, pipeline.open.Load())
	
	// Clean up
	pipeline.Close()
	
	mockSource.AssertExpectations(t)
	// Note: Write is not expected to be called since Read failed
}

// TestDataPipeline_Close tests the Close method
func TestDataPipeline_Close(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	mockSource.On("Disconnect").Return(nil)
	mockSink.On("Disconnect").Return(nil)
	mockSource.On("Name").Return("MockSource")
	mockSink.On("Name").Return("MockSink")
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	
	// Set up a cancel function for testing
	_, cancel := context.WithCancel(context.Background())
	pipeline.cancel = cancel
	pipeline.open.Store(true)
	
	result := pipeline.Close()
	
	assert.False(t, result)
	assert.False(t, pipeline.open.Load())
	
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}

// TestDataPipeline_Init tests the Init method
func TestDataPipeline_Init(t *testing.T) {
	pipeline := &DataPipeline{}
	
	err := pipeline.Init()
	
	assert.NoError(t, err)
}

// Integration-like test with actual data flowing through channels
func TestDataPipeline_DataFlow(t *testing.T) {
	mockSource := new(MockDataSource)
	mockSink := new(MockDataSink)
	
	// Create test data with proper UUID type and structure
	testJobs := []*models.Job{
		{ID: uuid.New()}, // Assuming models.Job has a UUID ID field
		{ID: uuid.New()}, // and doesn't have an exported Data field
	}
	
	initialJobChan := createJobChannel(testJobs)
	jobChan := createJobChannel(testJobs)
	
	// Set up a WaitGroup to synchronize test completion
	var wg sync.WaitGroup
	wg.Add(1)
	
	// Configure mocks
	mockSource.On("Connect", mock.Anything).Return(nil)
	mockSink.On("Connect", mock.Anything).Return(nil)
	mockSource.On("LoadInitialData", mock.Anything, mock.Anything).Return((<-chan *models.Job)(initialJobChan), nil)
	mockSource.On("Read", mock.Anything, mock.Anything).Return((<-chan *models.Job)(jobChan), nil)
	mockSink.On("Write", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			// Mark test as complete when Write is called
			wgArg := args.Get(1).(*sync.WaitGroup)
			wgArg.Done()
		}).
		Return(nil)
	mockSource.On("Info").Return("MockSource").Maybe()
	mockSink.On("Info").Return("MockSink").Maybe()
	mockSource.On("Name").Return("MockSource").Maybe()
	mockSink.On("Name").Return("MockSink").Maybe()
	mockSource.On("Disconnect").Return(nil)
	mockSink.On("Disconnect").Return(nil)
	
	pipeline := NewDataPipeline(mockSource, mockSink)
	pipeline.jobCount = 1 // Simplify test by using only one worker
	
	// Run pipeline in a goroutine
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pipeline.Run(ctx)
	}()
	
	// Wait for data processing to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	// Either wait for completion or timeout
	select {
	case <-done:
		// Test passed
	case <-time.After(1 * time.Second):
		t.Fatal("Test timed out waiting for data processing")
	}
	
	mockSource.AssertExpectations(t)
	mockSink.AssertExpectations(t)
}
