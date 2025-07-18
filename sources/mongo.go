package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
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
	open atomic.Bool // if connection is open

	// MongoDB connection details
	mongoDbUri             string
	mongoDbDb              string
	mongoDbCol             string
	pipelineKey            string
	pipelineName           string
	pipelineConnectionType string
	loadInitialData        bool
	project                bson.D
	csProject              bson.D
	filter                 bson.D
	sort                   bson.D
	client                 *mongo.Client
	collection             *mongo.Collection

	//
	logger zerolog.Logger
}

func (m *MongoSource) Init(args SourceConfig) error {
	m.logger.Printf("initializing a new mongo source with: %v", args)

	m.pipelineKey = args.Key
	m.pipelineName = args.Name
	m.pipelineConnectionType = args.ConnectionType
	loadInitialData, err := strconv.ParseBool(args.Config["load_initial_data"])
	if err != nil {
		log.Err(err).Msg("error when reading value for load_initial_data, defaulting to false")
		loadInitialData = false
	}
	m.loadInitialData = loadInitialData
	m.mongoDbUri = args.Config["uri"]
	m.mongoDbDb = args.Config["database"]
	m.mongoDbCol = args.Config["collection"]
	m.project = bson.D{}
	m.filter = bson.D{}
	m.sort = bson.D{}
	m.csProject = bson.D{}

	if m.pipelineKey == "" || m.pipelineConnectionType == "" || m.pipelineName == "" {
		m.logger.Fatal().Str("KEY", m.pipelineKey).Str("CONN", m.pipelineConnectionType).Str("NAME", m.pipelineName).Msgf("Missing information!")
	}

	if m.mongoDbUri == "" || m.mongoDbDb == "" || m.mongoDbCol == "" {
		m.logger.Fatal().Str("URI", m.mongoDbUri).Str("CONN", m.mongoDbDb).Str("NAME", m.mongoDbCol).Msgf("Missing information!")
	}

	return nil
}

func (m *MongoSource) Connect(ctx context.Context) error {

	if m.client != nil {
		log.Trace().Msg("Client already exists, not creating a new instance")
		return nil
	}

	log.Trace().Msg("Connecting to mongodb...")
	// TODO: break this down to NewClient and then Connect
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(m.mongoDbUri))
	if err != nil {
		log.Err(err).Msg("Error when connecting to mongodb database!")
		// return fmt.Errorf("%s", err)
		return err
	}

	m.client = client
	m.getCollectionInstance()

	readPref, err := readpref.New(readpref.PrimaryPreferredMode)
	if err != nil {
		m.logger.Err(err).Msgf("error when creating read preference config")
		return err
	}
	err = client.Ping(ctx, readPref)
	if err != nil {
		m.logger.Err(err).Msgf("error when trying to connect to mongodb")
		m.open.Store(false)
		return err
	}

	m.logger.Print("Setting the state of mongo store to open")
	m.open.Store(true)

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
func (m *MongoSource) LoadInitialData(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error) {
	if !m.open.Load() {
		m.logger.Printf("Cannot load initial data as there is no mongo client")
		return nil, fmt.Errorf("no mongo client")
	}

	initialDataStreamChan := make(chan *models.Job, 5)

	wg.Add(1)

	go func() {
		defer func() {
			close(initialDataStreamChan)
			wg.Done()
		}()

		if !m.loadInitialData {
			return
		}

		log.Info().Msg("Loading initial data from the source...")

		log.Debug().Msg("Loading initial data from mongodb")
		// TODO: batch upload the data
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

			jobData, err := models.New(jsonData)
			if err != nil {
				m.logger.Err(err).Msg("error when creating a new job")
			}

			initialDataStreamChan <- jobData
		}

		log.Debug().Msgf("LoadInitialData IS DONE")
		<-ctx.Done()

	}()

	return initialDataStreamChan, nil
}

// func (m *MongoSource) Watch() (<-chan []byte, error) {
func (m *MongoSource) Read(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error) {

	if !m.open.Load() {
		return nil, fmt.Errorf("no mongo client")
	}

	// This is to get the entire document along with the changes in the payload
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	stream, err := m.collection.Watch(ctx, mongo.Pipeline{}, opts)
	if err != nil {
		log.Error().Err(err).Msg("Error when watching for changes on the mongodb collection")
		return nil, err
	}

	// Create a channel to send the data to
	changeStreamChan := make(chan *models.Job, 5)

	// TODO: Misleading change the message
	// defer func() {
	// 	log.Trace().Msg("The Mongodb Source Read is done!")
	// }()

	wg.Add(1)
	// TODO: review this later, do i need a go function like this?
	go func(mongoStream *mongo.ChangeStream, opStream chan<- *models.Job) {
		defer func() {
			log.Trace().Msg("Done Reading from the mongodb source")
			wg.Done()
		}()

		// TODO: According to best practices this is the place to keep
		// the channel

		defer func() {
			log.Trace().Msg("Closing the mongo change stream")
			// Close here? Probably not as this stream is being used to watch for changes
			// mongoStream.Close(ctx) 
			close(changeStreamChan)
		}()

		for mongoStream.Next(ctx) {

			log.Debug().Msg("Got a new event")

			// BUG: This is causing a DATA RACE, I cant access stream.Err and stream.Next concurrently
			// streamError := mongoStream.Err()
			// if streamError != nil {
			// 	log.Err(streamError).Msg("Error in change streams")
			// }

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

			jobData, err := models.New(jsonData)
			if err != nil {
				m.logger.Err(err).Msg("error when creating a new job")
			}

			select {
			case <-ctx.Done():
				log.Trace().Msg("upstream context closed; closing read from mongodb")
				close(changeStreamChan)
				return
			// case data, a<-jsonData:
			case opStream <- jobData: // Send the change to the channel
			default: // Do not block as I want to read for changes
			}

		}

		// This should technically never happen, unless its a SYS INT
	}(stream, changeStreamChan)

	// BUG: This is causing a DATA RACE, I cant access stream.Err and stream.Next concurrently
	// if err := stream.Err(); err != nil {
	// 	log.Err(err).Msg("Error in the change stream")
	// 	return nil, err
	// }

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

// NewMongoSource returns a new instance of MongoSource
func NewMongoSource() *MongoSource {
	newLogger := logger.GetLogger("mongo-source")
	newLogger.Print("creating new mongo source")
	return &MongoSource{
		mongoDbUri:             "",
		mongoDbDb:              "",
		mongoDbCol:             "",
		pipelineKey:            "",
		pipelineName:           "",
		pipelineConnectionType: "",
		loadInitialData:        false,
		project:                bson.D{},
		csProject:              bson.D{},
		filter:                 bson.D{},
		sort:                   bson.D{},
		client:                 nil,
		collection:             nil,
		logger:                 newLogger,
	}
}
