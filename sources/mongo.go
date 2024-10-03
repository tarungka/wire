package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
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

	return nil
}

func (m *MongoSource) Connect(ctx context.Context) error {

	if m.client != nil {
		return nil
	}

	log.Trace().Msg("Connecting to mongodb...")
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(m.mongoDbUri))
	if err != nil {
		log.Err(err).Msg("Error when connecting to mongodb database!")
		return fmt.Errorf("%s", err)
	}

	m.client = client
	m.getCollectionInstance()

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

// As of now this function is not optimized to handled a lot of data, do not use this
// for huge amounts of data a it holds the initial loaded data in memory
func (m *MongoSource) LoadInitialData(ctx context.Context, done <-chan interface{}, wg *sync.WaitGroup) (<-chan []byte, error) {

	initialDataStreamChan := make(chan []byte, 5)

	wg.Add(1)

	go func() {
		defer func() {
			close(initialDataStreamChan)
			wg.Done()
		}()

		log.Debug().Msg("Loading initial data from mongodb")
		cursor, err := m.collection.Find(ctx, bson.D{})
		if err != nil {
			log.Err(err).Msg("Error when loading initial data from mongodb")
		}
		defer cursor.Close(ctx)

		var results []bson.M
		if err = cursor.All(ctx, &results); err != nil {
			log.Err(err).Msg("Error when reading data from cursor")
		}

		// Print the results
		for _, result := range results {
			// fmt.Println(result)
			jsonData, err := json.Marshal(result)
			if err != nil {
				log.Err(err).Msg("Error marshalling change document to JSON")
				continue
			}

			initialDataStreamChan <- jsonData
		}

		<-done

	}()

	return initialDataStreamChan, nil
}

// func (m *MongoSource) Watch() (<-chan []byte, error) {
func (m *MongoSource) Read(ctx context.Context, done <-chan interface{}, wg *sync.WaitGroup) (<-chan []byte, error) {

	// This is to get the entire document along with the changes in the payload
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	stream, err := m.collection.Watch(ctx, mongo.Pipeline{}, opts)
	if err != nil {
		log.Error().Err(err).Msg("Error when watching for changes on the mongodb collection")
		return nil, err
	}

	// Create a channel to send the data to
	changeStreamChan := make(chan []byte, 5)

	// TODO: Misleading change the message
	// defer func() {
	// 	log.Trace().Msg("The Mongodb Source Read is done!")
	// }()

	wg.Add(1)
	// TODO: review this later, do i need a go function like this?
	go func(mongoStream *mongo.ChangeStream, opStream chan<- []byte) {
		defer func() {
			log.Trace().Msg("Done Reading from the mongodb source")
			wg.Done()
		}()

		// TODO: According to best practices this is the place to keep
		// the channel

		defer func() {
			log.Trace().Msg("Closing the mongo change stream")
			// mongoStream.Close(ctx) // Close here?
			// close(opStream) // I cannot close the read only change stream
			// and expect the downstream channels to close, i need to close
			// the original channel
			close(changeStreamChan)
		}()

		for mongoStream.Next(ctx) {

			log.Debug().Msg("Got a new event")

			streamError := mongoStream.Err()
			if streamError != nil {
				log.Err(streamError).Msg("Error in change streams")
			}

			// ctx.

			// var changeDoc bson.M
			var changeDoc ChangeStreamOperation
			if err := mongoStream.Decode(&changeDoc); err != nil {
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

			select {
			case <-done:
				log.Trace().Msg("Closing read from mongodb")
				close(changeStreamChan)
				return
			// case data, a<-jsonData:
			case opStream <- jsonData: // Send the change to the channel
			default: // Do not block as I want to read for changes, right?
			}

		}

		// This should technically never happen, unless its a SYS INT
	}(stream, changeStreamChan)

	if err := stream.Err(); err != nil {
		log.Err(err).Msg("Error in the change stream")
		return nil, err
	}

	return changeStreamChan, nil
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

func (m *MongoSource) Disconnect() error {
	// Close MongoDB connection
	log.Info().Msg("Closing MongoDB connection")
	if err := m.client.Disconnect(context.Background()); err != nil {
		log.Err(err).Msg("Error when dis-connecting from mongodb database!") // panic(err)
	}
	return nil
}

func (m *MongoSource) Info() string {
	return fmt.Sprintf("Key:%s|Name:%s|Type:%s", m.pipelineKey, m.pipelineName, m.pipelineConnectionType)
}
