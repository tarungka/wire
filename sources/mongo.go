package sources

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoSource struct {
	// MongoDB connection details
	MongoDbUri string
	MongoDbDb  string
	MongoDbCol string
	Project    bson.D
	CsProject  bson.D
	Filter     bson.D
	Sort       bson.D
	client     *mongo.Client
}

func (m *MongoSource) Connect() error {

	log.Trace().Msg("Connecting to mongodb...")
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(m.MongoDbUri))
	if err != nil {
		log.Err(err).Msg("Error when connecting to mongodb database!")
		// panic(err)
		return fmt.Errorf("%s", err)
	}

	m.client = client

	// How does this work?
	// defer func() {
	// 	if err := client.Disconnect(context.TODO()); err != nil {
	// 		log.Err(err).Msg("Error when dis-connecting from mongodb database!") // panic(err)
	// 	}
	// }()

	return nil
}

func (m *MongoSource) Read() ([]byte, error) {
	// Read data from MongoDB
	log.Info().Msg("Reading from MongoDB")
	return []byte("data from MongoDB"), nil
}

func (m *MongoSource) Close() error {
	// Close MongoDB connection
	log.Info().Msg("Closing MongoDB connection")
	return nil
}
