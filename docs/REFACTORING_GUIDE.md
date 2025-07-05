# Wire Refactoring Guide

## Introduction

This guide provides specific patterns and examples for refactoring the Wire codebase. Each section includes before/after code examples and step-by-step instructions.

---

## 1. Error Handling Refactoring

### Current Anti-Pattern

```go
// ❌ Current problematic pattern
func (s *NodeStore) Join(nodeID, httpAddr, addr string) error {
    // Multiple issues:
    // 1. Generic error messages
    // 2. No error wrapping
    // 3. Panic instead of error return
    
    if s.raft.State() != raft.Leader {
        return ErrNotLeader
    }
    
    server := raft.Server{
        ID:       raft.ServerID(nodeID),
        Address:  raft.ServerAddress(addr),
    }
    
    f := s.raft.AddVoter(server.ID, server.Address, 0, 0)
    if f.Error() != nil {
        s.logger.Error().Err(f.Error()).Msg("error when adding a voter")
        return f.Error() // No context about what failed
    }
    
    if httpAddr != "" {
        s.setNodeAPIAddr(nodeID, httpAddr)
    }
    
    return ErrNotImplemented // Confusing - appears to fail always
}
```

### Refactored Pattern

```go
// ✅ Improved error handling
func (s *NodeStore) Join(nodeID, httpAddr, addr string) error {
    // Validate inputs
    if nodeID == "" {
        return errors.New(errors.ErrCodeInvalidInput, "nodeID cannot be empty")
    }
    if addr == "" {
        return errors.New(errors.ErrCodeInvalidInput, "node address cannot be empty")
    }
    
    // Check leadership with context
    if s.raft.State() != raft.Leader {
        leaderAddr, _ := s.LeaderAddr()
        return errors.New(errors.ErrCodeNotLeader, "not the leader").
            WithDetail("leader", leaderAddr).
            WithDetail("action", "join")
    }
    
    // Create server configuration
    server := raft.Server{
        ID:       raft.ServerID(nodeID),
        Address:  raft.ServerAddress(addr),
    }
    
    // Add to Raft with proper error handling
    future := s.raft.AddVoter(server.ID, server.Address, 0, defaultTimeout)
    if err := future.Error(); err != nil {
        return errors.Wrap(err, errors.ErrCodeInternal, "failed to add voter").
            WithDetail("nodeID", nodeID).
            WithDetail("address", addr)
    }
    
    // Update metadata
    if httpAddr != "" {
        if err := s.setNodeAPIAddr(nodeID, httpAddr); err != nil {
            // Log but don't fail - node is already added to Raft
            s.logger.Warn().
                Err(err).
                Str("nodeID", nodeID).
                Str("httpAddr", httpAddr).
                Msg("failed to set node API address")
        }
    }
    
    s.logger.Info().
        Str("nodeID", nodeID).
        Str("raftAddr", addr).
        Str("httpAddr", httpAddr).
        Msg("node joined cluster")
    
    return nil
}
```

### Refactoring Steps

1. **Add input validation** at the beginning of functions
2. **Wrap errors with context** using the custom error types
3. **Include relevant details** in error messages
4. **Use structured logging** instead of string formatting
5. **Return early** on errors to reduce nesting
6. **Add success logging** for important operations

---

## 2. Resource Management Refactoring

### Current Anti-Pattern

```go
// ❌ Resource leak potential
func (p *Pipeline) Run(ctx context.Context) error {
    // No proper cleanup
    source, err := p.source.Connect()
    if err != nil {
        return err
    }
    
    sink, err := p.sink.Connect()
    if err != nil {
        // source not closed!
        return err
    }
    
    // Goroutine without lifecycle management
    go func() {
        for {
            data := source.Read()
            sink.Write(data)
        }
    }()
    
    return nil
}
```

### Refactored Pattern

