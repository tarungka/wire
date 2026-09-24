package sdk

// YAML config values contain scalar values, maps and lists. Clone containers
// recursively so a connector can normalize its config without racing siblings
// or changing the configuration used by a later execution.
func clonePipelineValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return clonePipelineConfig(value)
	case map[any]any:
		result := make(map[any]any, len(value))
		for key, item := range value {
			result[key] = clonePipelineValue(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = clonePipelineValue(item)
		}
		return result
	default:
		return value
	}
}
func clonePipelineConfig(config map[string]any) map[string]any {
	if config == nil {
		return nil
	}
	result := make(map[string]any, len(config))
	for key, value := range config {
		result[key] = clonePipelineValue(value)
	}
	return result
}
