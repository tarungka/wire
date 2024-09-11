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
}

// func (m *MongoSource) Init(ko *koanf.Koanf) (error){
// func (m *MongoSource) Init(args map[string]interface{}) error {
// 	if pipelineConnectionKey, ok := args["key"].(string); ok {
// 		m.pipelineKey = pipelineConnectionKey
// 	}
// 	if pipelineConnectionName, ok := args["name"].(string); ok {
// 		m.pipelineName = pipelineConnectionName
// 	}
// 	if pipelineConnectionType, ok := args["type"].(string); ok {
// 		m.pipelineConnectionType = pipelineConnectionType
// 	}
// 	if mongoConfig, ok := args["config"]; ok {
// 		mongoConfigMap := mongoConfig.(map[string]interface{})
// 		if mongoUri, ok := mongoConfigMap["uri"].(string); ok {
// 			m.mongoDbUri = mongoUri
// 		}
// 		if mongoDatabase, ok := mongoConfigMap["database"].(string); ok {
// 			m.mongoDbDb = mongoDatabase
// 		}
// 		if mongoCollection, ok := mongoConfigMap["collection"].(string); ok {
// 			m.mongoDbCol = mongoCollection
// 		}
// 	}
// 	return nil
// }

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
	// mongoConfigMap := mongoConfig.(map[string]string)
	// if mongoUri, ok := mongoConfig["uri"].(string); ok {
	// 	m.mongoDbUri = mongoUri
	// }
	// if mongoDatabase, ok := mongoConfigMap["database"].(string); ok {
	// 	m.mongoDbDb = mongoDatabase
	// }
	// if mongoCollection, ok := mongoConfigMap["collection"].(string); ok {
	// 	m.mongoDbCol = mongoCollection
	// }
	return nil
}

func (m *MongoSource) Connect() error {

	log.Trace().Msg("Connecting to mongodb...")
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(m.mongoDbUri))
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

func (m *MongoSource) Key() (string, error) {
	if m.pipelineKey == "" {
		return "", fmt.Errorf("error no pipeline key is set")
	}
	return m.pipelineKey, nil
}

func (m *MongoSource) Name() (string) {
	return m.pipelineName
}

func (m *MongoSource) Close() error {
	// Close MongoDB connection
	log.Info().Msg("Closing MongoDB connection")
	return nil
}
