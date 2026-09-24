// Package secretconfig resolves connector configuration without changing the
// unresolved bytes that belong in durable job metadata.
package secretconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Resolve returns an independent configuration with environment references in
// JSON string values expanded. Configurations without references remain opaque.
// Lookup must be a coordinator-owned environment snapshot. Neither errors nor
// this package's API expose resolved values separately from the returned bytes.
// Callers must keep the result out of metadata, logs and API responses.
func Resolve(raw []byte, lookup func(string) (string, bool)) ([]byte, error) {
	return resolve(raw, lookup, nil)
}

// ResolveWithSecrets also returns the exact substituted values for runtime-only
// redaction. This includes defaults and excludes unrelated environment entries.
// Neither the returned configuration nor this list belongs in durable metadata.
func ResolveWithSecrets(raw []byte, lookup func(string) (string, bool)) ([]byte, []string, error) {
	values := make(map[string]bool)
	result, err := resolve(raw, lookup, func(value string) {
		if value != "" {
			values[value] = true
		}
	})
	if err != nil {
		return nil, nil, err
	}
	secrets := make([]string, 0, len(values))
	for value := range values {
		secrets = append(secrets, value)
	}
	sort.Strings(secrets)
	return result, secrets, nil
}

func resolve(raw []byte, lookup func(string) (string, bool), record func(string)) ([]byte, error) {
	hasRawReference := bytes.Contains(raw, []byte("${"))
	if !json.Valid(raw) && !hasRawReference {
		return bytes.Clone(raw), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, errors.New("secret references require valid JSON configuration")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("secret configuration must contain one JSON value")
	}
	changed := false
	var walk func(any) (any, error)
	walk = func(v any) (any, error) {
		switch v := v.(type) {
		case string:
			if strings.Contains(v, "${") {
				changed = true
			}
			return expand(v, lookup, record)
		case []any:
			for i := range v {
				resolved, err := walk(v[i])
				if err != nil {
					return nil, err
				}
				v[i] = resolved
			}
		case map[string]any:
			for k, child := range v {
				if strings.Contains(k, "${") {
					return nil, errors.New("secret references are not allowed in configuration keys")
				}
				resolved, err := walk(child)
				if err != nil {
					return nil, err
				}
				v[k] = resolved
			}
		}
		return v, nil
	}
	resolved, err := walk(value)
	if err != nil {
		return nil, err
	}
	if !changed {
		return bytes.Clone(raw), nil
	}
	result, err := json.Marshal(resolved)
	if err != nil {
		return nil, errors.New("cannot encode resolved secret configuration")
	}
	return result, nil
}

func expand(input string, lookup func(string) (string, bool), record func(string)) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(input, "${")
		if start < 0 {
			out.WriteString(input)
			return out.String(), nil
		}
		out.WriteString(input[:start])
		input = input[start+2:]
		end := strings.IndexByte(input, '}')
		if end < 0 {
			return "", errors.New("unterminated environment reference")
		}
		expression := input[:end]
		input = input[end+1:]
		name, fallback, hasDefault := strings.Cut(expression, ":-")
		if !validName(name) || strings.Contains(fallback, "${") {
			return "", errors.New("invalid environment reference")
		}
		value, present := lookup(name)
		if !present {
			if !hasDefault {
				return "", fmt.Errorf("environment variable %s not set", name)
			}
			value = fallback
		}
		if record != nil {
			record(value)
		}
		// Values are literal; never recursively interpret references from a secret.
		out.WriteString(value)
	}
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}
