package coordinator

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const leaderAnnotation = "wire.io/leader"

var errLeaseMissing = errors.New("lease not found")
var errLeaseConflict = errors.New("lease resource version conflict")
var errLeaseRejected = errors.New("kubernetes lease request rejected")

// KubernetesLeaseConfig configures the stable coordination.k8s.io/v1 API.
// No Kubernetes SDK is required; the backend uses GET/POST/PUT on one Lease.
type KubernetesLeaseConfig struct {
	APIServer, Namespace, LeaseName, TokenFile, CAFile string
	LeaseDuration, RenewDeadline, RetryPeriod          time.Duration
	// HTTPClient permits an explicit trust/proxy configuration (and API test servers).
	HTTPClient *http.Client
}

type leaseRecord struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string            `json:"name"`
		Namespace       string            `json:"namespace"`
		ResourceVersion string            `json:"resourceVersion,omitempty"`
		UID             string            `json:"uid,omitempty"`
		Annotations     map[string]string `json:"annotations,omitempty"`
		Labels          map[string]string `json:"labels,omitempty"`
	} `json:"metadata"`
	Spec struct {
		Holder      string `json:"holderIdentity,omitempty"`
		Duration    int32  `json:"leaseDurationSeconds,omitempty"`
		AcquireTime string `json:"acquireTime,omitempty"`
		RenewTime   string `json:"renewTime,omitempty"`
		Transitions int32  `json:"leaseTransitions,omitempty"`
	} `json:"spec"`
}

type kubernetesTerm struct {
	identity string
	nodeID   string
	grant    *LeaderContext
	timer    *time.Timer
	done     chan struct{}
	deadline time.Time // protected by updateMu
}

// KubernetesLeaseElection revokes local authority on renewal failure. Lease
// ownership alone is not storage fencing: HAService also owns an exclusive,
// authoritative metadata handle. Expiration uses locally observed elapsed time,
// never a wall-clock timestamp written by another host.
type KubernetesLeaseElection struct {
	cfg          KubernetesLeaseConfig
	client       *http.Client
	endpoint     string
	campaignMu   sync.Mutex
	updateMu     sync.Mutex
	mu           sync.Mutex
	term         *kubernetesTerm
	observedSpec string
	observedAt   time.Time
}

func NewKubernetesLeaseElection(cfg KubernetesLeaseConfig) (*KubernetesLeaseElection, error) {
	if cfg.LeaseDuration == 0 {
		cfg.LeaseDuration = 10 * time.Second
	}
	if cfg.RenewDeadline == 0 {
		cfg.RenewDeadline = 6 * time.Second
	}
	if cfg.RetryPeriod == 0 {
		cfg.RetryPeriod = time.Second
	}
	if cfg.RetryPeriod <= 0 || cfg.RenewDeadline <= cfg.RetryPeriod || cfg.LeaseDuration <= cfg.RenewDeadline || cfg.LeaseDuration%time.Second != 0 || cfg.LeaseDuration/time.Second > 2147483647 {
		return nil, fmt.Errorf("lease duration must be whole seconds and greater than renew deadline, which must exceed positive retry period")
	}
	if cfg.LeaseName == "" {
		cfg.LeaseName = "wire-coordinator"
	}
	if cfg.Namespace == "" {
		data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			return nil, fmt.Errorf("kubernetes namespace: %w", err)
		}
		cfg.Namespace = strings.TrimSpace(string(data))
	}
	if strings.ContainsAny(cfg.Namespace+cfg.LeaseName, "/\\?#") || cfg.Namespace == "" {
		return nil, fmt.Errorf("invalid Kubernetes lease identity")
	}
	if cfg.APIServer == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, fmt.Errorf("kubernetes API server is not configured")
		}
		cfg.APIServer = "https://" + net.JoinHostPort(host, port)
	}
	base, err := url.Parse(cfg.APIServer)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("kubernetes API server must be an HTTPS URL")
	}
	client := cfg.HTTPClient
	if client == nil {
		if cfg.TokenFile == "" {
			cfg.TokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		}
		if cfg.CAFile == "" {
			cfg.CAFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
		}
		ca, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, fmt.Errorf("invalid Kubernetes API CA")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
		client = &http.Client{Transport: transport}
	}
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	endpoint := strings.TrimRight(base.String(), "/") + "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(cfg.Namespace) + "/leases"
	return &KubernetesLeaseElection{cfg: cfg, client: &copied, endpoint: endpoint}, nil
}

