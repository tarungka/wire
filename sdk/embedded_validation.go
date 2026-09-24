package sdk

import (
	"fmt"
	"reflect"
)

func nilConnector(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Func, reflect.Interface, reflect.Slice, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}
func (g *StreamGraph) validateForEmbedded() error {
	for _, node := range g.nodes {
		valid := true
		switch node.Type {
		case NodeSource:
			valid = !nilConnector(node.Source) || node.SourceFactory != nil
		case NodeSink:
			valid = !nilConnector(node.Sink) || node.SinkFactory != nil
		case NodeMap:
			valid = node.MapFn != nil
		case NodeFlatMap:
			valid = node.FlatMapFn != nil
		case NodeFilter:
			valid = node.FilterFn != nil
		case NodeKeyBy:
			valid = node.KeyByFn != nil
		case NodeProcess:
			valid = node.ProcessFn != nil
		}
		if !valid {
			return fmt.Errorf("%w: embedded operator %q requires a function or connector instance", ErrInvalidConfig, node.Name)
		}
	}
	return nil
}
