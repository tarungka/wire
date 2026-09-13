package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/tarungka/wire/internal/jobcli"
)

func TestJobCLIInspectsCoordinator(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	job, err := c.SubmitJob("cli-job", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := startTestHTTPServer(t, c)
	var out bytes.Buffer
	if err := jobcli.Run(context.Background(), []string{"jobs", "get", job.ID, "--coordinator", "http://" + srv.Addr()}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var result jobDetailResponse
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Name != "cli-job" || result.Status != "CREATED" {
		t.Fatalf("unexpected job %+v", result)
	}
	if err := jobcli.Run(context.Background(), []string{"jobs", "get", "missing", "--coordinator", "http://" + srv.Addr()}, io.Discard, io.Discard); err == nil {
		t.Fatal("missing job was successful")
	}
}
