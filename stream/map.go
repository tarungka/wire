package stream

// MapFunction is a function that maps an event to another event.
type MapFunction func(event Event) Event

// MapOperator is an operator that applies a function to each event in the stream.
type MapOperator struct {
	BaseOperator
	mapFn MapFunction
}

// NewMapOperator creates a new MapOperator.
func NewMapOperator(id string, mapFn MapFunction) *MapOperator {
	return &MapOperator{
		BaseOperator: *NewBaseOperator(id),
		mapFn:        mapFn,
	}
}

// Process processes an event.
func (o *MapOperator) Process(event Event) Event {
	return o.mapFn(event)
}
