package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

type leaseAPIFixture struct {
	mu          sync.Mutex
	record      *leaseRecord
	version     int
	unavailable bool
	conflicts   int
}

func (a *leaseAPIFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unavailable {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if a.record == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(a.record)
	case http.MethodPost, http.MethodPut:
		var record leaseRecord
		if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if a.conflicts > 0 {
			a.conflicts--
			w.WriteHeader(http.StatusConflict)
			return
		}
		if r.Method == http.MethodPost && a.record != nil || r.Method == http.MethodPut && (a.record == nil || record.Metadata.ResourceVersion != a.record.Metadata.ResourceVersion) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		a.version++
		record.Metadata.ResourceVersion = strconv.Itoa(a.version)
		a.record = &record
		_ = json.NewEncoder(w).Encode(a.record)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func testLeaseElection(t *testing.T, server *httptest.Server) *KubernetesLeaseElection {
	t.Helper()
	e, err := NewKubernetesLeaseElection(KubernetesLeaseConfig{APIServer: server.URL, Namespace: "wire", LeaseName: "coordinator", HTTPClient: server.Client(), LeaseDuration: time.Second, RenewDeadline: 300 * time.Millisecond, RetryPeriod: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestKubernetesLeaseCompetingCandidatesAndDiscovery(t *testing.T) {
	api := &leaseAPIFixture{conflicts: 1}
	server := httptest.NewTLSServer(api)
	defer server.Close()
	first, second := testLeaseElection(t, server), testLeaseElection(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	one, err := first.Campaign(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	info := LeaderInfo{NodeID: "one", Address: "one:4001", RPCAddress: "one:4002", Epoch: 99}
	if err := first.PublishLeader(ctx, info); err != nil {
		t.Fatal(err)
	}
	got, err := second.ReadLeader(ctx)
	if err != nil || *got != info {
		t.Fatalf("discovery: %+v %v", got, err)
	}
	result := make(chan *LeaderContext, 1)
	errs := make(chan error, 1)
	go func() {
		grant, err := second.Campaign(ctx, "two")
		if err != nil {
			errs <- err
			return
		}
		result <- grant
	}()
	select {
	case <-result:
		t.Fatal("two active lease holders")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(150 * time.Millisecond):
	}
	if one.Ctx.Err() != nil {
		t.Fatal("renewing leader lost authority")
	}
	if err := first.Resign(ctx); err != nil {
		t.Fatal(err)
	}
	if one.Ctx.Err() == nil {
		t.Fatal("resigned grant remains active")
	}
	select {
	case grant := <-result:
		if grant.Ctx.Err() != nil {
			t.Fatal("new grant canceled")
		}
	case err := <-errs:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("standby did not acquire resigned lease")
	}
}

func TestKubernetesLeaseAPIFailureRevokesAndExpires(t *testing.T) {
	api := &leaseAPIFixture{}
	server := httptest.NewTLSServer(api)
	defer server.Close()
	first, second := testLeaseElection(t, server), testLeaseElection(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	grant, err := first.Campaign(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	api.unavailable = true
	api.mu.Unlock()
	select {
	case <-grant.Ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("API outage did not revoke local authority")
	}
	// Simulate a crashed holder without voluntarily clearing its lease. The
	// standby waits from its own observation, regardless of remote wall time.
	api.mu.Lock()
	api.unavailable = false
	api.record.Spec.RenewTime = "2999-01-01T00:00:00.000000Z"
	api.mu.Unlock()
	started := time.Now()
	next, err := second.Campaign(ctx, "two")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < time.Second {
		t.Fatal("stole a lease without waiting its observed duration")
	}
	if next.Ctx.Err() != nil {
		t.Fatal("new leader not active")
	}
}

func TestKubernetesLeaseRejectsInvalidBudgets(t *testing.T) {
	_, err := NewKubernetesLeaseElection(KubernetesLeaseConfig{LeaseDuration: time.Second, RenewDeadline: time.Second, RetryPeriod: time.Millisecond})
	if err == nil {
		t.Fatal("accepted overlapping renewal and takeover budgets")
	}
}

func TestKubernetesLeaseHungRenewalRevokesAuthority(t *testing.T) {
	api := &leaseAPIFixture{}
	var mu sync.Mutex
	blocked := false
	entered := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hang := blocked
		mu.Unlock()
		if hang {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		api.ServeHTTP(w, r)
	}))
	defer server.Close()
	election := testLeaseElection(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	grant, err := election.Campaign(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	blocked = true
	mu.Unlock()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("renewal did not enter API")
	}
	select {
	case <-grant.Ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("hung API extended local authority")
	}
	mu.Lock()
	blocked = false
	mu.Unlock()
}

func TestKubernetesLeaseDeniedCredentialsFailCampaign(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	election := testLeaseElection(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := election.Campaign(ctx, "node"); !errors.Is(err, errLeaseRejected) {
		t.Fatalf("authorization failure hidden by retry: %v", err)
	}
}
