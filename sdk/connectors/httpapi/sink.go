package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tarungka/wire/internal/engine"
)

type SinkConfig struct {
	URL                 string            `codec:"url"`
	Method              string            `codec:"method"`
	Headers             map[string]string `codec:"headers"`
	Auth                Auth              `codec:"auth"`
	BatchSize           int               `codec:"batch_size"`
	Timeout             time.Duration     `codec:"timeout"`
	MaxAttempts         int               `codec:"max_attempts"`
	InitialDelay        time.Duration     `codec:"initial_delay"`
	MaxDelay            time.Duration     `codec:"max_delay"`
	Backoff             string            `codec:"backoff"`
	IdempotencyKeyField string            `codec:"idempotency_key_field"`
	AllowInsecure       bool              `codec:"allow_insecure"`
}

// DeliveryError reports a failed request without including response bodies or
// credentials. Permanent distinguishes non-retryable HTTP errors for callers.
type DeliveryError struct {
	StatusCode int
	Permanent  bool
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("http-api: delivery failed (status %d, permanent=%t)", e.StatusCode, e.Permanent)
}

// Sink sends each Write synchronously. WriteBatch amortizes request overhead;
// it never acknowledges a locally buffered event before delivery succeeds.
type Sink struct {
	config SinkConfig
	client *http.Client
}

func NewSink(c SinkConfig) (*Sink, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || !c.AllowInsecure)) {
		return nil, fmt.Errorf("http-api: valid HTTPS URL required (HTTP requires allow_insecure)")
	}
	if c.Method == "" {
		c.Method = http.MethodPost
	}
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 3
	}
	if c.InitialDelay == 0 {
		c.InitialDelay = time.Second
	}
	if c.MaxDelay == 0 {
		c.MaxDelay = 30 * time.Second
	}
	if c.Backoff == "" {
		c.Backoff = "exponential"
	}
	if c.BatchSize < 1 || c.Timeout < 0 || c.MaxAttempts < 1 || c.InitialDelay < 0 || c.MaxDelay < c.InitialDelay || (c.Backoff != "constant" && c.Backoff != "exponential") {
		return nil, fmt.Errorf("http-api: invalid sink limits or backoff")
	}
	if err = c.Auth.validate(); err != nil {
		return nil, err
	}
	headers := make(map[string]string, len(c.Headers))
	for k, v := range c.Headers {
		headers[k] = v
	}
	c.Headers = headers
	return &Sink{config: c, client: &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone(), Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *Sink) Open(context.Context) error        { return nil }
func (s *Sink) Close() error                      { s.client.CloseIdleConnections(); return nil }
func (s *Sink) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (s *Sink) Write(ctx context.Context, event engine.Event) error {
	return s.WriteBatch(ctx, []engine.Event{event})
}
func (s *Sink) WriteBatch(ctx context.Context, events []engine.Event) error {
	for start := 0; start < len(events); start += s.config.BatchSize {
		if err := s.send(ctx, events[start:min(start+s.config.BatchSize, len(events))]); err != nil {
			return err
		}
	}
	return nil
}
func (s *Sink) send(ctx context.Context, events []engine.Event) error {
	request := envelope{Events: make([]jsonEvent, len(events))}
	ids := make([]string, 0, len(events))
	for i, e := range events {
		event, err := toJSON(e)
		if err != nil {
			return err
		}
		request.Events[i] = event
		if s.config.IdempotencyKeyField != "" {
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(e.Value, &fields); err != nil {
				return fmt.Errorf("http-api: idempotency field requires JSON object value")
			}
			id, ok := fields[s.config.IdempotencyKeyField]
			if !ok || string(id) == "null" {
				return fmt.Errorf("http-api: missing idempotency field")
			}
			var canonical any
			decoder := json.NewDecoder(bytes.NewReader(id))
			decoder.UseNumber()
			if err = decoder.Decode(&canonical); err != nil {
				return err
			}
			encoded, err := json.Marshal(canonical)
			if err != nil {
				return err
			}
			ids = append(ids, string(encoded))
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return err
	}
	batchID := hex.EncodeToString(random)
	var idempotency string
	if len(ids) > 0 {
		encoded, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(encoded)
		idempotency = hex.EncodeToString(sum[:])
	}
	delay := s.config.InitialDelay
	for attempt := 0; attempt < s.config.MaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, s.config.Method, s.config.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		for key, value := range s.config.Headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Wire-Batch-ID", batchID)
		if idempotency != "" {
			req.Header.Set("X-Idempotency-Key", idempotency)
		}
		s.config.Auth.apply(req)
		response, sendErr := s.client.Do(req)
		retryDelay := delay
		if sendErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
			permanent := response.StatusCode < 500 && response.StatusCode != 429
			sendErr = &DeliveryError{StatusCode: response.StatusCode, Permanent: permanent}
			if permanent {
				return sendErr
			}
			if response.StatusCode == 429 {
				retryDelay = retryAfter(response.Header.Get("Retry-After"), delay, s.config.MaxDelay)
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt+1 == s.config.MaxAttempts {
			return sendErr
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if s.config.Backoff == "exponential" {
			if delay > s.config.MaxDelay/2 {
				delay = s.config.MaxDelay
			} else {
				delay = min(delay*2, s.config.MaxDelay)
			}
		}
	}
	return nil
}
func retryAfter(value string, fallback, maximum time.Duration) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		if seconds > int64(maximum/time.Second) {
			return maximum
		}
		return min(time.Duration(seconds)*time.Second, maximum)
	}
	if date, err := http.ParseTime(value); err == nil {
		return min(max(time.Until(date), 0), maximum)
	}
	return fallback
}