```go
// ✅ Proper resource management
func (p *Pipeline) Run(ctx context.Context) error {
    // Setup cleanup
    ctx, cancel := context.WithCancel(ctx)
    defer cancel()
    
    g, ctx := errgroup.WithContext(ctx)
    
    // Connect source with cleanup
    source, err := p.source.Connect(ctx)
    if err != nil {
        return fmt.Errorf("connect source: %w", err)
    }
    defer func() {
        if err := source.Close(); err != nil {
            p.logger.Error().Err(err).Msg("failed to close source")
        }
    }()
    
    // Connect sink with cleanup
    sink, err := p.sink.Connect(ctx)
    if err != nil {
        return fmt.Errorf("connect sink: %w", err)
    }
    defer func() {
        if err := sink.Close(); err != nil {
            p.logger.Error().Err(err).Msg("failed to close sink")
        }
    }()
    
    // Managed goroutine
    g.Go(func() error {
        return p.process(ctx, source, sink)
    })
    
    // Wait for completion or error
    return g.Wait()
}

func (p *Pipeline) process(ctx context.Context, source Source, sink Sink) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            data, err := source.Read(ctx)
            if err != nil {
                if errors.Is(err, io.EOF) {
                    return nil
                }
                return fmt.Errorf("read source: %w", err)
            }
            
            if err := sink.Write(ctx, data); err != nil {
                return fmt.Errorf("write sink: %w", err)
            }
        }
    }
}
```

### Refactoring Steps

