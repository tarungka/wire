package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/twmb/franz-go/pkg/kgo"
)

type KafkaSource struct {
	pipelineKey            string
	pipelineName           string
	pipelineConnectionType string
	// MongoDB connection details
	bootstrapServers string
	consumerGroup    string // Not sure if this is even a feature
	topic            string

	kafkaConsumerClient *kgo.Client
}

func (m *KafkaSource) Init(args SourceConfig) error {
	m.pipelineKey = args.Key
	m.pipelineName = args.Name
	m.pipelineConnectionType = args.ConnectionType

	if args.Config["bootstrap_servers"] == "" || args.Config["group"] == "" || args.Config["topic"] == "" {
		log.Error().Msg("Error missing config values")
		return fmt.Errorf("error missing config values")
	} else {
		log.Debug().Str("bootstrap_servers", args.Config["bootstrap_servers"]).Str("topic", args.Config["topic"]).Str("group", args.Config["group"]).Send()
	}

	m.bootstrapServers = args.Config["bootstrap_servers"]
	m.consumerGroup = args.Config["group"]
	m.topic = args.Config["topic"]

	return nil
}

func (k *KafkaSource) Connect(ctx context.Context) error {

	log.Trace().Msg("Connecting to kafka cluster as a source...")
	opts := []kgo.Opt{
		kgo.SeedBrokers(k.bootstrapServers),
		kgo.ConsumerGroup(k.consumerGroup),
		kgo.ConsumeTopics(k.topic),
		kgo.AllowAutoTopicCreation(), // TODO: I think this needs to be a setting
		kgo.AutoCommitMarks(),
	}
	// TODO: To enable logging
	// opts = append(opts, kgo.WithLogger(kgo.BasicLogger(os.Stderr, kgo.LogLevelInfo, nil)))
	kafkaProducerClient, err := kgo.NewClient(opts...)
	if err != nil {
		log.Err(err).Msg("Error when creating a kafka consumer!")
		return err
	}
	k.kafkaConsumerClient = kafkaProducerClient

	return nil
}

func (k *KafkaSource) Read(ctx context.Context, done <-chan interface{}, wg *sync.WaitGroup) (<-chan []byte, error) {
	// This is to get the entire document along with the changes in the payload

	changeStreamChan := make(chan []byte, 5)
	defer func() {
		log.Trace().Msg("The Kafka Source Read is done!")
	}()

	wg.Add(1)
	go func(ctx context.Context, done <-chan interface{}, opStream chan<- []byte) {

		defer func () {
			log.Trace().Msg("Done Reading from the kafka source")
			wg.Done()
		}()
		// TODO: Do I need to close this here?
		defer close(opStream)

		var seen int
		for {

			// TODO: There has to be a better way to write this, looks shabby and
			// no go like

			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			default:
			}

			fetches := k.kafkaConsumerClient.PollFetches(ctx)
			if errs := fetches.Errors(); len(errs) > 0 {
				// All errors are retried internally when fetching, but non-retriable errors are
				// returned from polls so that users can notice and take action.
				log.Error().Msg(fmt.Sprint(errs))
			}
			if fetches.IsClientClosed() {
				return
			}
			fetches.EachError(func(t string, p int32, err error) {
				log.Err(err).Msgf("fetch err topic %s partition %d: %v", t, p, err)
				if errors.Is(err, context.Canceled) {
					log.Info().Msg("Context cancelled, gracefully shutting down the pipeline")
					return
				}
			})

			if fetches.Empty() {
				time.Sleep(100 * time.Millisecond) // Adding a small backoff
				continue
			}

			// fetches.RecordIter()

			fetches.EachRecord(func(record *kgo.Record) {
				seen++
				// log.Debug().Msgf("processed %d records--autocommitting now allows the **prior** poll to be available for committing, nothing can be lost!\n", seen)

				// Making an assumption that the data is in json format
				jsonByteData := record.Value

				var changeDoc map[string]interface{}
				err := json.Unmarshal(jsonByteData, &changeDoc)
				if err != nil {
					log.Err(err).Msg("Error un-marshalling MongoDB change document")
					return
				}

				log.Debug().Msgf("processed %d records-- %v\n", seen, changeDoc)
				if jsonByteData == nil {
					log.Warn().Msg("The json data is NIL!")
				}

				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case opStream <- jsonByteData:
					// Dont ever add default here, this needs to be a blocking operation
					// If I put default here there are chances of loosing data when the channel
					// is full and data is read but not pushed to the output stream, right?
					// default:
				}
			})
		}

	}(ctx, done, changeStreamChan)

	return   changeStreamChan, nil
}

func (k *KafkaSource) Key() (string, error) {
	if k.pipelineKey == "" {
		return "", fmt.Errorf("error no pipeline key is set")
	}
	return k.pipelineKey, nil
}

func (k *KafkaSource) Name() string {
	return k.pipelineName
}

func (k *KafkaSource) Disconnect() error {
	log.Trace().Msg("Disconnecting kafka sink")
	k.kafkaConsumerClient.Close()
	return nil
}

func (m *KafkaSource) Info() string {
	return fmt.Sprintf("Key:%s|Name:%s|Type:%s", m.pipelineKey, m.pipelineName, m.pipelineConnectionType)
}
