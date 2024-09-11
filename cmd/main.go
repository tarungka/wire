package main

// https://www.mongodb.com/community/forums/t/change-stream-projection-getting-last-resume-token/2817/3
// Be careful when the database is sharded
// If the deployment is a sharded cluster, a shard removal may cause an open change stream cursor to close, and the closed change stream cursor may not be fully resumable.

// How can I make this service distributed?
/*
CHATGPT:
There are a few different ways you could ensure that only one replica of your service is streaming data from Mongo
and updating Elasticsearch at a time when you scale up the replicas in Kubernetes:

1. Use a leader election mechanism: This would involve having each replica of your service compete
   to become the leader, and only the leader would be responsible for streaming data from Mongo and
   updating Elasticsearch. This can be done using Kubernetes primitives like ConfigMaps or using a
   separate leader election library like ZooKeeper.

2. Use a shared lock: This would involve having each replica of your service acquire a lock
   (e.g. using a distributed key-value store like etcd) before streaming data from Mongo and
   updating Elasticsearch. Only one replica would be able to acquire the lock at a time,
   ensuring that only one replica is streaming data at a time.

3. Use a shared queue: One replica can stream data and put it in a shared queue. Other replicas
   can dequeue it and update elasticsearch.

4. Use a shared state: You can store a shared state in a shared kubernetes configMap, that can be
   updated by any replica and read by all replicas. If the shared state indicates that streaming
   is already in progress, a new replica can wait for the streaming to complete before starting to
   stream data.

5. Use Kubernetes Pod Anti-Affinity: This would configure the Kubernetes scheduler to avoid scheduling
   multiple replicas of your service on the same node. This would ensure that only one replica would be
   running on each node, and therefore only one replica would be streaming data from Mongo and updating
   Elasticsearch.
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"gopkg.in/yaml.v3"

	// This is for thread safe queue
	"github.com/enriquebris/goconcurrentqueue"
	// This is for elastic search go driver
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

type ServiceConfig struct {
	// MongoDb
	MongoDbUri string
	MongoDbDb  string
	MongoDbCol string
	Project    bson.D
	CsProject  bson.D
	Filter     bson.D
	Sort       bson.D
	// ElasticSearch
	EsCloudId     string
	EsApiKey      string
	EsIndexName   string
	InstanceName  string
	TruncateIndex bool
	DataMapping   map[string]string
}

type ChangeStreamOperation struct {
	DocumentId    primitive.ObjectID `bson:"_id"`
	OperationType string
	FullDocument  map[string]interface{}
	Ns            struct {
		db   string
		coll string
	}
	UpdateDescription struct {
		updatedFields map[string]interface{}
		removedFields map[string]interface{}
	}
	ResumeToken string
	// Not supporting this as of now, will see in future releases
	// clusterTime  time.Time
}

var (
	buildString = "unknown"
	ko          = koanf.New(".")
)

// TODO: What are the downsides to storing this globally in go?
var CurrentResumeToken string

var _change_stream_lock bool = false

func acquireLock() {
	log.Info().Msg("Acquiring lock")
	_change_stream_lock = true
}

func releaseLock() {
	log.Info().Msg("Releasing lock")
	_change_stream_lock = false
}

func isLocked() bool {
	return _change_stream_lock
}

// Truncate an es index
func truncateIndex(esIndexName string, es *elasticsearch.Client) {
	req := esapi.DeleteByQueryRequest{
		Index: []string{esIndexName},
		Body:  bytes.NewReader([]byte(`{"query": {"match_all": {}}}`)),
	}

	log.Info().Msg("Truncating the index!")
	res, err := req.Do(context.Background(), es)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
}

func insertInIndex(req esapi.IndexRequest, es *elasticsearch.Client, docId string) bool {
	do_res, err := req.Do(context.Background(), es)
	if err != nil {
		log.Fatal().Msgf("Error getting response: %s", err)
	}
	defer do_res.Body.Close()

	if do_res.IsError() {
		log.Printf("[%s] Error indexing document ID=%s", do_res.Status(), docId)
		return false
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(do_res.Body).Decode(&r); err != nil {
			log.Printf("Error parsing the response body: %s", err)

		} else {
			// Print the response status and indexed document version.
			log.Printf("[%s] %s; version=%d", do_res.Status(), r["result"], int(r["_version"].(float64)))
		}
		return true
	}
}

func updateInIndex(req esapi.UpdateRequest, es *elasticsearch.Client, docId string) bool {
	do_res, err := req.Do(context.Background(), es)
	if err != nil {
		log.Fatal().Msgf("Error getting response: %s", err)
	}
	// TODO: understand what defer actually does
	// Do not use defer in a loop, does not release the resources at the earliest
	// I think I need to defer at the end of the loop
	defer do_res.Body.Close()

	if do_res.IsError() {
		log.Info().Msg(do_res.String())
		log.Info().Msgf("%s", do_res.Body)
		log.Info().Bool("Has warning?", do_res.HasWarnings()).Send()
		log.Info().Msgf("%s", do_res.Warnings())
		log.Printf("[%s] Error indexing document ID=%s", do_res.Status(), docId)
		log.Info().Msgf("%s", do_res.Header)
		return false
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(do_res.Body).Decode(&r); err != nil {
			log.Printf("Error parsing the response body: %s", err)
		} else {
			// Print the response status and indexed document version.
			log.Printf("[%s] %s; version=%d", do_res.Status(), r["result"], int(r["_version"].(float64)))
		}
		return true
	}
}

// Function to read data from mongodb
func loadInitialData(coll *mongo.Collection, es *elasticsearch.Client, serviceConfig ServiceConfig) {

	// TODO: Need to write a failover for this part of the code
	// how do I handle this for bulk of data?
	// Assuming right now this part of the code runs without exceptions

	// Check if the file named initial_data_loaded.lock exists
	if _, err := os.Stat("initial_data_loaded.lock"); err == nil {
		log.Info().Msg("Initial data already loaded")
		releaseLock()
	} else if errors.Is(err, os.ErrNotExist) {

		// Reacquiring the lock just in case
		// Acquire lock
		acquireLock()

		if serviceConfig.TruncateIndex {
			// Writing this in a func to release mem when operation is done
			truncateIndex(serviceConfig.EsIndexName, es)
		}

		var cursor *mongo.Cursor
		var results []bson.M
		// filter := bson.D{
		// 	{"isDeleted", false},
		// }
		// sort := bson.D{
		// 	{"createdAt", 1},
		// 	// {"updatedAt", 1},
		// }
		opts := options.Find().SetSort(serviceConfig.Sort).SetProjection(serviceConfig.Project)
		// Do I need to check if createAt exists?
		cursor, find_err := coll.Find(context.TODO(), serviceConfig.Filter, opts)
		if find_err == mongo.ErrNoDocuments {
			fmt.Print("No documents found")
			// Debate on which is better writing this line twice or defer wg.Done()?
			wg.Done()
			return
		}
		if find_err != nil {
			panic(find_err)
		}

		if err := cursor.All(context.TODO(), &results); err != nil {
			panic(err)
		}
		log.Info().Msgf("The length of initial data is: %d", len(results))

		// TODO: make this async,taking too long to upload initial data
		for _, result := range results {
			// Make each request a goroutine : https://pkg.go.dev/github.com/elastic/go-elasticsearch#section-readme
			res, _ := json.Marshal(result)

			req := esapi.IndexRequest{
				Index: serviceConfig.EsIndexName,
				// IMP: The Id of the document is used as unique identifier
				DocumentID: result["id"].(primitive.ObjectID).Hex(),
				Body:       bytes.NewReader(res),
				Refresh:    "true",
			}

			insertInIndex(req, es, string(result["id"].(primitive.ObjectID).Hex()))

		}

		// Update a persistent variable to indicate that the initial data has been loaded
		// TODO: Need to update like a redis or a etcd or something
		// Writing this to a file for now, wont work when running in a container
		// Create a file by the name initial_data_loaded.lock
		_, err := os.Create("initial_data_loaded.lock")
		if err != nil {
			log.Err(err).Send()
		}

		// Release lock
		releaseLock()

		log.Print("Uploaded the initial data!\n")
	}
	wg.Done()
}

// WARN: If ever this function is parallelized, then the lock needs to be handled properly
// Updation of the resume token needs to be handled - https://docs.mongodb.com/manual/changeStreams/#change-stream-resume-token
// Need to determine which event is latest when updating the resume token when parallelized - interesting problem
func uploadToElasticSearch(q *goconcurrentqueue.FIFO, es *elasticsearch.Client, serviceConfig ServiceConfig) {
	for {
		if isLocked() {
			continue
		}
		item, err := q.Dequeue()
		if err != nil {
			continue
		}
		cs_op, ok := item.(ChangeStreamOperation)
		if !ok {
			// This is a debug statement
			log.Info().Msg("Not a ChangeStreamOperation")
			log.Print("Is of data type:")
			log.Info().Type("Type:", reflect.TypeOf(cs_op)).Send()
			continue
		}

		// Make each request a goroutine : https://pkg.go.dev/github.com/elastic/go-elasticsearch#section-readme
		// For an insert operation the following in returned
		// Keys: ['_id', 'operationType', 'clusterTime', 'fullDocument', 'ns', 'documentKey']
		if cs_op.OperationType == "insert" {
			log.Info().Msg("Inserting the data in elastic search")
			res, _ := json.Marshal(cs_op.FullDocument)

			req := esapi.IndexRequest{
				Index:      serviceConfig.EsIndexName,
				DocumentID: cs_op.DocumentId.Hex(),
				Body:       bytes.NewReader(res),
				Refresh:    "true",
			}

			insertInIndex(req, es, string(cs_op.DocumentId.Hex()))
			// do_res, err := req.Do(context.Background(), es)
			// if err != nil {
			// 	log.Fatal().Msg("Error getting response: %s", err)
			// }

			// // BUG: defer in infinite loop, need to change the approach
			// // Just put this in a function and call it
			// // Does'nt really do anything here
			// defer do_res.Body.Close()

			// if do_res.IsError() {
			// 	log.Printf("[%s] Error indexing document ID=%s", do_res.Status(), string(cs_op.DocumentId.Hex()))
			// } else {
			// 	// Deserialize the response into a map.
			// 	var r map[string]interface{}
			// 	if err := json.NewDecoder(do_res.Body).Decode(&r); err != nil {
			// 		log.Printf("Error parsing the response body: %s", err)
			// 	} else {
			// 		// Print the response status and indexed document version.
			// 		log.Printf("[%s] %s; version=%d", do_res.Status(), r["result"], int(r["_version"].(float64)))
			// 	}
			// }

			CurrentResumeToken = cs_op.ResumeToken
		}
		// For an update operation the following in returned
		// Keys: ['_id', 'operationType', 'clusterTime', 'ns', 'documentKey', 'updateDescription']
		// Update the data in elastic search
		// Can raise an not found exception if the document is not found in elastic search
		// elasticsearch.NotFoundError -> catch this
		if cs_op.OperationType == "update" {
			log.Info().Msg("Updating the data in elastic search")
			// Update the data in elastic search
			log.Info().Interface("Interface:", cs_op.FullDocument).Send()
			res, s := json.Marshal(cs_op.FullDocument)
			// u := User{FirstName: "test", LastName: "test", Description: "test", Title: "test", _id: cs_op.DocumentId.Hex(), IsActive: true, IsDeleted: false}
			// res, s := json.Marshal(u)
			if s != nil {
				log.Info().Msg("Error in marshalling the data")
				log.Err(s).Send()
				continue
			}
			// log.Info().Msg(cs_op.DocumentId.Hex())
			// log.Info().Msg(reflect.TypeOf(res))
			// This step is vv imp - took me a day to figure this out
			res = []byte(`{"doc":` + string(res) + `}`)
			// log.Info().Msg(string(res))

			req := esapi.UpdateRequest{
				Index:      serviceConfig.EsIndexName,
				DocumentID: cs_op.DocumentId.Hex(),
				Body:       bytes.NewReader(res),
				Refresh:    "true",
				// ErrorTrace: true,
			}

			// BUG: Why this here?
			log.Info().Msgf("%s", req.Header)

			updateInIndex(req, es, string(cs_op.DocumentId.Hex()))

			CurrentResumeToken = cs_op.ResumeToken

		}

		// For a delete operation the following in returned
		// Keys: ['_id', 'operationType', 'clusterTime', 'ns', 'documentKey']
		// Delete the data from elastic search
		if cs_op.OperationType == "delete" {
			log.Fatal().Msg("Deleting the data from elastic search - FEATURE NOT IMPLEMENTED YET")
		}
	}
}

func watchChanges(coll *mongo.Collection, es *elasticsearch.Client, serviceConfig ServiceConfig) {

	queue := goconcurrentqueue.NewFIFO()

	pipeline := mongo.Pipeline{
		bson.D{{"$match",
			bson.D{{"operationType",
				bson.D{{"$in",
					bson.A{"insert", "update", "delete"},
				},
				},
			},
			},
		},
		}, bson.D{{"$project",
			serviceConfig.CsProject,
		},
		},
	}

	// Add this to the pipeline
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	cs, err := coll.Watch(context.TODO(), pipeline, opts)
	if err != nil {
		panic(err)
	}

	defer cs.Close(context.TODO())

	go uploadToElasticSearch(queue, es, serviceConfig)

	log.Info().Msg("Waiting For Change Events")
	for cs.Next(context.TODO()) {
		var event bson.M
		if err := cs.Decode(&event); err != nil {
			panic(err)
		}
		output, err := json.MarshalIndent(event, "", "    ")
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s\n", output)

		var each_request ChangeStreamOperation
		// var u User
		var ok bool

		// Kyes: ['_id', 'operationType', 'clusterTime', 'ns', 'documentKey', 'fullDocument']
		// Keys: ['_id', 'operationType', 'clusterTime', 'ns', 'documentKey', 'updateDescription']
		// Keys: ['_id', 'operationType', 'clusterTime', 'ns', 'documentKey']

		each_request.OperationType, ok = event["operationType"].(string)
		if !ok {
			log.Info().Msg("Error in operationType")
			continue
		}
		// each_request.clusterTime, ok = event["clusterTime"].(primitive.Timestamp)
		each_request.Ns.db, ok = event["ns"].(primitive.M)["db"].(string)
		if !ok {
			log.Info().Msg("Error no db in ns")
			continue
		}
		each_request.Ns.coll, ok = event["ns"].(primitive.M)["coll"].(string)
		if !ok {
			log.Info().Msg("Error no coll in ns")
			continue
		}
		each_request.DocumentId, ok = event["documentKey"].(primitive.M)["_id"].(primitive.ObjectID) // Am I typecasting here?
		if !ok {
			log.Fatal().Msg("Error while type asserting!")
			continue
		}

		if event["operationType"] == "insert" {
			each_request.FullDocument = event["fullDocument"].(primitive.M)
		}
		if event["operationType"] == "update" {
			each_request.FullDocument = event["fullDocument"].(primitive.M)
		}
		// TODO: Will handle this case later as it is currently not a feature in the database
		if event["operationType"] == "delete" {
			log.Print("Not handling delete events yet")
			continue
		}

		// Add to queue
		queue.Enqueue(each_request)
	}
	if err := cs.Err(); err != nil {
		panic(err)
	}

	wg.Done()
}

func loadConfig(content_type string) *map[string]interface{} {
	// cfg := make(map[interface{}]interface{})
	// ChatGPT says this, what does it mean?
	// In the loadConfig function, the cfg variable is declared as a
	// map[string]interface{}, but is not initialized. This means that
	// it will be nil when passed to yaml.Unmarshal, causing a runtime
	// qpanic. You should initialize the map before passing it to yaml.
	// Unmarshal, for example by using cfg := make(map[string]interface{}).

	// Also need to understand the scope of cfg
	cfg := make(map[string]interface{})
	// var cfg map[string]interface{}
	cfgFile, err := os.Open(".config/config.yaml")
	if err != nil {
		log.Err(err).Send()
	}
	defer cfgFile.Close()
	byteValue, _ := io.ReadAll(cfgFile)
	// log.Info().Msg(string(byteValue))
	// yaml.Unmarshal(byteValue, &cfg)
	err = yaml.Unmarshal(byteValue, &cfg)
	if err != nil {
		log.Err(err).Send()
	}
	// log.Info().Msg(cfg[content_type])
	// log.Info().Msg(reflect.TypeOf(cfg[content_type]))
	// log.Info().Msg(len(cfg[content_type].(map[string]interface{})))
	x := cfg[content_type].(map[string]interface{})

	return &x
	// cfg = cfg[content_type].([]interface{})
	// return &cfg
}

var wg sync.WaitGroup

func main() {

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	initFlags(ko)

	if ko.Bool("version") {
		fmt.Println(buildString)
		os.Exit(0)
	} else {
		log.Info().Str("build:", buildString).Msgf("Build Version: %s", buildString)
	}

	log.Info().Msg("Starting the application")

	if initError := initConfig(ko); initError != nil {
		log.Err(initError).Msg("Error when initializing the config!")
	}

	var allSourcesConfig []sources.SourceConfig
	var allSinksConfig []sinks.SinkConfig
	// var config Config

	// ko.Unmarshal("", &config)
	// fmt.Println(config)

	ko.Unmarshal("sources", &allSourcesConfig)
	ko.Unmarshal("sinks", &allSinksConfig)
	// fmt.Println("--------")
	// fmt.Println(allSourcesConfig)
	// fmt.Println(allSinksConfig)
	// For simplicity I will make the number of sources === sinks
	// if !(len(allSinksConfig) == len(allSinksConfig)) {
	// 	log.Panic().Msg("Number of sources and sinks do not match!")
	// }
	// fmt.Println("--------")

	var allSourceInterfaces []DataSource
	var allSinkInterfaces []DataSink

	for _, sourceConfig := range allSourcesConfig {
		// fmt.Println(sourceConfig)
		eachSourceInterface, err := dataSourceFactory(sourceConfig)
		if err != nil {
			log.Err(err).Send()
		}
		allSourceInterfaces = append(allSourceInterfaces, eachSourceInterface)
	}

	for _, sinkConfig := range allSinksConfig {
		// fmt.Println(sinkConfig)
		eachSinkInterface, err := dataSinkFactory(sinkConfig)
		if err != nil {
			log.Err(err).Send()
		}
		allSinkInterfaces = append(allSinkInterfaces, eachSinkInterface)
	}

	allSourcesAndSinks, err := createSourcesAndSinksConfigs(allSourceInterfaces, allSinkInterfaces)
	if err != nil {
		log.Panic().Err(err).Msg("Internal server error!")
	}

	dataPipelines, err := allSourcesAndSinks.getPipelineConfigs()
	if err != nil {
		log.Panic().Err(err).Send()
	}

	for index, pipeline := range dataPipelines {
		newPipeline := newDataPipeline(pipeline.source, pipeline.sink)
		pipelineString, err := newPipeline.Show()
		if err != nil {
			log.Err(err).Send()
		}
		log.Debug().Msgf("%d. Creating pipeline: %s", index, pipelineString)

		// newPipeline.Run()
	}

	// pipeline.Run()

	// defer source.Close()
	// defer sink.Close()

	os.Exit(0)
	return

	content_service_name := os.Getenv("CONTENT_SERVICE_NAME")
	if content_service_name == "" {
		log.Fatal().Msg("You must set your 'CONTENT_SERVICE_NAME' environmental variable.")
	}
	// I'm returning a pointer to a map
	streamCfg := *loadConfig(content_service_name)
	log.Info().Interface("Stream config:", streamCfg).Send()
	// streamCfg := streamCfg.(map[string]interface{}) // Find a better hack for this
	dbName := streamCfg["database"].(string)
	collName := streamCfg["collection"].(string)
	truncateIndex := streamCfg["truncateIndex"].(bool)
	log.Info().Msgf("Connecting to database: %s and collection: %s", dbName, collName)
	instanceName := streamCfg["instanceName"].(string)
	// CHECK THIS LATER
	// Why can I not convert this to map[string]string? I think it should be map[string]map[string]string
	dataMapper := streamCfg["data"].(map[string]interface{})
	// Data mapping hold the mapping of the data from the database to elastic search
	dataMapping := make(map[string]string)
	for k, v := range dataMapper {
		dataMapping[k] = v.(string)
	}

	// Code to print the keys
	// keys := make([]interface{}, 0, len(somethingCfg))
	// for k := range somethingCfg {
	// 	keys = append(keys, k)
	// }
	// log.Info().Msg(keys)

	index_name := os.Getenv("INDEX_NAME")
	if index_name == "" {
		log.Fatal().Msg("You must set your 'INDEX_NAME' environmental variable.")
	}
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		log.Fatal().Msg("You must set your 'MONGODB_URI' environmental variable. See\n\t https://www.mongodb.com/docs/drivers/go/current/usage-examples/#environment-variable")
	}
	es_cloud_id := os.Getenv("ES_CLOUD_ID")
	if es_cloud_id == "" {
		log.Fatal().Msg("You must set your 'ES_CLOUD_ID' environmental variable.")
	}
	es_api_key := os.Getenv("ES_API_KEY")
	if es_api_key == "" {
		log.Fatal().Msg("You must set your 'ES_API_KEY' environmental variable.")
	}

	mongoProject := bson.D{}
	mongoCsProject := bson.D{}
	for key, value := range dataMapping {
		mongoProject = append(mongoProject, bson.E{value, "$" + key})
		mongoCsProject = append(mongoCsProject, bson.E{"fullDocument." + value, "$fullDocument." + key})
	}

	// Exclude _id
	mongoProject = append(mongoProject, bson.E{"_id", 0})

	// Include the following parameters
	mongoCsProject = append(mongoCsProject, bson.E{"documentKey", 1})
	mongoCsProject = append(mongoCsProject, bson.E{"ns", 1})
	mongoCsProject = append(mongoCsProject, bson.E{"operationType", 1})
	mongoCsProject = append(mongoCsProject, bson.E{"updateDescription", 1})

	mongoFilter := bson.D{}
	if streamCfg["filter"] != nil {
		filter := streamCfg["filter"].(map[string]interface{})
		for key, value := range filter {
			mongoFilter = append(mongoFilter, bson.E{key, value})
		}
	}

	mongoSort := bson.D{}
	if streamCfg["sort"] != nil {
		sort := streamCfg["sort"].(map[string]interface{})
		for key, value := range sort {
			mongoSort = append(mongoSort, bson.E{key, value})
		}
	}

	log.Info().Msg("))))3")
	serverCfg := ServiceConfig{
		MongoDbUri:    uri,
		MongoDbDb:     dbName,
		MongoDbCol:    collName,
		EsCloudId:     es_cloud_id,
		EsApiKey:      es_api_key,
		EsIndexName:   index_name,
		InstanceName:  instanceName,
		DataMapping:   dataMapping,
		Project:       mongoProject,
		CsProject:     mongoCsProject,
		Filter:        mongoFilter,
		Sort:          mongoSort,
		TruncateIndex: truncateIndex,
	}
	// log.Info().Msg("Server config:")
	// log.Info().Msg(serverCfg)
	// log.Info().Msg("****************************************")
	acquireLock()

	client, find_err := mongo.Connect(context.TODO(), options.Client().ApplyURI(uri))
	if find_err != nil {
		panic(find_err)
	}
	// How does this work?
	defer func() {
		if err := client.Disconnect(context.TODO()); err != nil {
			panic(err)
		}
	}()

	coll := client.Database(dbName).Collection(collName)

	// Connect to elastic search
	esCfg := elasticsearch.Config{
		CloudID: es_cloud_id,
		APIKey:  es_api_key,
	}
	es, es_err := elasticsearch.NewClient(esCfg)
	if es_err != nil {
		panic(es_err)
	}
	res, err := es.Info()
	if err != nil {
		panic(err)
	}
	log.Info().Msgf("%s", res)
	log.Printf("Elastic Client Version: %s", elasticsearch.Version)
	// log.Printf("Server: %s", r["version"].(map[string]interface{})["number"])

	// TODO: Uncomment this later after understanding defer
	// defer res.Body.Close()

	wg.Add(2)

	// This below function I can change it to be run on the main thread also - can I?
	go watchChanges(coll, es, serverCfg)
	// Should I add a delay here just make sure the changes have started streaming?
	go loadInitialData(coll, es, serverCfg)

	wg.Wait() // Technically this never happens, as the server is deployed and never really stops
}
