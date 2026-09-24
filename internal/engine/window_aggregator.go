package engine

// CheckedWindowAggregator allows functions such as SDK Reduce to report an
// error without committing a partly updated accumulator. Ordinary Aggregators
// retain their existing interface. All callbacks must be deterministic.
type CheckedWindowAggregator interface {
	WindowAggregator
	AddChecked([]byte, Event) ([]byte, error)
	MergeChecked([]byte, []byte) ([]byte, error)
	ResultChecked([]byte) ([]byte, error)
}

func (p *WindowProcessor) add(acc []byte, event Event) ([]byte, error) {
	if checked, ok := p.aggregator.(CheckedWindowAggregator); ok {
		return checked.AddChecked(acc, event)
	}
	return p.aggregator.Add(acc, event), nil
}
func (p *WindowProcessor) merge(a, b []byte) ([]byte, error) {
	if checked, ok := p.aggregator.(CheckedWindowAggregator); ok {
		return checked.MergeChecked(a, b)
	}
	return p.aggregator.Merge(a, b), nil
}
func (p *WindowProcessor) resultBytes(acc []byte) ([]byte, error) {
	if checked, ok := p.aggregator.(CheckedWindowAggregator); ok {
		return checked.ResultChecked(acc)
	}
	return p.aggregator.GetResult(acc), nil
}
