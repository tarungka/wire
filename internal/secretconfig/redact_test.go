package secretconfig

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestRedactorFiltersCredentialRepresentations(t *testing.T) {
	secret := "credential\"/with + spaces\n"
	encoded, _ := json.Marshal(secret)
	r := NewRedactor([]string{"", secret, "credential"})
	for _, representation := range []string{secret, string(encoded[1 : len(encoded)-1]), url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawStdEncoding.EncodeToString([]byte(secret)), base64.URLEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret))} {
		if got := r.String("failure: " + representation + "!"); got != "failure: [REDACTED]!" {
			t.Fatalf("redaction left a credential suffix: %q", got)
		}
	}
	if got := r.String("unrelated transport failure"); got != "unrelated transport failure" {
		t.Fatal("changed unrelated diagnostics")
	}
}

func TestRedactorPreservesErrorClassification(t *testing.T) {
	sentinel := errors.New("checkpoint unavailable")
	source := fmt.Errorf("private-token: %w", sentinel)
	r := NewRedactor([]string{"private-token"})
	got := r.Error(source)
	if strings.Contains(got.Error(), "private-token") || !errors.Is(got, sentinel) {
		t.Fatal("lost redaction or classification")
	}
	if r.Error(nil) != nil {
		t.Fatal("changed nil error")
	}
	var noop *Redactor
	if noop.Error(source) != source || noop.String("plain") != "plain" {
		t.Fatal("nil redactor changed input")
	}
}

func TestRedactorConcurrentUseAndOverlappingValues(t *testing.T) {
	r := NewRedactor([]string{"abc", "abcdef"})
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.String("abcdef abc") != "[REDACTED] [REDACTED]" {
				t.Error("overlapping credentials not fully redacted")
			}
		}()
	}
	wg.Wait()
}
