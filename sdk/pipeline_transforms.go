package sdk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
)

func compilePipelineTransform(node *StreamNode, op pipelineOperator, env *cel.Env) error {
	allowed := map[string][]string{
		"json-parse": {"target-field"}, "filter": {"expression"}, "map": {"expression"}, "flat-map": {"expression"}, "key-by": {"key-expression"},
		"select": {"fields"}, "rename": {"mappings"}, "tumbling-window": {"size", "aggregation"}, "sliding-window": {"size", "slide", "aggregation"}, "session-window": {"gap", "aggregation"},
	}
	fields, ok := allowed[op.Type]
	if !ok {
		return fmt.Errorf("unknown transform type %q", op.Type)
	}
	for key := range op.Config {
		valid := false
		for _, field := range fields {
			valid = valid || field == key
		}
		if !valid {
			return fmt.Errorf("unknown config field %q", key)
		}
	}
	required := func(name string) (string, error) {
		value, ok := op.Config[name].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("%s is required and must be a string", name)
		}
		return value, nil
	}
	switch op.Type {
	case "json-parse":
		target, err := required("target-field")
		if err != nil {
			return err
		}
		if !pipelineIdentifier.MatchString(target) || target == "key" || target == "value" || target == "event_time" || target == "headers" {
			return fmt.Errorf("invalid or reserved target-field %q", target)
		}
		node.Type = NodeMap
		node.MapFn = func(event Event) (Event, error) {
			value, err := decodePipelineJSON(event.Value)
			if err != nil {
				return event, err
			}
			event.Value, err = json.Marshal(map[string]any{target: value})
			return event, err
		}
	case "filter", "map", "flat-map", "key-by":
		field := "expression"
		if op.Type == "key-by" {
			field = "key-expression"
		}
		expression, err := required(field)
		if err != nil {
			return err
		}
		program, err := compilePipelineExpression(env, expression)
		if err != nil {
			return err
		}
		switch op.Type {
		case "filter":
			node.Type = NodeFilter
			node.FilterFn = func(event Event) (bool, error) {
				value, err := evaluatePipelineExpression(program, event)
				if err != nil {
					return false, err
				}
				keep, ok := value.(bool)
				if !ok {
					return false, fmt.Errorf("filter expression must produce bool")
				}
				return keep, nil
			}
		case "map":
			node.Type = NodeMap
			node.MapFn = func(event Event) (Event, error) {
				value, err := evaluatePipelineExpression(program, event)
				if err != nil {
					return event, err
				}
				event.Value, err = json.Marshal(value)
				return event, err
			}
		case "flat-map":
			node.Type = NodeFlatMap
			node.FlatMapFn = func(event Event) ([]Event, error) {
				value, err := evaluatePipelineExpression(program, event)
				if err != nil {
					return nil, err
				}
				values, ok := value.([]any)
				if !ok {
					return nil, fmt.Errorf("flat-map expression must produce a list")
				}
				result := make([]Event, len(values))
				for i, item := range values {
					out := event
					out.Value, err = json.Marshal(item)
					if err != nil {
						return nil, err
					}
					result[i] = out
				}
				return result, nil
			}
		case "key-by":
			node.Type = NodeKeyBy
			node.KeyByFn = func(event Event) ([]byte, error) {
				value, err := evaluatePipelineExpression(program, event)
				if err != nil {
					return nil, err
				}
				switch value := value.(type) {
				case string:
					return []byte(value), nil
				case int64, uint64:
					return []byte(fmt.Sprint(value)), nil
				default:
					return nil, fmt.Errorf("key-expression must produce string or integer")
				}
			}
		}
	case "select":
		raw, ok := op.Config["fields"].([]any)
		if !ok || len(raw) == 0 {
			return fmt.Errorf("fields must be a nonempty list")
		}
		paths := make([]string, len(raw))
		for i, item := range raw {
			path, ok := item.(string)
			if !ok || path == "" {
				return fmt.Errorf("fields must contain nonempty paths")
			}
			for _, part := range strings.Split(path, ".") {
				if !pipelineIdentifier.MatchString(part) {
					return fmt.Errorf("invalid field path %q", path)
				}
			}
			paths[i] = path
		}
		node.Type = NodeMap
		node.MapFn = func(event Event) (Event, error) {
			value, err := decodePipelineJSON(event.Value)
			if err != nil {
				return event, err
			}
			result := map[string]any{}
			for _, path := range paths {
				item := value
				for _, part := range strings.Split(path, ".") {
					object, ok := item.(map[string]any)
					if !ok {
						return event, fmt.Errorf("missing field %q", path)
					}
					item, ok = object[part]
					if !ok {
						return event, fmt.Errorf("missing field %q", path)
					}
				}
				result[path] = item
			}
			event.Value, err = json.Marshal(result)
			return event, err
		}
	case "rename":
		mappings, ok := op.Config["mappings"].(map[string]any)
		if !ok || len(mappings) == 0 {
			return fmt.Errorf("mappings must be a nonempty object")
		}
		names := []string{}
		targets := map[string]string{}
		seen := map[string]bool{}
		for name, item := range mappings {
			target, ok := item.(string)
			if !ok || !pipelineIdentifier.MatchString(name) || !pipelineIdentifier.MatchString(target) || seen[target] {
				return fmt.Errorf("rename requires unique top-level field names")
			}
			seen[target] = true
			targets[name] = target
			names = append(names, name)
		}
		sort.Strings(names)
		node.Type = NodeMap
		node.MapFn = func(event Event) (Event, error) {
			value, err := decodePipelineJSON(event.Value)
			if err != nil {
				return event, err
			}
			object, ok := value.(map[string]any)
			if !ok {
				return event, fmt.Errorf("rename requires JSON object")
			}
			result := map[string]any{}
			for key, item := range object {
				if _, renamed := targets[key]; !renamed {
					result[key] = item
				}
			}
			for _, name := range names {
				item, exists := object[name]
				if !exists {
					return event, fmt.Errorf("missing field %q", name)
				}
				target := targets[name]
				if _, exists = result[target]; exists {
					return event, fmt.Errorf("rename overwrites field %q", target)
				}
				result[target] = item
			}
			event.Value, err = json.Marshal(result)
			return event, err
		}
	case "tumbling-window", "sliding-window", "session-window":
		duration := func(name string) (time.Duration, error) {
			raw, err := required(name)
			if err != nil {
				return 0, err
			}
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 {
				return 0, fmt.Errorf("%s must be a positive duration", name)
			}
			return value, nil
		}
		aggregation, err := required("aggregation")
		if err != nil {
			return err
		}
		switch aggregation {
		case "count":
			node.Aggregator = CountAggregator{}
		case "sum":
			node.Aggregator = SumAggregator{}
		case "min":
			node.Aggregator = MinAggregator{}
		case "max":
			node.Aggregator = MaxAggregator{}
		default:
			return fmt.Errorf("unknown aggregation %q", aggregation)
		}
		node.Type = NodeWindow
		switch op.Type {
		case "tumbling-window":
			size, err := duration("size")
			if err != nil {
				return err
			}
			node.Window = TumblingWindow(size)
		case "sliding-window":
			size, err := duration("size")
			if err != nil {
				return err
			}
			slide, err := duration("slide")
			if err != nil {
				return err
			}
			node.Window = SlidingWindow(size, slide)
		case "session-window":
			gap, err := duration("gap")
			if err != nil {
				return err
			}
			node.Window = SessionWindow(gap)
		}
	}
	return nil
}
