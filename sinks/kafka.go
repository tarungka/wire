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
func (k *KafkaSink) Write(done <-chan interface{}, dataChan <-chan []byte) error {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for docBytes := range dataChan {
		var wg sync.WaitGroup
		wg.Add(1)
		record := &kgo.Record{Value: docBytes}
		k.kafkaProducerClient.Produce(ctx, record, func(_ *kgo.Record, err error) {
			defer wg.Done()
			if err != nil {
				fmt.Printf("record had a produce error: %v\n", err)
			}
		})
		wg.Wait()
	}

	return nil
}

func (k *KafkaSink) Disconnect() error {
	log.Trace().Msg("Disconnecting kafka sink")
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
