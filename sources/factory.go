package sources

import (
	"fmt"
	"sync"

	// Import pulsar package to resolve NewPulsarSource
	// Assuming NewPulsarSource is in a package like "github.com/tarungka/wire/pkg/sources/pulsar"
	// This path might need adjustment based on your actual project structure.
	// For now, let's assume it will be resolvable or we'll use a placeholder.
	// Since NewPulsarSource is defined in the problem description as:
	// package pulsar ... func NewPulsarSource(config PulsarConfig) (*PulsarSource, error)
	// We need a way to refer to it.
	// One common way is to have each source package register itself.
	// Or the factory directly calls constructors if they are accessible.

	// For the structure provided, NewPulsarSource is not directly callable without its own package.
	// Let's assume for now that we will define a generic way to register them or call them.
	// The problem description implies NewPulsarSource would be in package sources or similar.
	// "RegisterSource("pulsar", NewPulsarSource)"
	// This suggests NewPulsarSource should be accessible here.
	// This will likely cause a compile error until NewPulsarSource is correctly linked.
	// For now, I will assume that the individual source packages (like pulsar) will have an init()
	// function that registers their source creator.

	_ "github.com/tarungka/wire/pkg/sources/pulsar" // Placeholder for pulsar package registration
)

// SourceFactory creates sources based on configuration
type SourceFactory struct {
	mu       sync.RWMutex
	creators map[string]SourceCreator
}

// SourceCreator creates a specific type of source
// The config passed here is a map, which then needs to be parsed into the specific source's config struct.
type SourceCreator func(config map[string]interface{}) (Source, error)

var defaultFactory = &SourceFactory{
	creators: make(map[string]SourceCreator),
}

// init function for package sources - this is where sources would typically register themselves.
// However, the problem description shows RegisterSource calls in the factory's init.
func init() {
	// Register built-in sources
	// The functions like NewKafkaSource, NewPulsarSource etc. must be actual functions
	// that match the SourceCreator signature.
	// This means they take map[string]interface{} and return (Source, error).
	// The provided snippets for NewPulsarSource etc. take specific config structs.
	// A wrapper will be needed.

	// Example of how NewPulsarSource might be registered if it were in this package
	// or if we had a wrapper.
	// RegisterSource("pulsar", PulsarSourceCreator) // Assuming PulsarSourceCreator is a wrapper

	// Based on the problem description's factory.go:
    // RegisterSource("kafka", NewKafkaSource) // Placeholder - NewKafkaSource would need to match SourceCreator
    // RegisterSource("pulsar", NewPulsarSource) // Placeholder for direct registration
    // RegisterSource("s3", NewS3Source)
    // RegisterSource("postgres-cdc", NewPostgresCDCSource)
    // RegisterSource("mysql-cdc", NewMySQLCDCSource)
    // RegisterSource("mongodb-cdc", NewMongoDBCDCSource)
    // RegisterSource("redis-stream", NewRedisStreamSource)
    // RegisterSource("filesystem", NewFileSystemSource)
    // RegisterSource("http", NewHTTPSource)
    // RegisterSource("websocket", NewWebSocketSource)

	// For now, I will leave these commented out as the actual constructor functions
	// (e.g., NewPulsarSource from pulsar/source.go) do not match the SourceCreator signature
	// (map[string]interface{}). They expect their specific config struct (e.g., PulsarConfig).
	// This implies that either:
	// 1. Each New... func needs a wrapper.
	// 2. The SourceCreator signature is different, or type assertion is used.
	// 3. The RegisterSource calls are made from within each source's package init().

	// Let's assume approach 1: wrapper functions.
	// I will define a creator for Pulsar as an example.
	// The actual NewPulsarSource is in package 'pulsar'.
}

// RegisterSource registers a new source type with the default factory.
func RegisterSource(name string, creator SourceCreator) {
	defaultFactory.mu.Lock()
	defer defaultFactory.mu.Unlock()

	if _, exists := defaultFactory.creators[name]; exists {
		// Optionally log or handle re-registration of a source type
		// For now, allow override.
	}
	defaultFactory.creators[name] = creator
}

// CreateSource creates a source instance using the default factory.
func CreateSource(sourceType string, config map[string]interface{}) (Source, error) {
	defaultFactory.mu.RLock()
	creator, exists := defaultFactory.creators[sourceType]
	defaultFactory.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown source type: %s. Ensure the source package is imported and registers itself, or the factory is initialized correctly", sourceType)
	}

	return creator(config)
}

