package sdk

// mockValueState is an in-memory ValueState implementation for testing.
type mockValueState struct {
	value []byte
}

func (s *mockValueState) Get() []byte      { return s.value }
func (s *mockValueState) Set(value []byte) { s.value = value }
func (s *mockValueState) Clear()           { s.value = nil }

// mockListState is an in-memory ListState implementation for testing.
type mockListState struct {
	items [][]byte
}

func (s *mockListState) Get() [][]byte    { return s.items }
func (s *mockListState) Add(value []byte) { s.items = append(s.items, value) }
func (s *mockListState) Clear()           { s.items = nil }

// mockMapState is an in-memory MapState implementation for testing.
type mockMapState struct {
	data map[string][]byte
}

func (s *mockMapState) Get(key string) []byte        { return s.data[key] }
func (s *mockMapState) Put(key string, value []byte) { s.data[key] = value }
func (s *mockMapState) Delete(key string)            { delete(s.data, key) }
func (s *mockMapState) Keys() []string {
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}
func (s *mockMapState) Clear() { s.data = make(map[string][]byte) }

// mockProcessContext is an in-memory ProcessContext for testing.
type mockProcessContext struct {
	key    []byte
	values map[string]*mockValueState
	lists  map[string]*mockListState
	maps   map[string]*mockMapState
}

// NewMockProcessContext creates a ProcessContext backed by in-memory state.
func NewMockProcessContext(key []byte) ProcessContext {
	return &mockProcessContext{
		key:    key,
		values: make(map[string]*mockValueState),
		lists:  make(map[string]*mockListState),
		maps:   make(map[string]*mockMapState),
	}
}

func (c *mockProcessContext) Key() []byte { return c.key }

func (c *mockProcessContext) GetValueState(name string) ValueState {
	if _, ok := c.values[name]; !ok {
		c.values[name] = &mockValueState{}
	}
	return c.values[name]
}

func (c *mockProcessContext) GetListState(name string) ListState {
	if _, ok := c.lists[name]; !ok {
		c.lists[name] = &mockListState{}
	}
	return c.lists[name]
}

func (c *mockProcessContext) GetMapState(name string) MapState {
	if _, ok := c.maps[name]; !ok {
		c.maps[name] = &mockMapState{data: make(map[string][]byte)}
	}
	return c.maps[name]
}

// Compile-time interface checks.
var (
	_ ValueState     = (*mockValueState)(nil)
	_ ListState      = (*mockListState)(nil)
	_ MapState       = (*mockMapState)(nil)
	_ ProcessContext = (*mockProcessContext)(nil)
)
