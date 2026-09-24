package coordinator

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// Keep the authorization acceptance inventory in sync with actual routes.
//
//go:embed http.go
var registeredHTTPSource string

func TestHTTPSRolesCoverRegisteredRoutes(t *testing.T) {
	// 0=public, 1=viewer, 2=operator, 3=admin. The API intentionally reserves
	// savepoint inspection for operators, while checkpoint inspection is readable.
	routes := map[string]int{
		"GET /healthz": 0, "GET /readyz": 0,
		"GET /api/v1/cluster/leader": 1, "GET /api/v1/cluster": 1,
		"DELETE /api/v1/cluster/nodes/{node_id}": 3,
		"POST /api/v1/jobs":                      2, "POST /api/v1/jobs/submit": 2,
		"GET /api/v1/jobs": 1, "GET /api/v1/jobs/{job_id}": 1,
		"POST /api/v1/jobs/{job_id}/cancel": 2, "POST /api/v1/jobs/{job_id}/pause": 2,
		"POST /api/v1/jobs/{job_id}/resume": 2, "POST /api/v1/jobs/{job_id}/rescale": 2,
		"POST /api/v1/jobs/{job_id}/checkpoints": 2, "GET /api/v1/jobs/{job_id}/checkpoints/{checkpoint_id}": 1,
		"POST /api/v1/jobs/{job_id}/savepoints": 2, "GET /api/v1/jobs/{job_id}/savepoints": 2,
		"GET /api/v1/jobs/{job_id}/savepoints/{savepoint_id}": 2, "DELETE /api/v1/jobs/{job_id}/savepoints/{savepoint_id}": 2,
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "http.go", registeredHTTPSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || method.Sel.Name != "HandleFunc" || len(call.Args) == 0 {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			t.Fatal("nonliteral route needs authorization inventory")
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := routes[pattern]; !ok {
			t.Errorf("registered route missing role acceptance case: %s", pattern)
		}
		found[pattern] = true
		return true
	})
	for route := range routes {
		if !found[route] {
			t.Errorf("acceptance case has no registered route: %s", route)
		}
	}
	if t.Failed() {
		return
	}
	c, _ := newTestCoordinator(t)
	s := NewHTTPServer(c, "", zerolog.Nop())
	original := s.server.Handler
	a, err := readAPIAuth(strings.NewReader(`{"users":[
 {"username":"viewer","role":"viewer","api_key":"wk_live_vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"},
 {"username":"operator","role":"operator","api_key":"wk_live_oooooooooooooooooooooooooooooooo"},
 {"username":"admin","role":"admin","api_key":"wk_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(s.authenticate(a, original))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	replacements := strings.NewReplacer("{job_id}", "missing-job", "{node_id}", "missing-node", "{checkpoint_id}", "1", "{savepoint_id}", "missing-savepoint")
	for pattern, minimum := range routes {
		method, path, _ := strings.Cut(pattern, " ")
		path = replacements.Replace(path)
		methods := []string{method}
		if method == http.MethodGet {
			methods = append(methods, http.MethodHead)
		}
		for _, method := range methods {
			baseline := httptest.NewRecorder()
			original.ServeHTTP(baseline, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
			for rank, role := range []string{"anonymous", "viewer", "operator", "admin"} {
				t.Run(role+"/"+method+path, func(t *testing.T) {
					// Admission itself has independent concurrency/rate tests. Reset the
					// bucket between these sequential role cases, not during requests.
					a.mu.Lock()
					a.tokens = 20
					a.last = time.Now()
					a.mu.Unlock()
					req, err := http.NewRequest(method, server.URL+path, strings.NewReader(`{}`))
					if err != nil {
						t.Fatal(err)
					}
					if rank > 0 {
						req.Header.Set("Authorization", "Bearer wk_live_"+strings.Repeat(role[:1], 32))
					}
					response, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					_, _ = io.Copy(io.Discard, response.Body)
					want := baseline.Code
					if minimum > 0 && rank == 0 {
						want = http.StatusUnauthorized
					} else if rank < minimum {
						want = http.StatusForbidden
					}
					if response.StatusCode != want {
						t.Fatalf("status=%d want=%d (unrestricted endpoint=%d)", response.StatusCode, want, baseline.Code)
					}
				})
			}
		}
	}
	// ServeMux unescapes path segments and redirects canonical paths. Verify
	// authorization cannot be bypassed by alternate spelling or redirect hops.
	for _, tc := range []struct {
		method, path, role string
		status             int
	}{
		{"GET", "/api/v1/jobs/missing-job/%73avepoints", "viewer", http.StatusForbidden},
		{"GET", "/api/v1/jobs%2Fmissing-job%2Fsavepoints", "viewer", http.StatusForbidden},
		{"DELETE", "/api/v1/%63luster/nodes/missing-node", "operator", http.StatusForbidden},
		{"GET", "/%68ealthz", "anonymous", http.StatusOK},
		{"DELETE", "/api/v1/jobs/../cluster/nodes/missing-node", "operator", http.StatusMovedPermanently},
	} {
		t.Run("encoded/"+tc.role+tc.path, func(t *testing.T) {
			a.mu.Lock()
			a.tokens = 20
			a.last = time.Now()
			a.mu.Unlock()
			req, err := http.NewRequest(tc.method, server.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.role != "anonymous" {
				req.Header.Set("Authorization", "Bearer wk_live_"+strings.Repeat(tc.role[:1], 32))
			}
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("encoded path status=%d want=%d", response.StatusCode, tc.status)
			}
			if tc.status == http.StatusMovedPermanently {
				location, err := response.Location()
				if err != nil {
					t.Fatal(err)
				}
				next, err := http.NewRequest(tc.method, location.String(), nil)
				if err != nil {
					t.Fatal(err)
				}
				next.Header = req.Header.Clone()
				redirected, err := client.Do(next)
				if err != nil {
					t.Fatal(err)
				}
				defer redirected.Body.Close()
				if redirected.StatusCode != http.StatusForbidden {
					t.Fatalf("redirect bypassed cluster authorization: %d", redirected.StatusCode)
				}
			}
		})
	}

}
