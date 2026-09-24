// Package httpapi implements the WIP-16 JSON HTTP ingest and delivery connector.
package httpapi

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/tarungka/wire/internal/engine"
)

type Auth struct {
	Type     string `codec:"type" json:"type"`
	Token    string `codec:"token" json:"token"`
	Username string `codec:"username" json:"username"`
	Password string `codec:"password" json:"password"`
}

func (a Auth) validate() error {
	switch a.Type {
	case "", "none":
		return nil
	case "bearer":
		if a.Token != "" {
			return nil
		}
	case "basic":
		if a.Username != "" && a.Password != "" {
			return nil
		}
	}
	return fmt.Errorf("http-api: invalid auth configuration")
}
func (a Auth) apply(r *http.Request) {
	switch a.Type {
	case "bearer":
		r.Header.Set("Authorization", "Bearer "+a.Token)
	case "basic":
		r.SetBasicAuth(a.Username, a.Password)
	}
}
func (a Auth) authorized(r *http.Request) bool {
	switch a.Type {
	case "bearer":
		return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.Token)) == 1
	case "basic":
		u, p, ok := r.BasicAuth()
		return ok && subtle.ConstantTimeCompare([]byte(u), []byte(a.Username)) == 1 && subtle.ConstantTimeCompare([]byte(p), []byte(a.Password)) == 1
	default:
		return true
	}
}

type jsonEvent struct {
	Key       string            `json:"key"`
	Value     string            `json:"value"`
	EventTime int64             `json:"event_time"`
	Headers   map[string]string `json:"headers,omitempty"`
}
type envelope struct {
	Events []jsonEvent `json:"events"`
}

func (e jsonEvent) event() engine.Event {
	headers := make(map[string][]byte, len(e.Headers))
	for k, v := range e.Headers {
		headers[k] = []byte(v)
	}
	return engine.Event{Key: []byte(e.Key), Value: []byte(e.Value), EventTime: e.EventTime, Headers: headers}
}
func toJSON(e engine.Event) (jsonEvent, error) {
	if !utf8.Valid(e.Key) || !utf8.Valid(e.Value) {
		return jsonEvent{}, fmt.Errorf("http-api: JSON event key/value must be UTF-8")
	}
	headers := make(map[string]string, len(e.Headers))
	for k, v := range e.Headers {
		if !utf8.ValidString(k) || !utf8.Valid(v) {
			return jsonEvent{}, fmt.Errorf("http-api: JSON headers must be UTF-8")
		}
		headers[k] = string(v)
	}
	return jsonEvent{Key: string(e.Key), Value: string(e.Value), EventTime: e.EventTime, Headers: headers}, nil
}