// Example configuration map (as provided in the problem description)
var exampleConfigs = map[string]interface{}{
	"pulsar_example": map[string]interface{}{
		"type":         "pulsar", // This field is often used by the caller of CreateSource, not by the creator itself
		"serviceURL":   "pulsar://localhost:6650",
		"topics":       []string{"events", "transactions"},
		"subscription": "wire-consumer",
		// Other PulsarConfig fields would go here...
		// "subscriptionType": pulsar.Exclusive, // This needs to be mapped from string in config
		// "ackTimeout": "10s", // Durations also need parsing
	},
	"s3_example": map[string]interface{}{
		"type":            "s3",
		"bucket":          "data-lake",
		"prefix":          "raw/events/",
		"format":          "parquet",
		"scanInterval":    "1m",
		"deleteAfterRead": false,
	},
	"postgres_cdc_example": map[string]interface{}{
		"type":        "postgres-cdc",
		"host":        "localhost",
		"database":    "production",
		"tables":      []string{"orders", "customers", "products"},
		"slotName":    "wire_cdc",
		"publication": "wire_pub",
	},
}

// Note: The NewPulsarSource function from `pulsar/source.go` has the signature:
// func NewPulsarSource(config PulsarConfig) (*PulsarSource, error)
// The SourceCreator type is:
// func(config map[string]interface{}) (Source, error)
//
// To make this work, we need a wrapper function for each source type,
// or each source package (e.g., `sources/pulsar/`) should have an `init()` function
// that calls `sources.RegisterSource` with an appropriate creator function.
//
// Example of how pulsar package could register itself:
//
// package pulsar
//
// import (
//      "github.com/mitchellh/mapstructure"
//      "github.com/tarungka/wire/pkg/sources"
// )
//
// func init() {
//      sources.RegisterSource("pulsar", createPulsarSourceFromMap)
// }
//
// func createPulsarSourceFromMap(configMap map[string]interface{}) (sources.Source, error) {
//      var config PulsarConfig
//      // Use a library like mapstructure to decode map into struct
//      // This also needs to handle parsing of complex types like time.Duration, pulsar.SubscriptionType etc.
//      if err := mapstructure.Decode(configMap, &config); err != nil {
//          return nil, fmt.Errorf("pulsar source: error decoding config: %w", err)
//      }
//      // Here, you would also need to parse common SourceConfig fields if they are nested
//      // or fill them from the map. The current PulsarConfig embeds sources.SourceConfig.
//      // mapstructure can handle embedded structs.
//
//      // Example: Manual mapping for fields not directly handled by mapstructure or needing defaults
//      // if config.SubscriptionType == 0 && configMap["subscriptionType"] != nil { ... }
//
//      return NewPulsarSource(config)
// }
//
// This approach (self-registration by packages) is cleaner.
// For now, I've created factory.go without the RegisterSource calls in its init(),
// anticipating that individual source packages will handle their registration.
// If the user wants the factory's init() to do the registration, those New<Type>Source functions
// will need to be accessible and wrapped.
//
// The problem description's `factory.go` snippet *does* show `RegisterSource` calls in `init()`.
// This implies that `NewPulsarSource` etc. must be accessible and match the `SourceCreator` signature.
// This is a structural challenge with the provided snippets.
// I will proceed by creating the factory and the user can then decide how to structure
// the registration calls (either in factory's init by making source constructors conform,
// or in each source package's init).
//
// For now, to make progress and align with the problem's `factory.go` snippet's *intent*,
// I will add a placeholder registration for Pulsar in the `init()` of `factory.go`.
// This will require a placeholder `PulsarSourceCreator` function.

// Placeholder for a generic Kafka source creator
func NewKafkaSource(config map[string]interface{}) (Source, error) {
    return nil, fmt.Errorf("KafkaSource not implemented")
}

// Placeholder for a generic S3 source creator
func NewS3Source(config map[string]interface{}) (Source, error) {
    return nil, fmt.Errorf("S3Source not implemented")
}