1. **Use defer for cleanup** immediately after resource acquisition
2. **Use errgroup** for goroutine management
3. **Pass context** to all operations
4. **Check context cancellation** in loops
5. **Handle cleanup errors** separately (log but don't fail)
6. **Use structured concurrency** patterns

---

## 3. Performance Refactoring

### Current Anti-Pattern

```go
// ❌ Inefficient processing
func (p *Pipeline) ProcessBatch(jobs []*Job) error {
    for _, job := range jobs {
        // Synchronous processing
        data, err := json.Marshal(job)
        if err != nil {
            return err
        }
        
        // No batching
        if err := p.sink.Write(data); err != nil {
            return err
        }
        
        // Synchronous wait
        p.waitGroup.Wait()
    }
    return nil
}
```

### Refactored Pattern

```go
// ✅ Optimized processing
type BatchProcessor struct {
    batchSize    int
    flushTimeout time.Duration
    parallelism  int
    
    jobPool      *ObjectPool[*Job]
    encoder      *json.Encoder
    buffer       *bytes.Buffer
    mu           sync.Mutex
}

func (p *Pipeline) ProcessBatch(ctx context.Context, jobs <-chan *Job) error {
    // Create worker pool
    sem := make(chan struct{}, p.parallelism)
    g, ctx := errgroup.WithContext(ctx)
    
    // Batch accumulator
    batch := make([]*Job, 0, p.batchSize)
    timer := time.NewTimer(p.flushTimeout)
    defer timer.Stop()
    
    // Process loop
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
            
        case job, ok := <-jobs:
            if !ok {
                // Flush remaining batch
                if len(batch) > 0 {
                    if err := p.flushBatch(ctx, batch, sem, g); err != nil {
                        return err
                    }
                }
                return g.Wait()
            }
            
            batch = append(batch, job)
            
            if len(batch) >= p.batchSize {
                if err := p.flushBatch(ctx, batch, sem, g); err != nil {
                    return err
                }
                batch = batch[:0]
                timer.Reset(p.flushTimeout)
            }
            
        case <-timer.C:
            if len(batch) > 0 {
                if err := p.flushBatch(ctx, batch, sem, g); err != nil {
                    return err
                }
                batch = batch[:0]
            }
            timer.Reset(p.flushTimeout)
        }
    }
}

func (p *Pipeline) flushBatch(ctx context.Context, batch []*Job, sem chan struct{}, g *errgroup.Group) error {
    // Copy batch for async processing
    jobs := make([]*Job, len(batch))
    copy(jobs, batch)
    
    g.Go(func() error {
        // Acquire semaphore
        select {
        case sem <- struct{}{}:
            defer func() { <-sem }()
        case <-ctx.Done():
            return ctx.Err()
        }
        
        // Process batch
        return p.processBatchAsync(ctx, jobs)
    })
    
    return nil
}

func (p *Pipeline) processBatchAsync(ctx context.Context, jobs []*Job) error {
    // Get buffer from pool
    buf := p.bufferPool.Get()
    defer p.bufferPool.Put(buf)
    
    // Encode batch
    encoder := json.NewEncoder(buf)
    for _, job := range jobs {
        if err := encoder.Encode(job); err != nil {
            return fmt.Errorf("encode job: %w", err)
        }
    }
    
    // Write batch
    return p.sink.WriteBatch(ctx, buf.Bytes())
}
```

### Refactoring Steps

1. **Implement batching** to reduce I/O operations
2. **Use object pools** to reduce allocations
3. **Add parallelism** with controlled concurrency
4. **Use channels** for job distribution
5. **Implement timeouts** for batch flushing
6. **Profile and measure** performance improvements

---

## 4. API Design Refactoring

### Current Anti-Pattern

```go
// ❌ Inconsistent API
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Manual routing
    if r.URL.Path == "/status" && r.Method == "GET" {
        s.handleStatus(w, r)
    } else if r.URL.Path == "/key" && r.Method == "POST" {
        // Test endpoint in production!
        key := r.URL.Query().Get("key")
        value := r.URL.Query().Get("value")
        s.test(key, value)
        w.Write([]byte("ok"))
    } else {
        http.Error(w, "not found", 404)
    }
}
```

### Refactored Pattern

```go
// ✅ Clean API design
type API struct {
    router      *gin.Engine
    middlewares []gin.HandlerFunc
}

func NewAPI(config APIConfig) *API {
    router := gin.New()
    
    // Global middleware
    router.Use(
        middleware.RequestID(),
        middleware.Logger(),
        middleware.Recovery(),
        middleware.RateLimit(config.RateLimit),
    )
    
    api := &API{router: router}
    
    // Version 1 API
    v1 := router.Group("/api/v1")
    {
        // Public endpoints
        public := v1.Group("")
        {
            public.GET("/health", api.handleHealth)
            public.GET("/ready", api.handleReady)
        }
        
        // Authenticated endpoints
        auth := v1.Group("")
        auth.Use(middleware.Authenticate(config.Auth))
        {
            // Cluster management
            cluster := auth.Group("/cluster")
            {
                cluster.GET("/status", api.handleClusterStatus)
                cluster.POST("/nodes", api.handleNodeJoin)
                cluster.DELETE("/nodes/:id", api.handleNodeRemove)
            }
            
            // Pipeline management
            pipelines := auth.Group("/pipelines")
            {
                pipelines.GET("", api.handleListPipelines)
                pipelines.POST("", api.handleCreatePipeline)
                pipelines.GET("/:id", api.handleGetPipeline)
                pipelines.PUT("/:id", api.handleUpdatePipeline)
                pipelines.DELETE("/:id", api.handleDeletePipeline)
                pipelines.POST("/:id/start", api.handleStartPipeline)
                pipelines.POST("/:id/stop", api.handleStopPipeline)
            }
        }
    }
    
    return api
}

// Consistent error response
type ErrorResponse struct {
    Error     string                 `json:"error"`
    Code      string                 `json:"code"`
    Details   map[string]interface{} `json:"details,omitempty"`
    RequestID string                 `json:"request_id"`
}

func (api *API) handleError(c *gin.Context, err error) {
    var wireErr *errors.WireError
    if !errors.As(err, &wireErr) {
        wireErr = errors.Wrap(err, errors.ErrCodeInternal, "internal error")
    }
    
    status := api.errorToHTTPStatus(wireErr.Code)
    
    c.JSON(status, ErrorResponse{
        Error:     wireErr.Message,
        Code:      string(wireErr.Code),
        Details:   wireErr.Details,
        RequestID: middleware.GetRequestID(c),
    })
}

func (api *API) handleCreatePipeline(c *gin.Context) {
    var req CreatePipelineRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        api.handleError(c, errors.Wrap(err, errors.ErrCodeInvalidInput, "invalid request"))
        return
    }
    
    // Validate request
    if err := req.Validate(); err != nil {
        api.handleError(c, err)
        return
    }
    
    // Create pipeline
    pipeline, err := api.pipelineService.Create(c.Request.Context(), req)
    if err != nil {
        api.handleError(c, err)
        return
    }
    
    c.JSON(http.StatusCreated, PipelineResponse{
        ID:        pipeline.ID,
        Name:      pipeline.Name,
        State:     pipeline.State,
        CreatedAt: pipeline.CreatedAt,
    })
}
```

### Refactoring Steps

1. **Use a proper router** (Gin, Chi, etc.)
2. **Implement API versioning** from the start
3. **Standardize error responses** across all endpoints
4. **Add request validation** middleware
5. **Use structured request/response types**
6. **Remove test endpoints** from production code
7. **Add comprehensive middleware** (auth, logging, rate limiting)

---

## 5. Testing Refactoring

### Current Anti-Pattern

```go
// ❌ Fragile tests
func TestPipeline(t *testing.T) {
    // Direct database access
    db, _ := badger.Open(badger.DefaultOptions("/tmp/test"))
    
    // No cleanup
    pipeline := &Pipeline{
        source: &KafkaSource{
            brokers: []string{"localhost:9092"}, // Hardcoded
        },
    }
    
    // Sleep-based synchronization
    go pipeline.Run(context.Background())
    time.Sleep(2 * time.Second)
    
    // No assertions
    fmt.Println("test passed")
}
```

### Refactored Pattern

```go
// ✅ Robust tests
func TestPipeline(t *testing.T) {
    // Use test fixtures
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    // Create test environment
    env := testutil.NewTestEnv(t)
    defer env.Cleanup()
    
    // Mock dependencies
    mockSource := mocks.NewMockSource(t)
    mockSink := mocks.NewMockSink(t)
    
    // Set expectations
    jobs := generateTestJobs(10)
    mockSource.EXPECT().
        Read(gomock.Any()).
        Return(jobs, nil).
        Times(len(jobs))
    
    mockSink.EXPECT().
        Write(gomock.Any(), gomock.Any()).
        Return(nil).
        Times(len(jobs))
    
    // Create pipeline with mocks
    pipeline := NewPipeline(
        WithSource(mockSource),
        WithSink(mockSink),
        WithLogger(testutil.TestLogger(t)),
    )
    
    // Run with proper synchronization
    errCh := make(chan error, 1)
    go func() {
        errCh <- pipeline.Run(ctx)
    }()
    
    // Wait for completion or timeout
    select {
    case err := <-errCh:
        require.NoError(t, err)
    case <-ctx.Done():
        t.Fatal("test timeout")
    }
    
    // Verify expectations
    mockSource.AssertExpectations(t)
    mockSink.AssertExpectations(t)
}

// Table-driven tests
func TestPipelineStates(t *testing.T) {
    tests := []struct {
        name          string
        initialState  PipelineState
        action        func(*Pipeline) error
        expectedState PipelineState
        expectError   bool
    }{
        {
            name:          "start created pipeline",
            initialState:  PipelineStateCreated,
            action:        (*Pipeline).Start,
            expectedState: PipelineStateRunning,
            expectError:   false,
        },
        {
            name:          "stop running pipeline",
            initialState:  PipelineStateRunning,
            action:        (*Pipeline).Stop,
            expectedState: PipelineStopped,
            expectError:   false,
        },
        {
            name:          "start failed pipeline",
            initialState:  PipelineFailed,
            action:        (*Pipeline).Start,
            expectedState: PipelineFailed,
            expectError:   true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            pipeline := &Pipeline{state: tt.initialState}
            
            err := tt.action(pipeline)
            
            if tt.expectError {
                require.Error(t, err)
            } else {
                require.NoError(t, err)
            }
            
            assert.Equal(t, tt.expectedState, pipeline.state)
        })
    }
}
```

### Refactoring Steps

1. **Use test fixtures** and helpers
2. **Mock external dependencies**
3. **Use table-driven tests** for multiple scenarios
4. **Add proper assertions** with require/assert
5. **Use context with timeout** instead of sleep
6. **Clean up resources** in defer blocks
7. **Test error cases** explicitly

---

## 6. Configuration Refactoring

### Current Anti-Pattern

```go
// ❌ Scattered configuration
func main() {
    // Hardcoded values
    addr := "localhost:8080"
    if os.Getenv("ADDR") != "" {
        addr = os.Getenv("ADDR")
    }
    
    // Mixed sources
    flag.StringVar(&raftAddr, "raft-addr", "localhost:7000", "")
    
    // No validation
    timeout, _ := strconv.Atoi(os.Getenv("TIMEOUT"))
}
```

### Refactored Pattern

```go
// ✅ Centralized configuration
type Config struct {
    HTTP     HTTPConfig     `mapstructure:"http"`
    Raft     RaftConfig     `mapstructure:"raft"`
    Pipeline PipelineConfig `mapstructure:"pipeline"`
    Log      LogConfig      `mapstructure:"log"`
}

type HTTPConfig struct {
    Address         string        `mapstructure:"address" validate:"required,hostname_port"`
    ReadTimeout     time.Duration `mapstructure:"read_timeout" default:"30s"`
    WriteTimeout    time.Duration `mapstructure:"write_timeout" default:"30s"`
    ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout" default:"10s"`
}

