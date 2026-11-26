// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type stubFetcher struct {
	secrets map[string]map[string]any
	calls   map[string]int
	failOn  map[string]error
}

func (s *stubFetcher) Fetch(path string) (map[string]any, error) {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[path]++
	if err, ok := s.failOn[path]; ok {
		return nil, err
	}
	v, ok := s.secrets[path]
	if !ok {
		return nil, errors.New("404 not found")
	}
	return v, nil
}

func TestResolveVaultURIs_KV2(t *testing.T) {
	fetcher := &stubFetcher{
		secrets: map[string]map[string]any{
			"kv/data/myapp/google": {
				"data": map[string]any{
					"data": map[string]any{
						"search_api_key": "AIza-real",
						"cse_id":         "0123:abc",
					},
				},
			},
		},
	}
	in := `google_search_api_key: vault://kv/data/myapp/google#search_api_key
google_cse_id: "vault://kv/data/myapp/google#cse_id"`
	out, errs := ResolveVaultURIs(in, fetcher)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if !strings.Contains(out, "google_search_api_key: AIza-real") {
		t.Errorf("api key not substituted:\n%s", out)
	}
	if !strings.Contains(out, `google_cse_id: "0123:abc"`) {
		t.Errorf("cse id not substituted (must respect surrounding quotes):\n%s", out)
	}
	if fetcher.calls["kv/data/myapp/google"] != 1 {
		t.Errorf("per-call cache should fetch path once, got %d", fetcher.calls["kv/data/myapp/google"])
	}
}

func TestResolveVaultURIs_KV1Fallback(t *testing.T) {
	fetcher := &stubFetcher{
		secrets: map[string]map[string]any{
			"secret/legacy": {
				"data": map[string]any{
					"token": "xoxb-legacy",
				},
			},
		},
	}
	out, errs := ResolveVaultURIs(`tok: vault://secret/legacy#token`, fetcher)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if !strings.Contains(out, "tok: xoxb-legacy") {
		t.Errorf("KV v1 fallback failed:\n%s", out)
	}
}

func TestResolveVaultURIs_MissingField(t *testing.T) {
	fetcher := &stubFetcher{
		secrets: map[string]map[string]any{
			"kv/data/x": {
				"data": map[string]any{
					"data": map[string]any{"only_key": "v"},
				},
			},
		},
	}
	out, errs := ResolveVaultURIs(`k: vault://kv/data/x#nope`, fetcher)
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d (%+v)", len(errs), errs)
	}
	if !strings.Contains(errs[0].Err.Error(), `"nope" not found`) {
		t.Errorf("error text: %v", errs[0])
	}
	if !strings.Contains(out, "vault://kv/data/x#nope") {
		t.Errorf("unresolved URI should be left in place:\n%s", out)
	}
}

func TestResolveVaultURIs_MalformedURI(t *testing.T) {
	fetcher := &stubFetcher{secrets: map[string]map[string]any{}}
	tests := []string{
		`a: vault://nofield`,
		`b: vault://#bare-field`,
		`c: vault://path#`,
	}
	for _, in := range tests {
		_, errs := ResolveVaultURIs(in, fetcher)
		if len(errs) != 1 {
			t.Errorf("%q: want 1 malformed error, got %d (%+v)", in, len(errs), errs)
			continue
		}
		if !strings.Contains(errs[0].Err.Error(), "malformed vault URI") {
			t.Errorf("%q: wrong error: %v", in, errs[0].Err)
		}
	}
}

func TestResolveVaultURIs_NilFetcher(t *testing.T) {
	out, errs := ResolveVaultURIs(`k: vault://kv/data/x#field`, nil)
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Err.Error(), "not configured") {
		t.Errorf("err = %v, want 'not configured'", errs[0].Err)
	}
	if !strings.Contains(out, "vault://kv/data/x#field") {
		t.Errorf("unresolved URI should be preserved in output")
	}
}

func TestResolveVaultURIs_DedupErrors(t *testing.T) {
	fetcher := &stubFetcher{}
	in := `a: vault://bad
b: vault://bad
c: vault://bad`
	_, errs := ResolveVaultURIs(in, fetcher)
	if len(errs) != 1 {
		t.Errorf("expected dedup to collapse 3 identical errors to 1, got %d: %+v", len(errs), errs)
	}
}

func TestResolveVaultURIs_AggregatesAcrossPaths(t *testing.T) {
	fetcher := &stubFetcher{
		failOn: map[string]error{
			"kv/data/a": errors.New("HTTP 403"),
			"kv/data/b": errors.New("HTTP 404"),
		},
	}
	in := `x: vault://kv/data/a#k
y: vault://kv/data/b#k`
	_, errs := ResolveVaultURIs(in, fetcher)
	if len(errs) != 2 {
		t.Fatalf("want 2 errors, got %d (%+v)", len(errs), errs)
	}
	if errs[0].Ref.Path != "kv/data/a" || errs[1].Ref.Path != "kv/data/b" {
		t.Errorf("not sorted by URI: %+v", errs)
	}
}

