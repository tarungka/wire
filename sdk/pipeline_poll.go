package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/tarungka/wire/internal/apiclient"
)

type reloadHTTPError struct{ status int }

func (e *reloadHTTPError) Error() string { return fmt.Sprintf("sdk: reload HTTP %d", e.status) }

// Every attempt decodes into a fresh value. A truncated response, or a response
// missing fields, must never inherit identity/status from an earlier poll.
func reloadRequestJSON[T any](ctx context.Context, client *apiclient.Client, method, target string, status int, body []byte) (T, error) {
	var result T
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		return result, &reloadHTTPError{response.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return result, err
	}
	if len(data) > 1<<20 {
		return result, fmt.Errorf("sdk: reload response exceeds 1 MiB")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return result, io.ErrUnexpectedEOF
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, err
	}
	return result, nil
}

func retryableReloadRead(err error) bool {
	var status *reloadHTTPError
	if errors.As(err, &status) {
		switch status.status {
		case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		return true
	}
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

// Only reads retry. Authentication, protocol and identity failures are terminal;
// callers must supply a context deadline to bound a prolonged outage.
func reloadReadJSON[T any](ctx context.Context, client *apiclient.Client, target string) (T, error) {
	delay := 100 * time.Millisecond
	for {
		result, err := reloadRequestJSON[T](ctx, client, http.MethodGet, target, http.StatusOK, nil)
		if err == nil || !retryableReloadRead(err) {
			return result, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
		if delay < 2*time.Second {
			delay *= 2
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
		}
	}
}
