package sinks

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/twmb/franz-go/pkg/kgo"
)

type KafkaSink struct {
	pipelineKey            string
	pipelineName           string
	pipelineConnectionType string
	// Kafka Producer details
	bootstrapServers string
	topic            string

	kafkaProducerClient *kgo.Client
}

func (k *KafkaSink) Init(args SinkConfig) error {
	k.pipelineKey = args.Key
	k.pipelineName = args.Name
	k.pipelineConnectionType = args.ConnectionType

	if args.Config["bootstrap_servers"] == "" || args.Config["topic"] == "" {
		log.Error().Msg("Error missing config values")
		return fmt.Errorf("error missing config values")
	} else {
		log.Debug().Str("bootstrap_servers", args.Config["bootstrap_servers"]).Str("topic", args.Config["topic"]).Send()
	}

	k.bootstrapServers = args.Config["bootstrap_servers"]
	k.topic = args.Config["topic"]

	return nil
}
func (k *KafkaSink) Connect(ctx context.Context) error {
	log.Trace().Msg("Connecting to kafka cluster as a sink...")
	opts := []kgo.Opt{
		kgo.SeedBrokers(k.bootstrapServers),
		kgo.DefaultProduceTopic(k.topic),
		kgo.AllowAutoTopicCreation(), // TODO: I think this needs to be a setting
		// kgo.AutoCommitMarks(),
	}
	kafkaProducerClient, err := kgo.NewClient(opts...)
	if err != nil {
		log.Err(err).Msg("Error when creating a kafka producer!")
		return err
	}
	k.kafkaProducerClient = kafkaProducerClient

	return nil
}

// func (k *KafkaSink) Write(done <-chan interface{}, wg *sync.WaitGroup, dataChan <-chan []byte) error {

// 	defer wg.Done()

// 	for i := 0; i < 10; i++ {
// 		time.Sleep(1 * time.Second)
// 		fmt.Printf("after %d seconds\n", i+1)
// 	}

// 	return nil
// }

func (k *KafkaSink) sendMessageToKafka(ctx context.Context, docBytes []byte) {
	var wgKafkaSend sync.WaitGroup
	wgKafkaSend.Add(1)
	record := &kgo.Record{Value: docBytes}
	k.kafkaProducerClient.Produce(ctx, record, func(record *kgo.Record, err error) {
		defer wgKafkaSend.Done()
		if err != nil {
			log.Err(err).Interface("record", record).Msg("record had a produce error")
		} else {
			log.Debug().Msgf("Successfully produced message")
			log.Trace().Msgf("Successfully produced message: %v\n", string(record.Value))
		}
	})
	wgKafkaSend.Wait()
}

// BUG: There is an error when trying to clean up/ close this channel/ function; unsure what the error is
func (k *KafkaSink) Write(done <-chan interface{}, wg *sync.WaitGroup, dataChan <-chan []byte, initialDataChan <-chan []byte) error {

	defer func() {
		wg.Done()
	}()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer func() {
			log.Debug().Msg("Write go routine is cleaning up")
			cancel()
		}()
		for {
			select {
			case docBytes, ok := <-dataChan:
				if !ok {
					// dataChan is closed, return from the function
					log.Debug().Msg("The upstream channel (dataChan) closed")
					return
				}

				log.Debug().Msg("New data on the channel")
				k.sendMessageToKafka(ctx, docBytes)
				log.Trace().Msg("After wait")
			case docBytes, ok := <-initialDataChan:
				if !ok{
					log.Info().Msg("Initial data channel closed")
				}
				log.Debug().Msg("New initial data on the channel")
				k.sendMessageToKafka(ctx, docBytes)
				log.Trace().Msg("After wait")
			// default:
				// case <-done:
				// 	// This probably should not happen, as this function should return only when
				// 	// the upstream channel is closed
				// 	log.Debug().Msg("Received done signal, terminating write operation")
				// 	return nil
				// default:
			}
		}
	}()

	// log.Debug().Msg("The upstream channel(source) closed")

	return nil
}

func (k *KafkaSink) Disconnect() error {
	log.Info().Msg("Disconnecting kafka sink")
	k.kafkaProducerClient.Close()
	return nil
}

func (k *KafkaSink) Key() (string, error) {
	if k.pipelineKey == "" {
		return "", fmt.Errorf("error no pipeline key is set")
	}
	return k.pipelineKey, nil
}

func (k *KafkaSink) Name() string { return k.pipelineName }

func (k *KafkaSink) Info() string {
	return fmt.Sprintf("Key:%s|Name:%s|Type:%s", k.pipelineKey, k.pipelineName, k.pipelineConnectionType)
}
