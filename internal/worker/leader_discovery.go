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
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var failures []error
	for _, seed := range w.cfg.CoordinatorSeeds {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		leader, err := queryLeader(ctx, client, seed)
		if err == nil && !leader.IsSelf && leader.HTTPAddr != "" {
			// A standby's discovery record is only a hint. Ask the advertised node
			// itself; readiness must be established after durable recovery.
			leader, err = queryLeader(ctx, client, leader.HTTPAddr)
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
		failures = append(failures, fmt.Errorf("seed %s: %w", seed, err))
	}
	return "", errors.Join(failures...)
}

func queryLeader(ctx context.Context, client *http.Client, address string) (discoveredLeader, error) {
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
