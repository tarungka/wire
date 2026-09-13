package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

var pipelineIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func newPipelineExpressionEnv(variables []string) (*cel.Env, error) {
	options := []cel.EnvOption{cel.ParserRecursionLimit(100), cel.ParserExpressionSizeLimit(16384)}
	seen := map[string]bool{}
	for _, name := range variables {
		if !pipelineIdentifier.MatchString(name) {
			return nil, fmt.Errorf("invalid field identifier %q", name)
		}
		if !seen[name] {
			options = append(options, cel.Variable(name, cel.DynType))
			seen[name] = true
		}
	}
	return cel.NewEnv(options...)
}
func compilePipelineExpression(env *cel.Env, expression string) (cel.Program, error) {
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}
	return env.Program(ast, cel.CostLimit(100000))
}
func decodePipelineJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("expected one JSON value")
	}
	return normalizePipelineJSON(value)
}
func normalizePipelineJSON(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		if number, err := value.Int64(); err == nil {
			return number, nil
		}
		if number, err := strconv.ParseUint(string(value), 10, 64); err == nil {
			return number, nil
		}
		if !strings.ContainsAny(string(value), ".eE") {
			return nil, fmt.Errorf("JSON integer exceeds 64-bit range")
		}
		return value.Float64()
	case []any:
		for i, item := range value {
			converted, err := normalizePipelineJSON(item)
			if err != nil {
				return nil, err
			}
			value[i] = converted
		}
		return value, nil
	case map[string]any:
		for name, item := range value {
			converted, err := normalizePipelineJSON(item)
			if err != nil {
				return nil, err
			}
			value[name] = converted
		}
		return value, nil
	default:
		return value, nil
	}
}
func evaluatePipelineExpression(program cel.Program, event Event) (any, error) {
	value, err := decodePipelineJSON(event.Value)
	if err != nil {
		value = string(event.Value)
	}
	vars := map[string]any{}
	if fields, ok := value.(map[string]any); ok {
		for key, field := range fields {
			vars[key] = field
		}
	}
	vars["key"] = string(event.Key)
	vars["value"] = value
	vars["event_time"] = event.EventTime
	headers := map[string]string{}
	for key, value := range event.Headers {
		headers[key] = string(value)
	}
	vars["headers"] = headers
	out, _, err := program.Eval(vars)
	if err != nil {
		return nil, err
	}
	return pipelineCELValue(out)
}
func pipelineCELValue(value ref.Val) (any, error) {
	switch value := value.(type) {
	case types.Null:
		return nil, nil
	case types.String:
		return string(value), nil
	case types.Bool:
		return bool(value), nil
	case types.Int:
		return int64(value), nil
	case types.Uint:
		return uint64(value), nil
	case types.Double:
		return float64(value), nil
	case traits.Lister:
		result := []any{}
		it := value.Iterator()
		for it.HasNext() == types.True {
			item, err := pipelineCELValue(it.Next())
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
		return result, nil
	case traits.Mapper:
		result := map[string]any{}
		it := value.Iterator()
		for it.HasNext() == types.True {
			key := it.Next()
			name, ok := key.(types.String)
			if !ok {
				return nil, fmt.Errorf("JSON expression map keys must be strings")
			}
			item, err := pipelineCELValue(value.Get(key))
			if err != nil {
				return nil, err
			}
			result[string(name)] = item
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expression result type %s is not JSON-compatible", value.Type())
	}
}
