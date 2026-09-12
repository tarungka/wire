package coordinator

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

type advisoryTestStore struct {
	MetadataStore
	flush func([]KVPair) error
}

func (s *advisoryTestStore) WriteBatchAsync(batch []KVPair) error { return s.flush(batch) }

// Deliberately interleave a durable registration with an older pending flush.
// Also model complete loss of the advisory write, which NoSync permits.
func TestHeartbeatDurability_Recovery(t *testing.T) {
	for _, lose := range []bool{false, true} {
		name := "retained"
		if lose {
			name = "lost"
		}
		t.Run(name, func(t *testing.T) {
			store := newTestPebbleStore(t)
			wrapped := &advisoryTestStore{MetadataStore: store}
			c := New(CoordinatorConfig{NodeID: "n1"}, wrapped, nil, zerolog.Nop())
			c.state = StateLeader
			old := &WorkerMeta{ID: "w1", Address: "old:5001", TaskSlotsTotal: 4, LastHeartbeat: time.Now().UTC()}
			if err := c.persistWorker(old); err != nil {
				t.Fatal(err)
			}
			called := false
			wrapped.flush = func(batch []KVPair) error {
				called = true
				newer := &WorkerMeta{ID: "w1", Address: "new:5001", TaskSlotsTotal: 8}
				if err := c.persistWorker(newer); err != nil {
					return err
				}
				if lose {
					return nil
				}
				return store.WriteBatchAsync(batch)
			}
			if err := c.flushHeartbeats(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("heartbeat flush did not use asynchronous capability")
			}
			state, err := recoverFromStore(store)
			if err != nil {
				t.Fatal(err)
			}
			w := state.workers["w1"]
			if w == nil || w.Address != "new:5001" || w.TaskSlotsTotal != 8 || !w.LastHeartbeat.IsZero() {
				t.Fatalf("recovered stale registration or trusted heartbeat: %+v", w)
			}
			if !lose {
				data, err := store.Get(WorkerHeartbeatKey("w1"))
				if err != nil {
					t.Fatal(err)
				}
				var heartbeat time.Time
				if err := protocol.DecodeMsgPack(data, &heartbeat); err != nil {
					t.Fatal(err)
				}
				if !heartbeat.Equal(old.LastHeartbeat) {
					t.Fatalf("heartbeat = %v", heartbeat)
				}
			}
		})
	}
}

// The child exits without closing Pebble. This exercises WAL recovery after a
// process exit; the loss test above separately models unsynced data loss.
func TestHeartbeatDurability_ProcessRecovery(t *testing.T) {
	const envKey = "WIRE_TEST_HEARTBEAT_CRASH_DIR"
	if dir := os.Getenv(envKey); dir != "" {
		store, err := NewPebbleStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		job, err := protocol.EncodeMsgPack(&JobMeta{ID: "j1", Name: "durable", Status: JobCreated})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.WriteBatch([]KVPair{{Key: JobMetaKey("j1"), Value: job}, {Key: JobConfigKey("j1"), Value: []byte("config")}}); err != nil {
			t.Fatal(err)
		}
		c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
		c.state = StateLeader
		if err := c.persistWorker(&WorkerMeta{ID: "w1", Address: "worker:5001", LastHeartbeat: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if err := c.flushHeartbeats(t.Context()); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHeartbeatDurability_ProcessRecovery$")
	cmd.Env = append(os.Environ(), envKey+"="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
	store, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	recovered, err := recoverFromStore(store)
	if err != nil {
		t.Fatal(err)
	}
	if job := recovered.jobs["j1"]; job == nil || job.Name != "durable" {
		t.Fatalf("lost acknowledged job: %+v", job)
	}
	config, err := store.Get(JobConfigKey("j1"))
	if err != nil || string(config) != "config" {
		t.Fatalf("lost job config: %q, %v", config, err)
	}
	if worker := recovered.workers["w1"]; worker == nil || !worker.LastHeartbeat.IsZero() {
		t.Fatalf("invalid recovered worker: %+v", worker)
	}
}
