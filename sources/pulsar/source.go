package pulsar

import (
	"context"
	"fmt"
	"time"

	"github.com/apache/pulsar-client-go/pulsar"
	"github.com/tarungka/wire/pkg/sources"
)

// PulsarOffset represents the Pulsar message ID for offset tracking
type PulsarOffset struct {
	MessageID pulsar.MessageID
}

// Serialize converts the PulsarOffset to a byte slice.
// For Pulsar, MessageID has a Serialize method.
func (po *PulsarOffset) Serialize() ([]byte, error) {
	return po.MessageID.Serialize(), nil
}

// Compare compares this PulsarOffset with another.
// This is a simplified comparison; Pulsar MessageIDs are not strictly comparable directly
// for ordering in all scenarios (e.g., across different partitions if not using a global ordering).
// This implementation assumes comparison within the same stream/partition context.
func (po *PulsarOffset) Compare(other sources.Offset) int {
	otherPulsarOffset, ok := other.(*PulsarOffset)
	if !ok {
		// Or handle error appropriately
		return 0 // Consider them incomparable or one greater/lesser by default
	}

	// Pulsar MessageIDs are not directly comparable with <, >.
	// A common way is to compare their ledgerId, entryId, and partition.
	// This is a simplified placeholder. For a robust comparison,
	// you might need to delve into the specifics of how Pulsar orders messages.
	// If `other` is "greater", it means it appeared after `po`.
	// Serialized versions can be compared byte-wise for equality.
	// For ordering, it's more complex.
	// For now, we'll assume they are equal if their serialized forms are equal.
	// A proper implementation would require a deeper understanding of Pulsar's MessageID structure.

	thisSerialized, _ := po.Serialize()
	otherSerialized, _ := otherPulsarOffset.Serialize()

	return compareBytes(thisSerialized, otherSerialized)
}

// compareBytes is a helper to compare two byte slices.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareBytes(a, b []byte) int {
	lenA := len(a)
	lenB := len(b)
	minLen := lenA
	if lenB < minLen {
		minLen = lenB
	}
	for i := 0; i < minLen; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if lenA < lenB {
		return -1
	}
	if lenA > lenB {
		return 1
	}
	return 0
}


type PulsarSource struct {
	config   PulsarConfig
	client   pulsar.Client
	consumer pulsar.Consumer

	metrics  *sources.SourceMetrics
	schema   *sources.Schema // Placeholder for schema
}

type PulsarConfig struct {
	sources.SourceConfig

	// Pulsar specific
	ServiceURL       string
	Topics           []string
	Subscription     string
	SubscriptionType pulsar.SubscriptionType

	// Authentication
	AuthType   string // "token", "oauth2", "tls"
	AuthParams map[string]string

	// Consumer options
	AckTimeout        time.Duration
	NackDelay         time.Duration
	ReceiverQueueSize int

	// Schema
	SchemaType       string // "avro", "json", "protobuf"
	SchemaDefinition string
}

func NewPulsarSource(config PulsarConfig) (*PulsarSource, error) {
	// Initialize metrics with a specific type, e.g., "pulsar"
	// The NewSourceMetrics function would ideally allow specifying the source type
	// for labeling metrics correctly.
	metrics := sources.NewSourceMetrics("pulsar")

	return &PulsarSource{
		config:  config,
		metrics: metrics,
	}, nil
}

func (s *PulsarSource) Open(ctx context.Context) error {
	// Create client options
	clientOpts := pulsar.ClientOptions{
		URL:               s.config.ServiceURL,
		OperationTimeout:  30 * time.Second, // Example default
		ConnectionTimeout: 30 * time.Second, // Example default
	}

	// Configure authentication
	if err := s.configureAuth(&clientOpts); err != nil {
		return fmt.Errorf("configure auth: %w", err)
	}

	// Create client
	client, err := pulsar.NewClient(clientOpts)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	s.client = client

	// Create consumer
	consumerOpts := pulsar.ConsumerOptions{
		Topics:              s.config.Topics,
		SubscriptionName:    s.config.Subscription,
		Type:                s.config.SubscriptionType,
		AckTimeout:          s.config.AckTimeout,
		NackRedeliveryDelay: s.config.NackDelay,
		ReceiverQueueSize:   s.config.ReceiverQueueSize,
	}

	// Configure schema if specified
	if s.config.SchemaType != "" {
		schema, err := s.createSchema() // This method needs to be implemented
		if err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		consumerOpts.Schema = schema
		// s.schema will also be set if schema discovery is part of this
	}

	consumer, err := client.Subscribe(consumerOpts)
	if err != nil {
		s.client.Close() // Clean up client if consumer creation fails
		return fmt.Errorf("create consumer: %w", err)
	}
	s.consumer = consumer

	s.metrics.ConnectionsActive.Inc()
	return nil
}

func (s *PulsarSource) Read(ctx context.Context) (*sources.Record, error) {
	msg, err := s.consumer.Receive(ctx)
	if err != nil {
		// Handle common Pulsar errors, e.g., context deadline exceeded, closed consumer
		if err == context.DeadlineExceeded {
			return nil, sources.ErrNoData // Or a specific error indicating timeout
		}
		s.metrics.ReadErrors.Inc()
		return nil, fmt.Errorf("receive message: %w", err)
	}

	record := &sources.Record{
		Key:       msg.Key(),
		Value:     msg.Payload(),
		Timestamp: msg.EventTime(),
		Headers:   s.convertProperties(msg.Properties()),
		Offset:    &PulsarOffset{MessageID: msg.ID()},
		// Partition: msg.TopicPartition(), // msg.TopicPartition() doesn't exist.
		// Need to get partition info differently if required.
		// The topic name itself can be obtained via msg.Topic()
		Source: msg.Topic(), // Store the topic from which the message was read
	}

	// If you need a specific partition index, it might be part of the topic name
	// or might require parsing msg.Topic() if it includes partition info.
	// For now, let's assume Partition int32 in Record is not set or set to a default.

	s.metrics.RecordsRead.Inc()
	s.metrics.BytesRead.Add(float64(len(record.Value)))

	return record, nil
}

