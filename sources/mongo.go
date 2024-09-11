package sources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoSource struct {
	// MongoDB connection details
	mongoDbUri             string
	mongoDbDb              string
	mongoDbCol             string
	pipelineKey            string
	pipelineName           string
	pipelineConnectionType string
	project                bson.D
	csProject              bson.D
	filter                 bson.D
	sort                   bson.D
	client                 *mongo.Client
	collection             *mongo.Collection
	objectContext          context.Context
	mongoStream            *mongo.ChangeStream

	changeStreamChan chan []byte
}

func (m *MongoSource) Init(args SourceConfig) error {
	m.pipelineKey = args.Key
	m.pipelineName = args.Name
	m.pipelineConnectionType = args.ConnectionType
	m.mongoDbUri = args.Config["uri"]
	m.mongoDbDb = args.Config["database"]
	m.mongoDbCol = args.Config["collection"]
	m.project = bson.D{}
	m.filter = bson.D{}
	m.sort = bson.D{}
	m.csProject = bson.D{}

	// Create context
	m.objectContext = context.Background()

	m.changeStreamChan = make(chan []byte, 100)

	return nil
}

func (m *MongoSource) Connect() error {

	if m.client != nil {
		return nil
	}

	log.Trace().Msg("Connecting to mongodb...")
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(m.mongoDbUri))
	if err != nil {
		log.Err(err).Msg("Error when connecting to mongodb database!")
		// panic(err)
		return fmt.Errorf("%s", err)
	}

	m.client = client
	m.getCollectionInstance()

	// How does this work?
	// defer func() {
	// 	if err := client.Disconnect(context.TODO()); err != nil {
	// 		log.Err(err).Msg("Error when dis-connecting from mongodb database!") // panic(err)
	// 	}
	// }()

	return nil
}

func (m *MongoSource) getCollectionInstance() error {
	m.collection = m.client.Database(m.mongoDbDb).Collection(m.mongoDbCol)
	return nil
}

// func (m *MongoSource) Read() (<- chan []byte, error) {
// 	// Read data from MongoDB
// 	log.Info().Msg("Reading from MongoDB")
// 	return []byte("data from MongoDB"), nil
// }

// func (m *MongoSource) Watch() (<-chan []byte, error) {
func (m *MongoSource) Read() (<-chan []byte, error) {
	stream, err := m.collection.Watch(m.objectContext, mongo.Pipeline{})
	if err != nil {
		log.Error().Err(err).Msg("Error when watching for changes on the mongodb collection")
	}
	m.mongoStream = stream

	// TODO: review this later, do i need a go function like this?
	go func() {
		for stream.Next(m.objectContext) {

			log.Debug().Msg("Got a new event")

			var changeDoc bson.M
			if err := stream.Decode(&changeDoc); err != nil {
				log.Err(err).Msg("Error decoding change document")
				continue
			}

			// Convert changeDoc to JSON
			jsonData, err := json.Marshal(changeDoc)
			if err != nil {
				log.Err(err).Msg("Error marshalling change document to JSON")
				continue
			}

			m.changeStreamChan <- jsonData // Send the change to the channel
		}

		// This should technically never happen, unless its a SYS INT
		close(m.changeStreamChan)
	}()

	if err := stream.Err(); err != nil {
		log.Err(err).Msg("Error in the change stream")
	}

	return m.changeStreamChan, nil
}

func (m *MongoSource) Key() (string, error) {
	if m.pipelineKey == "" {
		return "", fmt.Errorf("error no pipeline key is set")
	}
	return m.pipelineKey, nil
}

func (m *MongoSource) Name() string {
	return m.pipelineName
}

func (m *MongoSource) Close() error {
	// Close MongoDB connection
	log.Info().Msg("Closing MongoDB connection")
	if err := m.client.Disconnect(context.TODO()); err != nil {
		log.Err(err).Msg("Error when dis-connecting from mongodb database!") // panic(err)
	}
	return nil
}

func (m *MongoSource) Info() string {
	return fmt.Sprintf("Key:%s|Name:%s|Type:%s", m.pipelineKey, m.pipelineName, m.pipelineConnectionType)
}
