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
	"fmt"
	"os"

	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tgk/wire/sinks"
	"github.com/tgk/wire/sources"
)

var (
	buildString = "unknown"
	ko          = koanf.New(".")
)

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

	ko.Unmarshal("sources", &allSourcesConfig)
	ko.Unmarshal("sinks", &allSinksConfig)

	var allSourceInterfaces []DataSource
	var allSinkInterfaces []DataSink

	for _, sourceConfig := range allSourcesConfig {
		eachSourceInterface, err := dataSourceFactory(sourceConfig)
		if err != nil {
			log.Err(err).Send()
		}
		allSourceInterfaces = append(allSourceInterfaces, eachSourceInterface)
	}

	for _, sinkConfig := range allSinksConfig {
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
		log.Debug().Msgf("%d. Creating and running pipeline: %s", index, pipelineString)

		newPipeline.Run()
	}
}