func (e *KubernetesLeaseElection) request(ctx context.Context, method string, record *leaseRecord) (*leaseRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, min(2*time.Second, e.cfg.RenewDeadline/2))
	defer cancel()
	endpoint := e.endpoint
	if method != http.MethodPost {
		endpoint += "/" + url.PathEscape(e.cfg.LeaseName)
	}
	var body io.Reader
	if record != nil {
		data, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.cfg.TokenFile != "" {
		token, err := os.ReadFile(e.cfg.TokenFile)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, errLeaseMissing
	case http.StatusConflict:
		return nil, errLeaseConflict
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil, fmt.Errorf("%w: HTTP %d", errLeaseRejected, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kubernetes Lease %s: HTTP %d", method, resp.StatusCode)
	}
	var result leaseRecord
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (e *KubernetesLeaseElection) Campaign(ctx context.Context, nodeID string) (*LeaderContext, error) {
	e.campaignMu.Lock()
	defer e.campaignMu.Unlock()
	e.mu.Lock()
	active := e.term != nil
	e.mu.Unlock()
	if active {
		return nil, fmt.Errorf("resign previous Kubernetes term before campaigning")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	identity := nodeID + "-" + hex.EncodeToString(nonce)
	for ctx.Err() == nil {
		started := time.Now()
		record, err := e.request(ctx, http.MethodGet, nil)
		method := http.MethodPut
		if errors.Is(err, errLeaseMissing) {
			record = &leaseRecord{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
			record.Metadata.Name, record.Metadata.Namespace = e.cfg.LeaseName, e.cfg.Namespace
			err = nil
			method = http.MethodPost
		}
		if err == nil {
			spec, _ := json.Marshal(record.Spec)
			if string(spec) != e.observedSpec {
				e.observedSpec = string(spec)
				e.observedAt = time.Now()
			}
			vacant := record.Spec.Holder == ""
			expired := record.Spec.Duration > 0 && time.Since(e.observedAt) >= time.Duration(record.Spec.Duration)*time.Second
			if vacant || expired {
				record.Spec.Holder = identity
				record.Spec.Duration = int32(e.cfg.LeaseDuration / time.Second)
				record.Spec.AcquireTime = time.Now().UTC().Format("2006-01-02T15:04:05.000000Z")
				record.Spec.RenewTime = record.Spec.AcquireTime
				if record.Spec.Transitions == 2147483647 {
					return nil, fmt.Errorf("kubernetes Lease transitions exhausted")
				}
				record.Spec.Transitions++
				delete(record.Metadata.Annotations, leaderAnnotation)
				_, err = e.request(ctx, method, record)
				if err == nil && time.Since(started) < e.cfg.RenewDeadline {
					leaderCtx, cancel := context.WithCancel(ctx)
					term := &kubernetesTerm{identity: identity, nodeID: nodeID, grant: &LeaderContext{Ctx: leaderCtx, Cancel: cancel}, done: make(chan struct{}), deadline: started.Add(e.cfg.RenewDeadline)}
					term.timer = time.AfterFunc(time.Until(term.deadline), cancel)
					e.mu.Lock()
					e.term = term
					e.mu.Unlock()
					go e.renew(term)
					return term.grant, nil
				}
			}
		}
		if errors.Is(err, errLeaseRejected) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(e.cfg.RetryPeriod):
		}
	}
	return nil, ctx.Err()
}

func (e *KubernetesLeaseElection) renew(term *kubernetesTerm) {
	defer close(term.done)
	defer term.grant.Cancel()
	defer term.timer.Stop()
	ticker := time.NewTicker(e.cfg.RetryPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-term.grant.Ctx.Done():
			return
		case <-ticker.C:
		}
		e.updateMu.Lock()
		err := e.renewOnce(term)
		e.updateMu.Unlock()
		if errors.Is(err, ErrNotLeader) {
			return
		}
	}
}

func (e *KubernetesLeaseElection) renewOnce(term *kubernetesTerm) error {
	if term.grant.Ctx.Err() != nil || !time.Now().Before(term.deadline) {
		return ErrNotLeader
	}
	ctx, cancel := context.WithDeadline(term.grant.Ctx, term.deadline)
	defer cancel()
	started := time.Now()
	record, err := e.request(ctx, http.MethodGet, nil)
	if err != nil {
		return err
	}
	if record.Spec.Holder != term.identity || record.Spec.Duration != int32(e.cfg.LeaseDuration/time.Second) {
		return ErrNotLeader
	}
	record.Spec.RenewTime = started.UTC().Format("2006-01-02T15:04:05.000000Z")
	if _, err = e.request(ctx, http.MethodPut, record); err != nil {
		return err
	}
	if term.grant.Ctx.Err() != nil || !time.Now().Before(term.deadline) {
		return ErrNotLeader
	}
	// Confirm from request send time, so a delayed API response cannot extend
	// local authority beyond the conservative renewal budget.
	term.deadline = started.Add(e.cfg.RenewDeadline)
	term.timer.Reset(time.Until(term.deadline))
	return nil
}

func (e *KubernetesLeaseElection) Resign(ctx context.Context) error {
	e.mu.Lock()
	term := e.term
	e.mu.Unlock()
	if term == nil {
		return nil
	}
	term.grant.Cancel()
	<-term.done
	e.updateMu.Lock()
	defer e.updateMu.Unlock()
	record, err := e.request(ctx, http.MethodGet, nil)
	if err == nil && record.Spec.Holder == term.identity {
		record.Spec.Holder = ""
		delete(record.Metadata.Annotations, leaderAnnotation)
		_, err = e.request(ctx, http.MethodPut, record)
	}
	e.mu.Lock()
	if e.term == term {
		e.term = nil
	}
	e.mu.Unlock()
	// Unavailable API leaves the lease to expire; it never renews authority.
	if errors.Is(err, errLeaseMissing) || errors.Is(err, errLeaseConflict) {
		return nil
	}
	return err
}

func (e *KubernetesLeaseElection) PublishLeader(ctx context.Context, info LeaderInfo) error {
	e.mu.Lock()
	term := e.term
	e.mu.Unlock()
	if term == nil {
		return ErrNotLeader
	}
	e.updateMu.Lock()
	defer e.updateMu.Unlock()
	if term.grant.Ctx.Err() != nil || info.NodeID != term.nodeID {
		return ErrNotLeader
	}
	bounded, cancel := context.WithDeadline(ctx, term.deadline)
	stop := context.AfterFunc(term.grant.Ctx, cancel)
	defer cancel()
	defer stop()
	record, err := e.request(bounded, http.MethodGet, nil)
	if err != nil {
		return err
	}
	if record.Spec.Holder != term.identity || record.Spec.Duration != int32(e.cfg.LeaseDuration/time.Second) {
		return ErrNotLeader
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if record.Metadata.Annotations == nil {
		record.Metadata.Annotations = make(map[string]string)
	}
	record.Metadata.Annotations[leaderAnnotation] = string(data)
	_, err = e.request(bounded, http.MethodPut, record)
	return err
}

func (e *KubernetesLeaseElection) ReadLeader(ctx context.Context) (*LeaderInfo, error) {
	record, err := e.request(ctx, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	if record.Spec.Holder == "" {
		return nil, ErrNoLeader
	}
	var info LeaderInfo
	if err := json.Unmarshal([]byte(record.Metadata.Annotations[leaderAnnotation]), &info); err != nil {
		return nil, ErrNoLeader
	}
	if info.NodeID == "" || info.Address == "" {
		return nil, ErrNoLeader
	}
	return &info, nil
}
func (e *KubernetesLeaseElection) GetLeader(ctx context.Context) (string, string, error) {
	info, err := e.ReadLeader(ctx)
	if err != nil {
		return "", "", err
	}
	return info.NodeID, info.Address, nil
}
func (e *KubernetesLeaseElection) Close() error { return e.Resign(context.Background()) }