func LoadConfig() (*Config, error) {
    k := koanf.New(".")
    
    // Load defaults
    if err := k.Load(structs.Provider(defaultConfig(), "mapstructure"), nil); err != nil {
        return nil, fmt.Errorf("load defaults: %w", err)
    }
    
    // Load from file
    if err := k.Load(file.Provider("config.yaml"), yaml.Parser()); err != nil {
        if !os.IsNotExist(err) {
            return nil, fmt.Errorf("load config file: %w", err)
        }
    }
    
    // Load from environment
    if err := k.Load(env.Provider("WIRE_", ".", func(s string) string {
        return strings.Replace(strings.ToLower(
            strings.TrimPrefix(s, "WIRE_")), "_", ".", -1)
    }), nil); err != nil {
        return nil, fmt.Errorf("load env: %w", err)
    }
    
    // Load from flags
    fs := flag.NewFlagSet("wire", flag.ContinueOnError)
    defineFlags(fs)
    if err := fs.Parse(os.Args[1:]); err != nil {
        return nil, fmt.Errorf("parse flags: %w", err)
    }
    if err := k.Load(posflag.Provider(fs, ".", k), nil); err != nil {
        return nil, fmt.Errorf("load flags: %w", err)
    }
    
    // Unmarshal to struct
    var config Config
    if err := k.Unmarshal("", &config); err != nil {
        return nil, fmt.Errorf("unmarshal config: %w", err)
    }
    
    // Validate
    if err := validateConfig(&config); err != nil {
        return nil, fmt.Errorf("validate config: %w", err)
    }
    
    return &config, nil
}

