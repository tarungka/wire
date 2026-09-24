package coordinator

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthRejectsUnusableBcryptHashes(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	original := string(hash)
	cases := []string{original[:7] + "!" + original[8:], original[:29] + "!" + original[30:], original + "x", original[:len(original)-1], strings.Replace(original, "$2a$", "$2z$", 1)}
	for _, hash := range cases {
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			t.Fatalf("fixture must pass header-only validation: %v", err)
		}
		data, err := json.Marshal(map[string]any{"users": []apiUser{{Username: "admin", Role: "admin", PasswordHash: hash}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readAPIAuth(bytes.NewReader(data)); err == nil {
			t.Fatal("installed an unusable password credential")
		}
	}
	for _, version := range []string{"2", "2a", "2b", "2y"} {
		if !validBcryptEncoding(strings.Replace(original, "$2a$", "$"+version+"$", 1)) {
			t.Fatalf("rejected supported bcrypt version %s", version)
		}
	}
}

func TestUnknownBasicUserCannotAuthenticateWithFallbackPassword(t *testing.T) {
	low, err := bcrypt.GenerateFromPassword([]byte("low-password"), 10)
	if err != nil {
		t.Fatal(err)
	}
	high, err := bcrypt.GenerateFromPassword([]byte("high-password"), 11)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"users": []apiUser{{Username: "low", Role: "viewer", PasswordHash: string(low)}, {Username: "high", Role: "admin", PasswordHash: string(high)}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := readAPIAuth(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.dummyHash, high) {
		t.Fatal("unknown-user checks do not use highest configured bcrypt cost")
	}
	for _, tc := range []struct {
		name, password string
		allowed        bool
	}{{"missing", "high-password", false}, {"missing", "low-password", false}, {"low", "low-password", true}, {"high", "high-password", true}, {"low", "wrong", false}} {
		request := httptest.NewRequest("GET", "/api/v1/jobs", nil)
		request.SetBasicAuth(tc.name, tc.password)
		if _, ok := a.user(request); ok != tc.allowed {
			t.Fatalf("unexpected authentication result for %s", tc.name)
		}
	}
}
