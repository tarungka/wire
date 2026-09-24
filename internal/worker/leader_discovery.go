package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/apiclient"
)

type discoveredLeader struct {
	HTTPAddr string `json:"leader_http_addr"`
	RPCAddr  string `json:"leader_rpc_addr"`
	Epoch    uint64 `json:"leader_epoch"`
	IsSelf   bool   `json:"is_self"`
	Ready    bool   `json:"ready"`
}

func (w *Worker) discoverCoordinator(ctx context.Context) (string, error) {
	if len(w.cfg.CoordinatorSeeds) == 0 {
		return w.cfg.CoordinatorAddr, nil
	}
	w.mu.RLock()
	minimum := w.epoch
	w.mu.RUnlock()
	allowed := make(map[string]bool)
	for _, seed := range w.cfg.CoordinatorSeeds {
		if origin, err := discoveryOrigin(seed, "http"); err == nil {
			allowed[origin] = true
		}
	}
	var failures []error
	for index, seed := range w.cfg.CoordinatorSeeds {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		origin, err := discoveryOrigin(seed, "http")
		var leader discoveredLeader
		if err == nil {
			leader, err = w.queryConfiguredLeader(ctx, origin)
		}
		if err == nil && !leader.IsSelf && leader.HTTPAddr != "" {
			// A standby's discovery record is only a hint. Ask the advertised node
			// itself; readiness must be established after durable recovery.
			scheme := "http"
			if strings.HasPrefix(origin, "https://") {
				scheme = "https"
			}
			target, parseErr := discoveryOrigin(leader.HTTPAddr, scheme)
			secure := scheme == "https" || w.cfg.DiscoverySecurity != (apiclient.Config{})
			if parseErr != nil {
				err = parseErr
			} else if secure && (!allowed[target] || !strings.HasPrefix(target, "https://")) {
				err = fmt.Errorf("secure leader hint is not a configured HTTPS seed")
			} else {
				leader, err = w.queryConfiguredLeader(ctx, target)
			}
		}
		if err == nil {
			host, _, addressErr := net.SplitHostPort(leader.RPCAddr)
			if !leader.IsSelf || !leader.Ready || leader.Epoch < minimum || addressErr != nil || host == "" {
				err = fmt.Errorf("discovered coordinator is unready, stale, or has no routable RPC endpoint")
			}
		}
		if err == nil {
			return leader.RPCAddr, nil
		}
		failures = append(failures, fmt.Errorf("coordinator seed %d: %w", index, err))
	}
	return "", errors.Join(failures...)
}

func queryLeader(ctx context.Context, client interface {
	Do(*http.Request) (*http.Response, error)
}, address string) (discoveredLeader, error) {
	var result discoveredLeader
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	base, err := url.Parse(address)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		return result, fmt.Errorf("invalid coordinator discovery address")
	}
	base.Path = "/api/v1/cluster/leader"
	base.RawPath, base.RawQuery, base.Fragment = "", "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return result, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("leader query: HTTP %d", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result)
	return result, err
}

func discoveryOrigin(address, defaultScheme string) (string, error) {
	if !strings.Contains(address, "://") {
		address = defaultScheme + "://" + address
	}
	base, err := url.Parse(address)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" {
		return "", fmt.Errorf("invalid coordinator discovery address")
	}
	return base.Scheme + "://" + base.Host, nil
}

func (w *Worker) queryConfiguredLeader(ctx context.Context, origin string) (discoveredLeader, error) {
	client, err := apiclient.New(origin, w.cfg.DiscoverySecurity, time.Second)
	if err != nil {
		return discoveredLeader{}, err
	}
	defer client.CloseIdleConnections()
	return queryLeader(ctx, client, origin)
}