func TestDefaultVaultFetcher_HTTPRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/kv/data/myapp/google", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Vault-Token"); got != "test-token" {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data": map[string]any{"search_api_key": "AIza-real"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &DefaultVaultFetcher{Addr: srv.URL, Token: "test-token", Client: srv.Client()}
	out, errs := ResolveVaultURIs(
		`key: vault://kv/data/myapp/google#search_api_key`,
		f,
	)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if !strings.Contains(out, "key: AIza-real") {
		t.Errorf("substitution failed:\n%s", out)
	}
}

func TestDefaultVaultFetcher_PassesThroughHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/kv/data/missing", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":["permission denied"]}`, http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &DefaultVaultFetcher{Addr: srv.URL, Token: "t", Client: srv.Client()}
	_, errs := ResolveVaultURIs(`k: vault://kv/data/missing#f`, f)
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Err.Error(), "403") || !strings.Contains(errs[0].Err.Error(), "permission denied") {
		t.Errorf("HTTP error not surfaced: %v", errs[0].Err)
	}
}

func TestNewDefaultVaultFetcher_RequiresAddr(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("VAULT_TOKEN", "stub")
	if _, err := NewDefaultVaultFetcher(); err == nil || !strings.Contains(err.Error(), "VAULT_ADDR") {
		t.Errorf("err = %v, want VAULT_ADDR missing", err)
	}
}

func TestDiscoverVaultToken_TokenFile(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".vault-token"), []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverVaultToken()
	if err != nil {
		t.Fatalf("DiscoverVaultToken: %v", err)
	}
	if got != "file-token" {
		t.Errorf("got %q, want %q", got, "file-token")
	}
}

func TestDiscoverVaultToken_EnvWinsOverFile(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "env-token")
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.WriteFile(filepath.Join(home, ".vault-token"), []byte("file-token"), 0o600)
	got, err := DiscoverVaultToken()
	if err != nil {
		t.Fatalf("DiscoverVaultToken: %v", err)
	}
	if got != "env-token" {
		t.Errorf("got %q, want env-token", got)
	}
}

func TestDiscoverVaultToken_NoneAvailable(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := DiscoverVaultToken(); err == nil || !strings.Contains(err.Error(), "vault login") {
		t.Errorf("err = %v, want guidance to run `vault login`", err)
	}
}

func TestEnvVaultFetcher_SurfacesConstructErrorThroughResolve(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	_, errs := ResolveVaultURIs(`x: vault://p#k`, EnvVaultFetcher{})
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Err.Error(), "VAULT_ADDR") {
		t.Errorf("err = %v, want VAULT_ADDR guidance", errs[0].Err)
	}
}

func TestResolveVaultURIs_EmptyFieldIsReported(t *testing.T) {
	f := &stubFetcher{secrets: map[string]map[string]any{
		"p": {"data": map[string]any{"data": map[string]any{"k": ""}}},
	}}
	out, errs := ResolveVaultURIs(`api_key: vault://p#k`, f)
	if len(errs) != 1 || !strings.Contains(errs[0].Err.Error(), "empty") {
		t.Fatalf("errs = %+v, want the empty field reported", errs)
	}
	if !strings.Contains(out, "vault://p#k") {
		t.Errorf("out = %q, want the reference left literal rather than blanked", out)
	}
}

func TestEnvVaultFetcher_SurfacesConstructError(t *testing.T) {
	t.Setenv("VAULT_ADDR", "")
	if _, err := (EnvVaultFetcher{}).Fetch("p"); err == nil || !strings.Contains(err.Error(), "VAULT_ADDR") {
		t.Errorf("err = %v, want VAULT_ADDR guidance", err)
	}
}

func TestEnvVaultFetcher_RereadsRotatedToken(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Vault-Token"))
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"data": map[string]any{"k": "v"}}})
	}))
	defer srv.Close()

	t.Setenv("VAULT_ADDR", srv.URL)
	t.Setenv("VAULT_TOKEN", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	tokenPath := filepath.Join(home, ".vault-token")

	f := EnvVaultFetcher{}
	if err := os.WriteFile(tokenPath, []byte("first-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Fetch("p"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if err := os.WriteFile(tokenPath, []byte("rotated-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Fetch("p"); err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	want := []string{"first-token", "rotated-token"}
	if !slices.Equal(seen, want) {
		t.Errorf("tokens sent = %v, want %v: the rotated token must be picked up "+
			"without restarting the process", seen, want)
	}
}
