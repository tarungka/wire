package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

type crashRecoveryReport struct {
	Epoch     uint64
	Jobs      int
	Running   int
	Completed int
	Aborted   bool
}

// The helper runs in a separate process so Kill tests OS lock release and WAL
// recovery, rather than a graceful Close disguised as a crash.
func TestHAProcessHelper(t *testing.T) {
	root := os.Getenv("WIRE_HA_PROCESS_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	first := os.Getenv("WIRE_HA_PROCESS_FIRST") == "1"
	open := func() (MetadataStore, error) {
		store, err := NewPebbleStore(filepath.Join(root, "metadata"))
		if err != nil {
			return nil, err
		}
		if first {
			var batch []KVPair
			for i := 0; i < 100; i++ {
				status := JobPaused
				if i < 50 {
					status = JobRunning
				}
				job := JobMeta{ID: fmt.Sprintf("job-%d", i), Name: fmt.Sprintf("job-%d", i), Status: status, Parallelism: 1}
				if i == 0 {
					job.LatestCheckpoint = 10
				}
				raw, err := protocol.EncodeMsgPack(job)
				if err != nil {
					_ = store.Close()
					return nil, err
				}
				batch = append(batch, KVPair{Key: JobMetaKey(job.ID), Value: raw})
			}
			for i := uint64(1); i <= 10; i++ {
				raw, err := protocol.EncodeMsgPack(CheckpointMeta{ID: i, JobID: "job-0", EpochID: 1, Status: CheckpointCompleted})
				if err != nil {
					_ = store.Close()
					return nil, err
				}
				batch = append(batch, KVPair{Key: CheckpointKey("job-0", i), Value: raw})
			}
			if err := store.WriteBatch(batch); err != nil {
				_ = store.Close()
				return nil, err
			}
		}
		return store, nil
	}
	node := os.Getenv("WIRE_HA_PROCESS_NODE")
	election := NewFileLockElection(filepath.Join(root, "leader.lock"), "pending")
	service := NewHAService(CoordinatorConfig{NodeID: node, ListenAddr: "127.0.0.1:0"}, "127.0.0.1:0", election, open, nil, zerolog.Nop())
	if err := service.Listen(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, node+".listen"), []byte(service.HTTPAddr()), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			coord, ready := service.CurrentCoordinator()
			if !ready {
				time.Sleep(time.Millisecond)
				continue
			}
			if first {
				raw, err := protocol.EncodeMsgPack(CheckpointMeta{ID: 11, JobID: "job-0", EpochID: coord.CurrentEpoch(), Status: CheckpointTriggered})
				if err != nil {
					panic(err)
				}
				if err := coord.store.Set(CheckpointKey("job-0", 11), raw); err != nil {
					panic(err)
				}
			}
			report := crashRecoveryReport{Epoch: coord.CurrentEpoch()}
			coord.mu.RLock()
			report.Jobs = len(coord.jobs)
			for _, job := range coord.jobs {
				if job.Status == JobRunning {
					report.Running++
				}
			}
			coord.mu.RUnlock()
			for i := uint64(1); i <= 11; i++ {
				raw, err := coord.store.Get(CheckpointKey("job-0", i))
				if err != nil {
					panic(err)
				}
				var cp CheckpointMeta
				if err := protocol.DecodeMsgPack(raw, &cp); err != nil {
					panic(err)
				}
				if cp.Status == CheckpointCompleted {
					report.Completed++
				}
				if i == 11 {
					report.Aborted = cp.Status == CheckpointAborted
				}
			}
			data, err := json.Marshal(report)
			if err != nil {
				panic(err)
			}
			if err := os.WriteFile(filepath.Join(root, node+".ready"), data, 0o600); err != nil {
				panic(err)
			}
			return
		}
	}()
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHACrashReopensDurableMetadataInStandbyProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("hard process-kill integration test")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	start := func(node string, first bool) (*exec.Cmd, <-chan struct{}) {
		t.Helper()
		cmd := exec.Command(exe, "-test.run=^TestHAProcessHelper$")
		cmd.Env = append(os.Environ(), "WIRE_HA_PROCESS_ROOT="+root, "WIRE_HA_PROCESS_NODE="+node)
		if first {
			cmd.Env = append(cmd.Env, "WIRE_HA_PROCESS_FIRST=1")
		} else {
			cmd.Env = append(cmd.Env, "WIRE_HA_PROCESS_FIRST=0")
		}
		log, err := os.Create(filepath.Join(root, node+".log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			_ = log.Close()
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		t.Cleanup(func() { _ = cmd.Process.Kill(); <-done; _ = log.Close() })
		return cmd, done
	}
	await := func(node string, done <-chan struct{}) crashRecoveryReport {
		t.Helper()
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			raw, err := os.ReadFile(filepath.Join(root, node+".ready"))
			var report crashRecoveryReport
			if err == nil && json.Unmarshal(raw, &report) == nil {
				return report
			}
			select {
			case <-done:
				log, _ := os.ReadFile(filepath.Join(root, node+".log"))
				t.Fatalf("%s exited before readiness: %s", node, log)
			case <-deadline.C:
				log, _ := os.ReadFile(filepath.Join(root, node+".log"))
				t.Fatalf("%s did not recover: %s", node, log)
			case <-tick.C:
			}
		}
	}
	first, firstDone := start("first", true)
	before := await("first", firstDone)
	_, secondDone := start("second", false)
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		address, _ := os.ReadFile(filepath.Join(root, "second.listen"))
		if len(address) > 0 {
			resp, err := client.Get("http://" + string(address) + "/healthz")
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("standby process did not serve health while leader owned storage")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The second process has been launched against a database still owned by
	// the first. No graceful coordinator or store shutdown occurs here.
	began := time.Now()
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-firstDone
	after := await("second", secondDone)
	if after.Epoch <= before.Epoch || after.Jobs != 100 || after.Running != 50 || after.Completed != 10 || !after.Aborted {
		t.Fatalf("crash recovery lost state: before=%+v after=%+v", before, after)
	}
	if elapsed := time.Since(began); elapsed >= 10*time.Second {
		t.Fatalf("restart exceeded target: %s", elapsed)
	}
	t.Logf("hard-kill takeover recovered 100 jobs and 10 completed checkpoints in %s", time.Since(began))
}