func validateConfig(cfg *Config) error {
    validate := validator.New()
    
    // Register custom validators
    validate.RegisterValidation("hostname_port", validateHostPort)
    
    if err := validate.Struct(cfg); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }
    
    // Business logic validation
    if cfg.Raft.HeartbeatTimeout >= cfg.Raft.ElectionTimeout {
        return errors.New("heartbeat timeout must be less than election timeout")
    }
    
    return nil
}
```

### Refactoring Steps

1. **Define configuration structs** with proper types
2. **Use a configuration library** (Viper, Koanf)
3. **Support multiple sources** with precedence
4. **Add validation** for all fields
5. **Use defaults** for optional fields
6. **Support hot reloading** where appropriate
7. **Document all configuration options**

---

## Common Refactoring Patterns

### 1. Extract Method
```go
// Before
func process(data []byte) error {
    // 50 lines of parsing logic
    // 30 lines of validation
    // 40 lines of transformation
}

// After
func process(data []byte) error {
    parsed, err := parse(data)
    if err != nil {
        return fmt.Errorf("parse: %w", err)
    }
    
    if err := validate(parsed); err != nil {
        return fmt.Errorf("validate: %w", err)
    }
    
    return transform(parsed)
}
```

### 2. Replace Magic Numbers
```go
// Before
if len(buffer) > 1048576 {
    flush()
}

// After
const maxBufferSize = 1 * 1024 * 1024 // 1MB

if len(buffer) > maxBufferSize {
    flush()
}
```

### 3. Introduce Parameter Object
```go
// Before
func NewPipeline(name string, source Source, sink Sink, 
    parallelism int, bufferSize int, timeout time.Duration) *Pipeline

// After
type PipelineConfig struct {
    Name        string
    Source      Source
    Sink        Sink
    Parallelism int
    BufferSize  int
    Timeout     time.Duration
}

func NewPipeline(config PipelineConfig) *Pipeline
```

### 4. Replace Conditional with Polymorphism
```go
// Before
func process(job *Job) error {
    switch job.Type {
    case "transform":
        // transform logic
    case "filter":
        // filter logic
    case "aggregate":
        // aggregate logic
    }
}

// After
type Processor interface {
    Process(job *Job) error
}

type TransformProcessor struct{}
type FilterProcessor struct{}
type AggregateProcessor struct{}

func process(job *Job, processor Processor) error {
    return processor.Process(job)
}
```

---

## Refactoring Checklist

Before submitting refactored code, ensure:

- [ ] All tests pass
- [ ] No new linting warnings
- [ ] Performance benchmarks show no regression
- [ ] Error handling is consistent
- [ ] Resources are properly managed
- [ ] Code follows project conventions
- [ ] Documentation is updated
- [ ] Breaking changes are documented
- [ ] Commit messages explain the why

---

## Tools and Commands

```bash
# Run tests with race detection
go test -race ./...

# Check for common issues
golangci-lint run

# Format code
gofmt -w -s .

# Update dependencies
go mod tidy

# Generate mocks
mockgen -source=interfaces.go -destination=mocks/interfaces.go

# Run benchmarks
go test -bench=. -benchmem ./...

# Profile memory usage
go test -memprofile mem.prof -bench .
go tool pprof mem.prof

# Check test coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

This refactoring guide provides concrete patterns and examples for improving the Wire codebase systematically.