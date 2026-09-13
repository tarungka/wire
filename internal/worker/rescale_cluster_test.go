package worker_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/coordinator"
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/worker"
)

type rescaleStatelessSource struct{ checkpointTestSource }

func (*rescaleStatelessSource) Checkpoint(uint64) ([]byte, error) { return nil, nil }

type rescaleClusterMap struct{ *rescaleClusterSource }

func (*rescaleClusterMap) Map(_ context.Context, event engine.Event) (engine.Event, error) {
	return event, nil
}

type rescaleClusterSource struct {
	checkpointTestSource
	backend  *engine.PebbleStateBackend
	groups   rpc.KeyGroupRange
	restored *atomic.Int32
}

func (s *rescaleClusterSource) Open(context.Context) error {
	for group := s.groups.Start; group <= s.groups.End; group++ {
		key := make([]byte, 7)
		binary.BigEndian.PutUint16(key, uint16(group))
		if err := s.backend.Put(key, []byte(fmt.Sprint(group))); err != nil {
			return err
		}
	}
	return nil
}
func (s *rescaleClusterSource) Close() error { return s.backend.Close() }
func (s *rescaleClusterSource) CheckpointState(id uint64) (engine.SnapshotHandle, error) {
	return s.backend.Checkpoint(id)
}
func (s *rescaleClusterSource) RestoreState(handle engine.SnapshotHandle) error {
	return s.backend.Restore(handle)
}
func (s *rescaleClusterSource) RestoreKeyGroupState(ctx context.Context, assigned keygroup.KeyGroupRange, parts []engine.KeyGroupSnapshot) error {
	if err := s.backend.RestoreKeyGroupRanges(ctx, assigned, parts); err != nil {
		return err
	}
	count := 0
	err := s.backend.VisitKeyGroupRange(ctx, keygroup.KeyGroupRange{Start: 0, End: 128}, func(key, value []byte) error {
		group := binary.BigEndian.Uint16(key)
		if !assigned.Contains(group) || string(value) != fmt.Sprint(group) {
			return fmt.Errorf("wrong restored group %d value %q", group, value)
		}
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != assigned.Size() {
		return fmt.Errorf("restored %d groups, expected %d", count, assigned.Size())
	}
	s.restored.Add(1)
	return nil
}

func TestClusterSavepointRescalesKeyGroups(t *testing.T) {
	for _, sizes := range [][2]int{{4, 8}, {8, 4}, {4, 3}} {
		t.Run(fmt.Sprintf("%d-to-%d", sizes[0], sizes[1]), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			coord := coordinator.New(coordinator.CoordinatorConfig{NodeID: "coordinator"}, coordinator.NewMemoryStore(), nil, zerolog.Nop())
			coordDone := make(chan error, 1)
			go func() { coordDone <- coord.Run(ctx) }()
			waitFor(t, 2*time.Second, coord.IsReady)
			server := coordinator.NewTransportServer(coord, "127.0.0.1:0", zerolog.Nop())
			if err := server.Listen(); err != nil {
				t.Fatal(err)
			}
			serverDone := make(chan error, 1)
			go func() { serverDone <- server.Serve(ctx) }()
			api := coordinator.NewHTTPServer(coord, "127.0.0.1:0", zerolog.Nop())
			if err := api.Listen(); err != nil {
				t.Fatal(err)
			}
			apiDone := make(chan error, 1)
			go func() { apiDone <- api.Serve() }()
			defer func() { _ = api.Shutdown(context.Background()); <-apiDone }()
			var restored atomic.Int32
			registry := worker.NewRegistry()
			registry.RegisterSource("source", func(context.Context, []byte, worker.TaskContext) (engine.SourceOperator, error) {
				return &rescaleStatelessSource{}, nil
			})
			registry.RegisterMap("state", func(_ context.Context, _ []byte, tc worker.TaskContext) (engine.MapOperator, error) {
				backend, err := engine.NewStateBackend(engine.StateBackendConfig{Type: engine.StateBackendPebble, PebbleDataDir: t.TempDir()})
				if err != nil {
					return nil, err
				}
				return &rescaleClusterMap{&rescaleClusterSource{backend: backend.(*engine.PebbleStateBackend), groups: tc.KeyGroup, restored: &restored}}, nil
			})
			var workers []*worker.Worker
			var done []chan error
			for i := 0; i < 2; i++ {
				replica := &worker.CheckpointReplicaConfig{ListenAddr: "127.0.0.1:0", StoreRoot: t.TempDir(), ArtifactRoot: t.TempDir(), StagingRoot: t.TempDir(), Concurrency: 8}
				w := worker.NewWithRegistry(worker.Config{WorkerID: fmt.Sprint("worker-", i), CoordinatorAddr: server.Addr(), TaskSlots: 8, CheckpointReplica: replica}, registry, zerolog.Nop())
				workers = append(workers, w)
				ch := make(chan error, 1)
				done = append(done, ch)
				go func() { ch <- w.Run(ctx) }()
			}
			defer func() {
				cancel()
				for _, w := range workers {
					_ = w.Shutdown(context.Background())
				}
				for _, ch := range done {
					<-ch
				}
				_ = server.Shutdown(context.Background())
				<-serverDone
				<-coordDone
			}()
			waitFor(t, 3*time.Second, func() bool { return len(coord.ListWorkers()) == 2 })
			graph, err := protocol.EncodeMsgPack(rpc.JobGraph{NumKeyGroups: 128, Operators: []rpc.OperatorDescriptor{{OperatorID: "source", ClassName: "source", Type: rpc.OperatorTypeSource}, {OperatorID: "state", ClassName: "state", Type: rpc.OperatorTypeMap}}, Edges: []rpc.EdgeDescriptor{{SourceOperatorID: "source", TargetOperatorID: "state", Shuffle: rpc.ShuffleStrategyHash}}})
			if err != nil {
				t.Fatal(err)
			}
			job, err := coord.SubmitJob("rescale", sizes[0], graph)
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Second, func() bool {
				current, err := coord.GetJob(job.ID)
				return err == nil && current.Status == coordinator.JobRunning
			})
			sp, err := coord.TriggerSavepoint(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 10*time.Second, func() bool {
				current, err := coord.GetSavepoint(job.ID, sp.ID)
				return err == nil && current.Status == coordinator.SavepointCompleted
			})
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/api/v1/jobs/%s/rescale", api.Addr(), job.ID), strings.NewReader(fmt.Sprintf(`{"savepoint_id":%q,"parallelism":%d}`, sp.ID, sizes[1])))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode != http.StatusAccepted {
				t.Fatalf("rescale HTTP status %d: %s (%v)", response.StatusCode, body, readErr)
			}
			waitFor(t, 10*time.Second, func() bool {
				current, err := coord.GetJob(job.ID)
				return err == nil && current.Status == coordinator.JobRunning && restored.Load() == int32(sizes[1])
			})
			// The new deployment must be able to checkpoint its redistributed state,
			// releasing the old savepoint as a recovery dependency.
			replacement, err := coord.TriggerCheckpoint(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			waitFor(t, 10*time.Second, func() bool {
				current, err := coord.GetJob(job.ID)
				return err == nil && current.LatestCheckpoint == replacement.ID
			})
			if err := coord.DeleteSavepoint(job.ID, sp.ID); err != nil {
				t.Fatalf("replacement did not release old savepoint: %v", err)
			}

		})
	}
}
