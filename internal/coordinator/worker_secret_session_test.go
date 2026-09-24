package coordinator

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestWorkerCredentialAuthorizationTracksSession(t *testing.T) {
	c, _ := newTestCoordinator(t)
	req := RegisterWorkerRequest{WorkerID: "worker", Address: "worker:1", TaskSlotsTotal: 1, HighestSeenEpoch: c.epoch}
	oldPeer := rpc.NewClient(nil, rpc.DefaultConfig())
	oldDone := make(chan struct{})
	if _, err := c.registerWorker(req, oldPeer, oldDone, "worker"); err != nil {
		t.Fatal(err)
	}
	c.mu.RLock()
	authenticated := c.workers["worker"].RPCAuthenticated
	c.mu.RUnlock()
	if !authenticated {
		t.Fatal("verified current session not authorized")
	}
	replacement := rpc.NewClient(nil, rpc.DefaultConfig())
	replacementDone := make(chan struct{})
	if _, err := c.registerWorker(req, replacement, replacementDone, ""); err != nil {
		t.Fatal(err)
	}
	close(oldDone)
	c.mu.RLock()
	current := c.workers["worker"]
	if current.RPCAuthenticated || current.RPCClient != replacement {
		t.Error("plaintext replacement inherited old authorization")
	}
	c.mu.RUnlock()
	close(replacementDone)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		disconnected := c.workers["worker"].RPCClient == nil && !c.workers["worker"].RPCAuthenticated
		c.mu.RUnlock()
		if disconnected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("disconnect did not clear session authorization")
}

func TestWorkerSessionAuthorizationCannotBeRecoveredFromMetadata(t *testing.T) {
	original := WorkerMeta{ID: "worker", RPCAuthenticated: true, RPCPeerEpoch: 99}
	raw, err := protocol.EncodeMsgPack(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored WorkerMeta
	if err := protocol.DecodeMsgPack(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.RPCAuthenticated || restored.RPCPeerEpoch != 0 {
		t.Fatal("recovered transport authorization from metadata")
	}
	raw, err = json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("RPCAuthenticated")) {
		t.Fatal("exposed internal session state through JSON")
	}
}

func TestWorkerCredentialAuthorizationRequiresVerifiedIdentityAndLifetime(t *testing.T) {
	for _, tc := range []struct {
		name, identity string
		peer, lifetime bool
	}{
		{"plaintext", "", true, true}, {"wrong worker", "other", true, true},
		{"no connection", "worker", false, true}, {"no lifetime", "worker", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestCoordinator(t)
			var peer *rpc.Client
			if tc.peer {
				peer = rpc.NewClient(nil, rpc.DefaultConfig())
			}
			var done chan struct{}
			if tc.lifetime {
				done = make(chan struct{})
				defer close(done)
			}
			_, err := c.registerWorker(RegisterWorkerRequest{WorkerID: "worker", Address: "worker:1", TaskSlotsTotal: 1, HighestSeenEpoch: c.epoch}, peer, done, tc.identity)
			if err != nil {
				t.Fatal(err)
			}
			c.mu.RLock()
			authenticated := c.workers["worker"].RPCAuthenticated
			c.mu.RUnlock()
			if authenticated {
				t.Fatal("incomplete authentication authorized credentials")
			}
		})
	}
}
