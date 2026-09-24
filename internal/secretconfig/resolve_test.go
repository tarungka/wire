package secretconfig

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResolvePreservesUnresolvedInputAndEscapesCredentials(t *testing.T) {
	raw := []byte(`{"headers":{"Authorization":"Bearer ${TOKEN}"},"values":["${EMPTY:-fallback}","${MISSING:-default}"],"large":9007199254740993}`)
	original := bytes.Clone(raw)
	secret := "quotes\"\nbackslash\\${DO_NOT_EXPAND}"
	resolved, err := Resolve(raw, func(name string) (string, bool) {
		switch name {
		case "TOKEN":
			return secret, true
		case "EMPTY":
			return "", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("mutated durable configuration")
	}
	var got struct {
		Headers map[string]string
		Values  []string
		Large   json.Number
	}
	if err := json.Unmarshal(resolved, &got); err != nil {
		t.Fatal(err)
	}
	if got.Headers["Authorization"] != "Bearer "+secret {
		t.Fatal("changed credential")
	}
	if got.Values[0] != "" || got.Values[1] != "default" || got.Large.String() != "9007199254740993" {
		t.Fatalf("changed config values: %+v", got.Values)
	}
	resolved[0] = 'x'
	if !bytes.Equal(raw, original) {
		t.Fatal("aliased input")
	}
}

func TestResolveMissingAndMalformedReferencesReturnNoSecret(t *testing.T) {
	for _, raw := range []string{
		`{"token":"${MISSING}"}`, `{"token":"${UNFINISHED"}`,
		`{"token":"${BAD-NAME}"}`, `{"token":"${}"}`,
		`{"token":"${N:-${OTHER}}"}`, `{"${KEY}":"value"}`,
		`{"token":"${TOKEN}"} {}`, `private-secret ${TOKEN}`,
	} {
		t.Run(raw, func(t *testing.T) {
			result, err := Resolve([]byte(raw), func(string) (string, bool) { return "", false })
			if err == nil || result != nil {
				t.Fatal("accepted invalid reference or returned partial result")
			}
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), raw) {
				t.Fatal("error exposed configuration")
			}
		})
	}
}

func TestResolveOpaqueConfigAndEmptyDefault(t *testing.T) {
	raw := []byte{0xff, 0x00, 0x01}
	resolved, err := Resolve(raw, func(string) (string, bool) { t.Fatal("unexpected lookup"); return "", false })
	if err != nil || !bytes.Equal(raw, resolved) {
		t.Fatal("changed opaque config")
	}
	resolved[0] = 0
	if raw[0] != 0xff {
		t.Fatal("opaque result aliases durable bytes")
	}
	resolved, err = Resolve([]byte(`{"value":"prefix${OPTIONAL:-}suffix"}`), func(string) (string, bool) { return "", false })
	if err != nil || string(resolved) != `{"value":"prefixsuffix"}` {
		t.Fatalf("empty default: %s %v", resolved, err)
	}
}

func TestResolveEscapedReference(t *testing.T) {
	resolved, err := Resolve([]byte(`{"token":"\u0024{TOKEN}"}`), func(name string) (string, bool) { return "value", name == "TOKEN" })
	if err != nil || string(resolved) != `{"token":"value"}` {
		t.Fatalf("escaped reference: %s %v", resolved, err)
	}
}

func TestResolveCollectsOnlyUsedCredentialValues(t *testing.T) {
	lookup := func(name string) (string, bool) {
		switch name {
		case "TOKEN":
			return "used-token", true
		case "UNRELATED":
			return "not-for-this-task", true
		default:
			return "", false
		}
	}
	_, values, err := ResolveWithSecrets([]byte(`{"authorization":"Bearer ${TOKEN}","again":"${TOKEN}","fallback":"${MISSING:-default-secret}","empty":"${EMPTY:-}"}`), lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != "default-secret" || values[1] != "used-token" {
		t.Fatalf("incorrect redaction values: %v", values)
	}
	r := NewRedactor(values)
	if r.String("connector rejected used-token") != "connector rejected [REDACTED]" {
		t.Fatal("did not capture credential embedded in config string")
	}
	result, values, err := ResolveWithSecrets([]byte(`["${TOKEN}","${MISSING}"]`), lookup)
	if err == nil || result != nil || values != nil {
		t.Fatal("failed resolution returned partial secret values")
	}
}
