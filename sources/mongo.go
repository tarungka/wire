package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ChangeStreamOperation struct {
	ResumeToken       map[string]interface{} `bson:"_id" json:"_id"`
	ResumeTokenString string                 `bson:"_id._data" json:"_id._data"`
	DocumentId        map[string]interface{} `bson:"documentKey" json:"documentKey"`
	DocumentIdString  string                 `bson:"documentKey._id" json:"documentKey._id"`
	OperationType     string                 `bson:"operationType" json:"operationType"`
	FullDocument      map[string]interface{} `bson:"fullDocument" json:"fullDocument"`
	WallTime          time.Time              `bson:"wallTime" json:"wallTime"`
	Ns                struct {
		Db   string `bson:"db" json:"db"`
		Coll string `bson:"coll" json:"coll"`
	} `bson:"ns" json:"ns"`
	UpdateDescription struct {
		UpdatedFields   map[string]interface{} `bson:"updatedFields" json:"updatedFields"`
		RemovedFields   []interface{}          `bson:"removedFields" json:"removedFields"`
		TruncatedArrays []interface{}          `bson:"truncatedArrays" json:"truncatedArrays"`
	} `bson:"updateDescription" json:"updateDescription"`
	ClusterTime primitive.Timestamp `json:"clusterTime" bson:"clusterTime"`
}

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

	// TODO: I might need to make this an interface, lets see
	// TODO: IMP: Apparently this is bad practice to have this as
	// a member variable, move this over to the function using it, i.e Read
	// Contraire
	// What if I want to merge the data streams from different places on this
	// channel? Are there better approaches?
	// Also initiate the close of all downstream goroutines by closing this
	// when shutting down a pipeline
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
	// This is to get the entire document along with the changes in the payload
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	stream, err := m.collection.Watch(m.objectContext, mongo.Pipeline{}, opts)
	if err != nil {
		log.Error().Err(err).Msg("Error when watching for changes on the mongodb collection")
	}
	// TODO: I think the scope of stream variable needs to be local, not global
	m.mongoStream = stream

	// TODO: review this later, do i need a go function like this?
	go func() {

		// TODO: According to best practices this is the place to keep
		// the channel

		for stream.Next(m.objectContext) {

			log.Debug().Msg("Got a new event")

			// var changeDoc bson.M
			var changeDoc ChangeStreamOperation
			if err := stream.Decode(&changeDoc); err != nil {
				log.Err(err).Msg("Error decoding change document")
				continue
			}

			// This is the raw indented output of the change stream pipeline result
			// output, err := json.MarshalIndent(changeDoc, "", "    ")
			// if err != nil {
			// 	panic(err)
			// }
			// fmt.Printf("%s\n", output)

			log.Trace().Msgf("The change type is: %s", changeDoc.DocumentIdString)

			// Convert changeDoc to JSON
			jsonData, err := json.Marshal(changeDoc.FullDocument)
			if err != nil {
				log.Err(err).Msg("Error marshalling change document to JSON")
				continue
			}

			log.Trace().Str("jsonData", string(jsonData)).Msgf("The json data being sent over the channel is: %s", jsonData)

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
