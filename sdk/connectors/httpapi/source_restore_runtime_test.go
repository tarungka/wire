package httpapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

func TestHTTPSourceTaskRestoresBeforeOpeningIngress(t *testing.T) {
	source, err := NewSource(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	slot := engine.NewTaskSlot(engine.DefaultTaskSlotConfig(), nil, nil, nil, source)
	slot.TaskID = "source"
	slot.RestoreCheckpoint = &engine.TaskCheckpoint{TaskID: "source", CheckpointID: 7, EpochID: 2, HasSource: true, Source: binary.BigEndian.AppendUint64(nil, 42)}
	var observed uint64
	slot.OnRunning = func() {
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+source.Address()+"/ingest", strings.NewReader(`{"events":[{"key":"key","value":"value"}]}`))
		if err != nil {
			t.Error(err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var accepted struct {
			Sequence uint64 `json:"sequence"`
		}
		if err := json.Unmarshal(body, &accepted); err != nil {
			t.Error(err)
			return
		}
		if response.StatusCode != 200 {
			t.Errorf("ingress status=%d body=%s", response.StatusCode, body)
		}
		observed = accepted.Sequence
	}
	err = slot.Run(ctx)
	if err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
	if observed != 43 {
		t.Fatalf("first accepted sequence=%d want 43", observed)
	}
}

func TestHTTPSourceInvalidRestoreNeverOpensIngress(t *testing.T) {
	for _, invalid := range []string{"topology", "offset"} {
		t.Run(invalid, func(t *testing.T) {
			source, err := NewSource(SourceConfig{Address: "127.0.0.1:0", AllowInsecure: true})
			if err != nil {
				t.Fatal(err)
			}
			slot := engine.NewTaskSlot(engine.DefaultTaskSlotConfig(), nil, nil, nil, source)
			slot.TaskID = "source"
			slot.RestoreCheckpoint = &engine.TaskCheckpoint{TaskID: "source", CheckpointID: 7, HasSource: true, Source: binary.BigEndian.AppendUint64(nil, 42)}
			if invalid == "topology" {
				slot.RestoreCheckpoint.TaskID = "other"
			} else {
				slot.RestoreCheckpoint.Source = []byte("bad")
			}
			if err := slot.Run(t.Context()); err == nil {
				t.Fatal("invalid source restore accepted")
			}
			if source.Address() != "" {
				t.Fatal("ingress opened before restore validation")
			}
		})
	}
}
