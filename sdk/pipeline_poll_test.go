package sdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/apiclient"
)

func TestReloadReadRetriesTransientFailures(t *testing.T) {
	for _, failure := range []string{"unavailable", "disconnect", "truncated"} {
		t.Run(failure, func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("retried a mutation: %s", r.Method)
				}
				if reads.Add(1) == 1 {
					switch failure {
					case "unavailable":
						w.WriteHeader(http.StatusServiceUnavailable)
					case "disconnect":
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
					case "truncated":
						w.Header().Set("Content-Length", "100")
						_, _ = io.WriteString(w, `{"id":`)
					}
					return
				}
				_, _ = io.WriteString(w, `{"id":"confirmed"}`)
			}))
			defer server.Close()
			client, err := apiclient.New(server.URL, apiclient.Config{}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := reloadReadJSON[struct{ ID string }](ctx, client, server.URL)
			if err != nil || result.ID != "confirmed" || reads.Load() != 2 {
				t.Fatalf("result=%+v err=%v reads=%d", result, err, reads.Load())
			}
		})
	}
}

func TestReloadReadDoesNotRetryPermanentFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusBadRequest, http.StatusNotImplemented, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reads.Add(1)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"invalid":]}`)
			}))
			defer server.Close()
			client, err := apiclient.New(server.URL, apiclient.Config{}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			_, err = reloadReadJSON[map[string]any](t.Context(), client, server.URL)
			if err == nil || reads.Load() != 1 {
				t.Fatalf("err=%v reads=%d", err, reads.Load())
			}
		})
	}
}

func TestReloadReadCancellationInterruptsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		cancel()
	}))
	defer server.Close()
	client, err := apiclient.New(server.URL, apiclient.Config{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	_, err = reloadReadJSON[map[string]any](ctx, client, server.URL)
	if !errors.Is(err, context.Canceled) || reads.Load() != 1 {
		t.Fatalf("err=%v reads=%d", err, reads.Load())
	}
}

func TestReloadReadMissingFieldsCannotReusePreviousIdentity(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if reads.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"ID":"old","Status":"COMPLETED"}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	client, err := apiclient.New(server.URL, apiclient.Config{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	type state struct{ ID, Status string }
	old, err := reloadReadJSON[state](t.Context(), client, server.URL)
	if err != nil || old.ID != "old" {
		t.Fatalf("old=%+v err=%v", old, err)
	}
	next, err := reloadReadJSON[state](t.Context(), client, server.URL)
	if err != nil || next != (state{}) {
		t.Fatalf("stale fields inherited: %+v %v", next, err)
	}
}
