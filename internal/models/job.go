package models

import (
	"time"

	uuid "github.com/google/uuid"
	"github.com/tarungka/wire/internal/logger"
)

// Job is a struct that holds all the data about the job
// and other metadata necessary
type Job struct {
	ID            uuid.UUID   // a UUID v7 to identify the job
	data          interface{} // can be anything; but is usually a JSON
	nodeCreatedAt time.Time
	nodeUpdatedAt time.Time
	eventTime     time.Time
	priority      int
}

func New(data interface{}) (*Job, error) {
	jId, err := uuid.NewV7()
	if err != nil {
		logger.AdHocLogger.Err(err).Msg("error when creating a new job")
		return nil, err
	}
	now := time.Now()
	var stringEventTime string
	var ok bool
	var eventTime time.Time
	switch t := data.(type) {
	case map[string]interface{}:
		stringEventTime, ok = t["eventTime"].(string)
		if !ok {
			break // should break out of the case
		}
		// TODO: what other time formats do I need to support?
		eventTime, err = time.Parse(time.RFC3339, stringEventTime)
		if err != nil {
			logger.AdHocLogger.Err(err).Msg("error when parsing eventTime")
			break
		}
	}
	return &Job{
		ID:   jId,
		data: data,
		// The times on each node are only consistent on the node that they
		// are created they may or may not be consistent across nodes
		// use ID for sorting/ conflict resolution with ordering the jobs
		nodeCreatedAt: now,
		nodeUpdatedAt: now,
		eventTime:     eventTime,
		priority:      0, // default to 0, will add this as a tunable feature in the future
	}, nil
}
