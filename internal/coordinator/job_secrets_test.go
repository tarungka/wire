package coordinator

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestSubmissionRejectsMissingSecretsBeforePersistence(t *testing.T) {
	const variable = "WIRE_TEST_SUBMISSION_REQUIRED_SECRET"
	previous, exists := os.LookupEnv(variable)
	if err := os.Unsetenv(variable); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(variable, previous)
		} else {
			_ = os.Unsetenv(variable)
		}
	})
	c, store := newReadyCoordinator(t)
	for _, dlq := range []bool{false, true} {
		graph := linearGraph()
		raw := []byte(`{"token":"${WIRE_TEST_SUBMISSION_REQUIRED_SECRET}"}`)
		if dlq {
			graph.Operators[0].DLQSink = &rpc.DLQSinkDescriptor{Config: raw}
		} else {
			graph.Operators[0].Config = raw
		}
		encoded, err := protocol.EncodeMsgPack(graph)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.SubmitJob("missing", 1, encoded)
		if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), variable) {
			t.Fatalf("missing-variable rejection: %v", err)
		}
		_, err = c.SubmitJobFromSavepoint("missing", 1, encoded, "jobs/old/checkpoints/1")
		if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), variable) {
			t.Fatalf("upgrade missing-variable rejection: %v", err)
		}
	}
	c.mu.RLock()
	reserved := len(c.jobs) + len(c.activeJobNames)
	c.mu.RUnlock()
	if reserved != 0 {
		t.Fatal("failed validation reserved job")
	}
	count := 0
	if err := store.PrefixScan([]byte("jobs/"), func(_, _ []byte) bool { count++; return true }); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed validation wrote metadata")
	}
}

func TestSubmissionPersistsOnlySecretReferences(t *testing.T) {
	const secret = "unique-private-credential-never-persist"
	t.Setenv("WIRE_TEST_SUBMISSION_SECRET", secret)
	c, store := newReadyCoordinator(t)
	graph := linearGraph()
	graph.Operators[0].Config = []byte(`{"token":"${WIRE_TEST_SUBMISSION_SECRET}"}`)
	encoded, err := protocol.EncodeMsgPack(graph)
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(encoded)
	job, err := c.SubmitJob("references", 1, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, original) || !bytes.Equal(job.Config, original) {
		t.Fatal("submission replaced references")
	}
	for _, key := range [][]byte{JobMetaKey(job.ID), JobConfigKey(job.ID)} {
		data, err := store.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("resolved credential persisted")
		}
		if !bytes.Contains(data, []byte("${WIRE_TEST_SUBMISSION_SECRET}")) {
			t.Fatal("reference lost in metadata")
		}
	}
}