func (s *PulsarSource) CommitOffset(offset sources.Offset) error {
	pulsarOffset, ok := offset.(*PulsarOffset)
	if !ok {
		return fmt.Errorf("invalid offset type: expected *PulsarOffset, got %T", offset)
	}

	// Ack the specific message ID
	return s.consumer.AckID(pulsarOffset.MessageID)
}

// configureAuth configures authentication for the Pulsar client.
// This is a placeholder and needs to be implemented based on supported auth types.
func (s *PulsarSource) configureAuth(opts *pulsar.ClientOptions) error {
	switch s.config.AuthType {
	case "token":
		token, ok := s.config.AuthParams["token"]
		if !ok {
			return fmt.Errorf("token not provided for token authentication")
		}
		opts.Authentication = pulsar.NewAuthenticationToken(token)
	case "oauth2":
		// Example: opts.Authentication = pulsar.NewAuthenticationOAuth2(s.config.AuthParams)
		return fmt.Errorf("oauth2 authentication not yet implemented")
	case "tls":
		// TLS configuration would involve setting TLSTrustCertsFilePath, TLSCertificateFile, TLSKeyFilePath etc.
		// Example:
		// if certPath, ok := s.config.AuthParams["tlsCertPath"]; ok {
		//	 opts.TLSCertificateFile = certPath
		// } // etc.
		return fmt.Errorf("tls authentication not yet implemented")
	case "":
		// No auth
	default:
		return fmt.Errorf("unsupported authentication type: %s", s.config.AuthType)
	}
	return nil
}

// createSchema creates a Pulsar schema based on the configuration.
// This is a placeholder and needs to be implemented.
func (s *PulsarSource) createSchema() (pulsar.Schema, error) {
	// Example for a JSON schema based on a struct
	// if s.config.SchemaType == "json" && s.config.SchemaDefinition != "" {
	//  	return pulsar.NewJSONSchema(s.config.SchemaDefinition, nil), nil
	// }
	return nil, fmt.Errorf("schema creation for type '%s' not yet implemented", s.config.SchemaType)
}

// convertProperties converts Pulsar message properties to the Record's header format.
func (s *PulsarSource) convertProperties(properties map[string]string) map[string][]byte {
	if properties == nil {
		return nil
	}
	headers := make(map[string][]byte, len(properties))
	for k, v := range properties {
		headers[k] = []byte(v)
	}
	return headers
}

// Close closes the Pulsar consumer and client.
func (s *PulsarSource) Close() error {
	if s.consumer != nil {
		s.consumer.Close()
	}
	if s.client != nil {
		s.client.Close()
	}
	if s.metrics != nil { // Defensive check
		s.metrics.ConnectionsActive.Dec()
	}
	return nil
}

// GetOffset is not fully implemented as per problem description for Pulsar.
// It might involve querying the consumer for the last received message ID or similar.
// For now, it returns a not implemented error.
func (s *PulsarSource) GetOffset() (sources.Offset, error) {
	// This would typically return the last acknowledged or received offset.
	// Pulsar's consumer doesn't directly expose this in a simple way for arbitrary queries.
	// It's usually managed via subscriptions.
	return nil, fmt.Errorf("GetOffset not implemented for PulsarSource")
}

// SeekToOffset is not fully implemented. Pulsar's Seek methods operate on MessageID.
func (s *PulsarSource) SeekToOffset(offset sources.Offset) error {
	pulsarOffset, ok := offset.(*PulsarOffset)
	if !ok {
		return fmt.Errorf("invalid offset type for SeekToOffset")
	}
	if s.consumer == nil {
		return fmt.Errorf("consumer not initialized, cannot seek")
	}
	return s.consumer.Seek(pulsarOffset.MessageID)
}

// DiscoverSchema is a placeholder.
func (s *PulsarSource) DiscoverSchema() (*sources.Schema, error) {
	// If a schema was configured and loaded in Open(), return it.
	if s.schema != nil {
		return s.schema, nil
	}
	// Otherwise, Pulsar's topic schema registry might be queried.
	return nil, fmt.Errorf("DiscoverSchema not implemented for PulsarSource")
}

// GetMetrics returns the source's metrics.
func (s *PulsarSource) GetMetrics() sources.SourceMetrics {
	if s.metrics == nil {
		// Return empty metrics if not initialized, though NewPulsarSource should do it.
		return *sources.NewSourceMetrics("pulsar_uninitialized")
	}
	return *s.metrics
}

// HealthCheck performs a health check on the Pulsar source.
// This could involve checking the connection to the Pulsar broker.
func (s *PulsarSource) HealthCheck() error {
	// A simple health check could be trying to receive with a very short timeout,
	// or checking client/consumer internal state if available.
	// For now, we assume if Open succeeded, it's healthy.
	// A more robust check might involve a ping to the service or checking consumer status.
	if s.client == nil || s.consumer == nil {
		return fmt.Errorf("pulsar source is not open or not healthy")
	}
	// Potentially, one could try a non-blocking receive or check some internal status.
	// This is a basic check.
	return nil
}
// Ensure PulsarSource implements the sources.Source interface
var _ sources.Source = (*PulsarSource)(nil)