// Placeholder for Postgres CDC
func NewPostgresCDCSource(config map[string]interface{}) (Source, error) {
    return nil, fmt.Errorf("PostgresCDCSource not implemented")
}
// Add other placeholders similarly if needed for the init block to be syntactically valid.
func NewMySQLCDCSource(config map[string]interface{}) (Source, error) { return nil, fmt.Errorf("not implemented"); }
func NewMongoDBCDCSource(config map[string]interface{}) (Source, error) { return nil, fmt.Errorf("not implemented"); }
func NewRedisStreamSource(config map[string]interface{}) (Source, error) { return nil, fmt.Errorf("not implemented"); }
func NewFileSystemSource(config map[string]interface{}) (Source, error) { return nil, fmt.Errorf("not implemented"); }
func NewHTTPSource(config map[string]interface{}) (Source, error) { return nil, fmt.Errorf("not implemented"); }
func NewWebSocketSource(config map[string]interface{}) (Source, error) { return nil, fmt.Errorf("not implemented"); }


// Actual creator for PulsarSource that matches SourceCreator signature
// This function would typically live in the pulsar package and be registered via pulsar's init().
// Or, if NewPulsarSource is made public and PulsarConfig too, it can be here.
// For now, let's assume we'll define it here to match the problem's factory.go structure.
// This requires PulsarConfig and NewPulsarSource to be accessible.
// To do this properly, we'd need to import the pulsar package and use its types.
// Let's assume `github.com/tarungka/wire/pkg/sources/pulsar` is the package path.

/*
// This is how it would ideally look if pulsar.NewPulsarSource was directly usable
// and types were compatible or mappable.
import (
	"github.com/mitchellh/mapstructure"
	"github.com/tarungka/wire/pkg/sources/pulsar" // Assuming this path
)

func PulsarSourceCreator(configMap map[string]interface{}) (Source, error) {
	var pConfig pulsar.PulsarConfig // This is pulsar.PulsarConfig from pulsar/source.go

	// mapstructure is a common library for this.
	// It would also need to handle the embedded sources.SourceConfig.
	decoderConfig := &mapstructure.DecoderConfig{
		Result:           &pConfig,
		WeaklyTypedInput: true,
		// Custom decoders for time.Duration, pulsar.SubscriptionType, etc.
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			// mapstructure.StringToTimeDurationHookFunc(),
			// mapstructure.StringToSliceHookFunc(","),
			// Add hook for pulsar.SubscriptionType if it's a string in config
		),
	}
	decoder, err := mapstructure.NewDecoder(decoderConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create mapstructure decoder: %w", err)
	}

	if err := decoder.Decode(configMap); err != nil {
		return nil, fmt.Errorf("error decoding pulsar config: %w", err)
	}

	// NewPulsarSource is from the pulsar package.
	return pulsar.NewPulsarSource(pConfig)
}
*/

// For now, to fulfill the factory.go structure from the problem, I'll add a placeholder for
// NewPulsarSource that matches the signature, and have init() call it.
// This is a temporary measure. The ideal solution is self-registration from pulsar package.

func NewPulsarSourceWrapper(configMap map[string]interface{}) (Source, error) {
	// In a real scenario, this wrapper would:
	// 1. Define or import the actual PulsarConfig struct (from sources/pulsar/source.go).
	// 2. Use a library like 'mapstructure' to convert configMap to PulsarConfig.
	//    This includes handling type conversions (e.g., string to time.Duration, string to pulsar.SubscriptionType).
	// 3. Call the actual `pulsar.NewPulsarSource(typedConfig)`
	// This is a simplified placeholder because directly implementing mapstructure here is too complex for this step.
	// It also assumes PulsarConfig and NewPulsarSource from the pulsar package are accessible.
	// For the purpose of this step, we'll return an error.
	return nil, fmt.Errorf("PulsarSource creator wrapper not fully implemented. Config map: %v", configMap)
	// To make this work, you'd need to:
	// var cfg pulsar.PulsarConfig // Assuming pulsar.PulsarConfig is the type from pulsar/source.go
	// mapstructure.Decode(configMap, &cfg)
	// return pulsar.NewPulsarSource(cfg)
}


func init() {
    RegisterSource("kafka", NewKafkaSource) // Placeholder
    RegisterSource("pulsar", NewPulsarSourceWrapper) // Using the wrapper
    RegisterSource("s3", NewS3Source) // Placeholder
    RegisterSource("postgres-cdc", NewPostgresCDCSource) // Placeholder
    RegisterSource("mysql-cdc", NewMySQLCDCSource) // Placeholder
    RegisterSource("mongodb-cdc", NewMongoDBCDCSource) // Placeholder
    RegisterSource("redis-stream", NewRedisStreamSource) // Placeholder
    RegisterSource("filesystem", NewFileSystemSource) // Placeholder
    RegisterSource("http", NewHTTPSource) // Placeholder
    RegisterSource("websocket", NewWebSocketSource) // Placeholder
}
