// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseResourceMetadataURL(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"empty header", "", ""},
		{"no resource_metadata", `Bearer realm="example"`, ""},
		{"quoted URL", `Bearer resource_metadata="https://auth.example.com/.well-known/oauth"`, "https://auth.example.com/.well-known/oauth"},
		{"unquoted URL", `Bearer resource_metadata=https://auth.example.com/.well-known/oauth`, "https://auth.example.com/.well-known/oauth"},
		{"unquoted with comma", `Bearer resource_metadata=https://auth.example.com/meta, error="insufficient_scope"`, "https://auth.example.com/meta"},
		{"unquoted with space", `Bearer resource_metadata=https://auth.example.com/meta error="insufficient_scope"`, "https://auth.example.com/meta"},
		{"case insensitive key", `Bearer Resource_Metadata="https://auth.example.com/meta"`, "https://auth.example.com/meta"},
		{"unterminated quote", `Bearer resource_metadata="https://auth.example.com/meta`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResourceMetadataURL(tt.header)
			if got != tt.want {
				t.Errorf("ParseResourceMetadataURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildWellKnownURLs(t *testing.T) {
	assertURLs := func(t *testing.T, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("len(urls) = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("urls[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	}

	t.Run("resource only, root — nil authServer reuses resource, URL 6 deduped", func(t *testing.T) {
		res, _ := url.Parse("https://mcp.example.com")
		got := BuildWellKnownURLs(nil, res)
		assertURLs(t, got, []string{
			"https://mcp.example.com/.well-known/oauth-authorization-server",
			"https://mcp.example.com/.well-known/openid-configuration",
		})
	})

	t.Run("resource only, path-aware — 4 URLs (2,3,4,5); URL 6 deduped", func(t *testing.T) {
		res, _ := url.Parse("https://mcp.example.com/tenant/a")
		got := BuildWellKnownURLs(nil, res)
		assertURLs(t, got, []string{
			"https://mcp.example.com/.well-known/oauth-authorization-server/tenant/a",
			"https://mcp.example.com/.well-known/oauth-authorization-server",
			"https://mcp.example.com/.well-known/openid-configuration/tenant/a",
			"https://mcp.example.com/.well-known/openid-configuration",
		})
	})

	t.Run("differing authServer and resource, both root — 3 URLs (3,5,6)", func(t *testing.T) {
		as, _ := url.Parse("https://auth.example.com")
		res, _ := url.Parse("https://mcp.example.com")
		got := BuildWellKnownURLs(as, res)
		assertURLs(t, got, []string{
			"https://auth.example.com/.well-known/oauth-authorization-server",
			"https://auth.example.com/.well-known/openid-configuration",
			"https://mcp.example.com/.well-known/openid-configuration",
		})
	})

	t.Run("differing, authServer path-aware — 5 URLs (2,3,4,5,6)", func(t *testing.T) {
		as, _ := url.Parse("https://auth.example.com/tenant1")
		res, _ := url.Parse("https://mcp.example.com/v1")
		got := BuildWellKnownURLs(as, res)
		assertURLs(t, got, []string{
			"https://auth.example.com/.well-known/oauth-authorization-server/tenant1",
			"https://auth.example.com/.well-known/oauth-authorization-server",
			"https://auth.example.com/.well-known/openid-configuration/tenant1",
			"https://auth.example.com/.well-known/openid-configuration",
			"https://mcp.example.com/.well-known/openid-configuration",
		})
	})

	t.Run("same origin authServer and resource — URL 6 deduped", func(t *testing.T) {
		same, _ := url.Parse("https://combined.example.com")
		got := BuildWellKnownURLs(same, same)
		assertURLs(t, got, []string{
			"https://combined.example.com/.well-known/oauth-authorization-server",
			"https://combined.example.com/.well-known/openid-configuration",
		})
	})

	t.Run("trailing slash stripped from authServer path", func(t *testing.T) {
		as, _ := url.Parse("https://auth.example.com/api/")
		res, _ := url.Parse("https://mcp.example.com")
		got := BuildWellKnownURLs(as, res)
		assertURLs(t, got, []string{
			"https://auth.example.com/.well-known/oauth-authorization-server/api",
			"https://auth.example.com/.well-known/oauth-authorization-server",
			"https://auth.example.com/.well-known/openid-configuration/api",
			"https://auth.example.com/.well-known/openid-configuration",
			"https://mcp.example.com/.well-known/openid-configuration",
		})
	})

	t.Run("root path explicitly / is treated as empty", func(t *testing.T) {
		as, _ := url.Parse("https://auth.example.com/")
		res, _ := url.Parse("https://mcp.example.com")
		got := BuildWellKnownURLs(as, res)
		assertURLs(t, got, []string{
			"https://auth.example.com/.well-known/oauth-authorization-server",
			"https://auth.example.com/.well-known/openid-configuration",
			"https://mcp.example.com/.well-known/openid-configuration",
		})
	})
}

func TestOAuthMetadata_IsExpired(t *testing.T) {
	t.Run("nil metadata", func(t *testing.T) {
		var m *OAuthMetadata
		if !m.IsExpired() {
			t.Error("nil metadata should be expired")
		}
	})

	t.Run("fresh metadata", func(t *testing.T) {
		m := &OAuthMetadata{DiscoveredAt: time.Now()}
		if m.IsExpired() {
			t.Error("freshly discovered metadata should not be expired")
		}
	})

	t.Run("old metadata", func(t *testing.T) {
		m := &OAuthMetadata{DiscoveredAt: time.Now().Add(-25 * time.Hour)}
		if !m.IsExpired() {
			t.Error("metadata older than 24h should be expired")
		}
	})
}

func TestDiscoverOAuthMetadata(t *testing.T) {
	clearDiscoveryCache := func() {
		DiscoveryCacheMu.Lock()
		DiscoveryCache = make(map[string]*OAuthMetadata)
		DiscoveryCacheMu.Unlock()
	}

	t.Run("well-known fallback: no challenge → construct PRM URL", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer":                 "https://auth.example.com",
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		resSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-protected-resource" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"resource":              r.Host,
					"authorization_servers": []string{authSrv.URL},
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(t.Context(), resSrv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta == nil {
			t.Fatal("expected metadata from well-known fallback")
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
		if meta.TokenEndpoint != "https://auth.example.com/token" {
			t.Errorf("TokenEndpoint = %q", meta.TokenEndpoint)
		}
	})

	t.Run("challenge is followed verbatim to a non-canonical PRM URL", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		const nonCanonicalPRMPath = "/tenant-7/oauth-metadata"
		var challengeHit atomic.Bool

		resMux := http.NewServeMux()
		resMux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+nonCanonicalPRMPath+`"`)
			w.WriteHeader(401)
		})
		resMux.HandleFunc(nonCanonicalPRMPath, func(w http.ResponseWriter, _ *http.Request) {
			challengeHit.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "https://mcp.example.com/mcp",
				"authorization_servers": []string{authSrv.URL},
			})
		})
		resMux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
			t.Error("canonical well-known PRM path was probed; challenge URL was not followed verbatim")
			w.WriteHeader(500)
		})
		resSrv := httptest.NewServer(resMux)
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(t.Context(), resSrv.URL+"/mcp")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !challengeHit.Load() {
			t.Fatal("non-canonical PRM URL from the challenge was never fetched")
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
		if meta.Resource != "https://mcp.example.com/mcp" {
			t.Errorf("meta.Resource = %q, want the PRM resource from the challenge document", meta.Resource)
		}
	})

	t.Run("POST elicits the challenge where a bare GET does not", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		var challengeMethod atomic.Value
		challengeMethod.Store("")

		resMux := http.NewServeMux()
		resMux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				challengeMethod.Store(http.MethodPost)
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/prm"`)
				w.WriteHeader(401)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
		})
		resMux.HandleFunc("/prm", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "https://mcp.example.com/mcp",
				"authorization_servers": []string{authSrv.URL},
			})
		})
		resSrv := httptest.NewServer(resMux)
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(t.Context(), resSrv.URL+"/mcp")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if challengeMethod.Load().(string) != http.MethodPost {
			t.Errorf("challenge was not drawn by POST; a GET-only probe would miss it")
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
		if meta.Resource != "https://mcp.example.com/mcp" {
			t.Errorf("meta.Resource = %q, want the challenge PRM resource", meta.Resource)
		}
	})

	t.Run("GET fallback: server that challenges only on GET", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		var challengeMethod atomic.Value
		challengeMethod.Store("")

		resMux := http.NewServeMux()
		resMux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				challengeMethod.Store(http.MethodGet)
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/prm"`)
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		})
		resMux.HandleFunc("/prm", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_servers": []string{authSrv.URL},
			})
		})
		resSrv := httptest.NewServer(resMux)
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(t.Context(), resSrv.URL+"/mcp")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if challengeMethod.Load().(string) != http.MethodGet {
			t.Errorf("challenge was not drawn by the GET fallback")
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
	})

	t.Run("challenge PRM with unparseable authorization_servers[0] → well-known auth server, challenge resource preserved", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		const challengeResource = "https://mcp.example.com/mcp"

		resMux := http.NewServeMux()
		resMux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/challenge-prm"`)
			w.WriteHeader(401)
		})
		resMux.HandleFunc("/challenge-prm", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              challengeResource,
				"authorization_servers": []string{"not-a-url"},
			})
		})
		resMux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_servers": []string{authSrv.URL},
			})
		})
		resSrv := httptest.NewServer(resMux)
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(t.Context(), resSrv.URL+"/mcp")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q, want the well-known auth server", meta.AuthorizationEndpoint)
		}
		if meta.Resource != challengeResource {
			t.Errorf("meta.Resource = %q, want the challenge-captured resource %q", meta.Resource, challengeResource)
		}
	})

	t.Run("Bearer challenge found among multiple WWW-Authenticate headers", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		resMux := http.NewServeMux()
		resMux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("WWW-Authenticate", `Basic realm="mcp"`)
			w.Header().Add("WWW-Authenticate", `Bearer resource_metadata="http://`+r.Host+`/prm"`)
			w.WriteHeader(401)
		})
		resMux.HandleFunc("/prm", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_servers": []string{authSrv.URL},
			})
		})
		resSrv := httptest.NewServer(resMux)
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(t.Context(), resSrv.URL+"/mcp")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q; Bearer challenge behind Basic was missed", meta.AuthorizationEndpoint)
		}
	})

	t.Run("relative resource_metadata hint is resolved against the resource URL", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		resMux := http.NewServeMux()
		resMux.HandleFunc("/mcp", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="/tenant/prm"`)
			w.WriteHeader(401)
		})
		resMux.HandleFunc("/tenant/prm", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_servers": []string{authSrv.URL},
			})
		})
		resSrv := httptest.NewServer(resMux)
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(t.Context(), resSrv.URL+"/mcp")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q; relative resource_metadata was not resolved", meta.AuthorizationEndpoint)
		}
	})

	t.Run("captures RFC 9728 resource identifier for RFC 8707 token binding", func(t *testing.T) {
		clearDiscoveryCache()

		const declaredResource = "https://mcp.example.com/canonical"

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer":                 "https://auth.example.com",
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		resSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-protected-resource" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"resource":              declaredResource,
					"authorization_servers": []string{authSrv.URL},
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(context.Background(), resSrv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta.Resource != declaredResource {
			t.Errorf("meta.Resource = %q, want %q", meta.Resource, declaredResource)
		}
	})

	t.Run("step A: authServer with path → path-aware well-known hit first", func(t *testing.T) {
		clearDiscoveryCache()

		var pathAwareHit atomic.Bool
		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server/tenant1" {
				pathAwareHit.Store(true)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/tenant1/authorize",
					"token_endpoint":         "https://auth.example.com/tenant1/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		resSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-protected-resource" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"resource":              r.Host,
					"authorization_servers": []string{authSrv.URL + "/tenant1"},
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(context.Background(), resSrv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !pathAwareHit.Load() {
			t.Error("expected path-aware well-known URL to be the successful probe")
		}
		if meta.AuthorizationEndpoint != "https://auth.example.com/tenant1/authorize" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
	})

	t.Run("step A URL inserts well-known between host and path (RFC 9728 §3.1)", func(t *testing.T) {
		clearDiscoveryCache()

		var probed atomic.Value
		probed.Store("")

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://auth.example.com/authorize",
					"token_endpoint":         "https://auth.example.com/token",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer authSrv.Close()

		resMux := http.NewServeMux()
		resMux.HandleFunc("/.well-known/oauth-protected-resource/v1/sse", func(w http.ResponseWriter, r *http.Request) {
			probed.Store(r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              r.Host,
				"authorization_servers": []string{authSrv.URL},
			})
		})
		resMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		resSrv := httptest.NewServer(resMux)
		defer resSrv.Close()

		pathResourceURL := resSrv.URL + "/v1/sse"

		meta, err := DiscoverOAuthMetadata(context.Background(), pathResourceURL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta == nil {
			t.Fatal("expected metadata from step A")
		}
		got := probed.Load().(string)
		if got != "/.well-known/oauth-protected-resource/v1/sse" {
			t.Errorf("RM endpoint probed at %q, want RFC-compliant path-aware %q", got, "/.well-known/oauth-protected-resource/v1/sse")
		}
	})

	t.Run("step A fails → WWW-Authenticate resource_metadata hint used", func(t *testing.T) {
		clearDiscoveryCache()

		authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://wwwauth.example.com/authorize",
					"token_endpoint":         "https://wwwauth.example.com/token",
				})
				return
			}
			w.WriteHeader(404)
		}))
		defer authSrv.Close()

		var customRMSrvURL string
		customRMSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "custom",
				"authorization_servers": []string{authSrv.URL},
			})
		}))
		defer customRMSrv.Close()
		customRMSrvURL = customRMSrv.URL

		resSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-protected-resource" {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+customRMSrvURL+`"`)
			w.WriteHeader(401)
		}))
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(context.Background(), resSrv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta == nil {
			t.Fatal("expected metadata from WWW-Authenticate fallback")
		}
		if meta.AuthorizationEndpoint != "https://wwwauth.example.com/authorize" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
		if meta.Resource != "custom" {
			t.Errorf("meta.Resource = %q, want the PRM resource carried via the WWW-Authenticate fallback", meta.Resource)
		}
	})

	t.Run("resource survives an unparseable authorization_servers[0]", func(t *testing.T) {
		clearDiscoveryCache()

		var resSrv *httptest.Server
		resSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/.well-known/oauth-protected-resource":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"resource":              "https://declared.example.com/canonical",
					"authorization_servers": []string{"::not a url"},
				})
			case "/.well-known/oauth-authorization-server":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer":                 resSrv.URL,
					"authorization_endpoint": resSrv.URL + "/authorize",
					"token_endpoint":         resSrv.URL + "/token",
				})
			default:
				w.WriteHeader(404)
			}
		}))
		defer resSrv.Close()

		meta, err := DiscoverOAuthMetadata(context.Background(), resSrv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta.Resource != "https://declared.example.com/canonical" {
			t.Errorf("meta.Resource = %q, want the declared PRM resource to survive the auth-server parse failure", meta.Resource)
		}
	})

	t.Run("step A + WWW-Authenticate both fail → well-known fallback", func(t *testing.T) {
		clearDiscoveryCache()

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
			w.WriteHeader(401)
		})
		mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint": "https://wk.example.com/auth",
				"token_endpoint":         "https://wk.example.com/token",
			})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		meta, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta == nil {
			t.Fatal("expected metadata from well-known fallback")
		}
		if meta.AuthorizationEndpoint != "https://wk.example.com/auth" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
	})

	t.Run("cache hit", func(t *testing.T) {
		clearDiscoveryCache()

		var callCount atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount.Add(1)
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://cached.example.com/auth",
					"token_endpoint":         "https://cached.example.com/token",
				})
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
			w.WriteHeader(401)
		}))
		defer srv.Close()

		_, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("first call error: %v", err)
		}
		firstCallCount := callCount.Load()

		meta, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("second call error: %v", err)
		}
		if got := callCount.Load(); got != firstCallCount {
			t.Errorf("expected cache hit but server received %d extra calls", got-firstCallCount)
		}
		if meta.AuthorizationEndpoint != "https://cached.example.com/auth" {
			t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
		}
	})

	t.Run("cache expired → rediscovers", func(t *testing.T) {
		clearDiscoveryCache()

		var hitCount atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				hitCount.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://srv.example.com/auth",
					"token_endpoint":         "https://srv.example.com/token",
				})
				return
			}
			w.WriteHeader(401)
		}))
		defer srv.Close()

		_, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("first call error: %v", err)
		}
		if got := hitCount.Load(); got != 1 {
			t.Fatalf("expected 1 metadata fetch after first call, got %d", got)
		}

		cacheKey := normaliseCacheKey(srv.URL)
		DiscoveryCacheMu.Lock()
		if cached := DiscoveryCache[cacheKey]; cached != nil {
			cached.DiscoveredAt = time.Now().Add(-2 * discoveryTTL)
		}
		DiscoveryCacheMu.Unlock()

		_, err = DiscoverOAuthMetadata(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("second call error: %v", err)
		}
		if got := hitCount.Load(); got != 2 {
			t.Errorf("expected a fresh fetch after TTL expiry, got %d hits", got)
		}
	})

	t.Run("refuses to cache metadata missing required endpoints", func(t *testing.T) {
		clearDiscoveryCache()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"issuer": "https://bad.example.com",
				})
				return
			}
			w.WriteHeader(401)
		}))
		defer srv.Close()

		_, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error when metadata lacks required endpoints")
		}
		DiscoveryCacheMu.RLock()
		_, cached := DiscoveryCache[srv.URL]
		DiscoveryCacheMu.RUnlock()
		if cached {
			t.Error("must not cache metadata missing required endpoints")
		}
	})

	t.Run("singleflight dedupes concurrent callers across handlers", func(t *testing.T) {
		clearDiscoveryCache()

		var metaHits int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				atomic.AddInt64(&metaHits, 1)
				time.Sleep(50 * time.Millisecond)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://sf.example.com/auth",
					"token_endpoint":         "https://sf.example.com/token",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		const N = 8
		errs := make(chan error, N)
		start := make(chan struct{})
		for range N {
			go func() {
				<-start
				_, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
				errs <- err
			}()
		}
		close(start)
		for range N {
			if err := <-errs; err != nil {
				t.Errorf("concurrent DiscoverOAuthMetadata: %v", err)
			}
		}
		if got := atomic.LoadInt64(&metaHits); got != 1 {
			t.Errorf("expected exactly 1 metadata fetch across %d concurrent callers, got %d", N, got)
		}
	})

	t.Run("leader cancellation does not fail other waiters", func(t *testing.T) {
		clearDiscoveryCache()

		started := make(chan struct{}, 1)
		allow := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/.well-known/oauth-authorization-server" {
				select {
				case started <- struct{}{}:
				default:
				}
				<-allow
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"authorization_endpoint": "https://cancel.example.com/auth",
					"token_endpoint":         "https://cancel.example.com/token",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		leaderCtx, cancelLeader := context.WithCancel(t.Context())
		waiterCtx := t.Context()

		type res struct {
			meta *OAuthMetadata
			err  error
		}
		leaderCh := make(chan res, 1)
		waiterCh := make(chan res, 1)

		go func() {
			m, err := DiscoverOAuthMetadata(leaderCtx, srv.URL)
			leaderCh <- res{m, err}
		}()
		<-started
		go func() {
			m, err := DiscoverOAuthMetadata(waiterCtx, srv.URL)
			waiterCh <- res{m, err}
		}()
		time.Sleep(20 * time.Millisecond)
		cancelLeader()
		close(allow)

		waiterRes := <-waiterCh
		if waiterRes.err != nil {
			t.Fatalf("waiter got error despite having a valid context: %v", waiterRes.err)
		}
		if waiterRes.meta == nil {
			t.Fatal("waiter got nil metadata")
		}
		if waiterRes.meta.AuthorizationEndpoint != "https://cancel.example.com/auth" {
			t.Errorf("waiter metadata wrong: %+v", waiterRes.meta)
		}
		<-leaderCh
	})

	t.Run("all discovery fails → no cache write", func(t *testing.T) {
		clearDiscoveryCache()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
			w.WriteHeader(401)
		}))
		defer srv.Close()

		_, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error when all discovery fails")
		}
		DiscoveryCacheMu.RLock()
		_, cached := DiscoveryCache[srv.URL]
		DiscoveryCacheMu.RUnlock()
		if cached {
			t.Error("must not cache when discovery fails")
		}
	})
}

func TestProbeWWWAuthenticate_DoesNotDrainBody(t *testing.T) {
	bodyStarted := make(chan struct{})
	unblockServer := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://rm.example.com"`)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusUnauthorized)
		close(bodyStarted)
		flusher, _ := w.(http.Flusher)
		tick := time.NewTicker(1 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-unblockServer:
				return
			case <-r.Context().Done():
				return
			case <-tick.C:
				_, _ = w.Write([]byte(": keep-alive\n\n"))
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}))
	defer srv.Close()
	defer close(unblockServer)

	resource, _ := url.Parse(srv.URL)

	done := make(chan string, 1)
	go func() {
		done <- probeWWWAuthenticate(context.Background(), resource)
	}()

	select {
	case hint := <-done:
		if hint != "https://rm.example.com" {
			t.Errorf("hint = %q, want 'https://rm.example.com'", hint)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("probeWWWAuthenticate blocked reading a streaming body — it should have returned as soon as headers were available")
	}
	<-bodyStarted
}

func TestFetchMetadata(t *testing.T) {
	t.Run("valid metadata", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 "https://auth.example.com",
				"authorization_endpoint": "https://auth.example.com/auth",
				"token_endpoint":         "https://auth.example.com/token",
				"scopes_supported":       []string{"read", "write"},
			})
		}))
		defer srv.Close()

		meta, err := fetchMetadata(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if meta.Issuer != "https://auth.example.com" {
			t.Errorf("Issuer = %q", meta.Issuer)
		}
		if len(meta.ScopesSupported) != 2 {
			t.Errorf("ScopesSupported = %v", meta.ScopesSupported)
		}
	})

	t.Run("missing required endpoints", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": "https://auth.example.com",
			})
		}))
		defer srv.Close()

		_, err := fetchMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error for missing endpoints")
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(404)
		}))
		defer srv.Close()

		_, err := fetchMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error for 404")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{invalid`))
		}))
		defer srv.Close()

		_, err := fetchMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

func TestFetchResourceMetadata(t *testing.T) {
	t.Run("valid metadata", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "https://mcp.example.com",
				"authorization_servers": []string{"https://auth.example.com"},
			})
		}))
		defer srv.Close()

		rm, err := fetchResourceMetadata(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if rm.Resource != "https://mcp.example.com" {
			t.Errorf("Resource = %q", rm.Resource)
		}
		if len(rm.AuthorizationServers) != 1 || rm.AuthorizationServers[0] != "https://auth.example.com" {
			t.Errorf("AuthorizationServers = %v", rm.AuthorizationServers)
		}
	})

	t.Run("missing authorization_servers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource": "https://mcp.example.com",
			})
		}))
		defer srv.Close()

		_, err := fetchResourceMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error when authorization_servers is absent")
		}
	})

	t.Run("empty authorization_servers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "https://mcp.example.com",
				"authorization_servers": []string{},
			})
		}))
		defer srv.Close()

		_, err := fetchResourceMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error when authorization_servers is empty")
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		_, err := fetchResourceMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error for 404")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{not json`))
		}))
		defer srv.Close()

		_, err := fetchResourceMetadata(context.Background(), srv.URL)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

func TestRegisterClient(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client_name"] != "Solace Agent Mesh" {
				t.Errorf("client_name = %v", body["client_name"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  "reg-cid",
				"client_secret":              "reg-secret",
				"token_endpoint_auth_method": "client_secret_post",
			})
		}))
		defer srv.Close()

		reg, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", nil)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if reg.ClientID != "reg-cid" {
			t.Errorf("ClientID = %q", reg.ClientID)
		}
		if reg.ClientSecret != "reg-secret" {
			t.Errorf("ClientSecret = %q", reg.ClientSecret)
		}
	})

	t.Run("empty URL falls back to env vars", func(t *testing.T) {
		t.Setenv("TOOL_OAUTH_CLIENT_ID", "env-cid")
		t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "env-secret")

		reg, err := RegisterClient(context.Background(), "", "http://localhost/cb", nil)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if reg.ClientID != "env-cid" {
			t.Errorf("ClientID = %q", reg.ClientID)
		}
		if reg.ClientSecret != "env-secret" {
			t.Errorf("ClientSecret = %q", reg.ClientSecret)
		}
	})

	t.Run("registration error falls back to env", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(500)
		}))
		defer srv.Close()

		t.Setenv("TOOL_OAUTH_CLIENT_ID", "fallback-cid")
		t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "")
		t.Setenv("MCP_CLIENT_ID", "")
		t.Setenv("MCP_CLIENT_SECRET", "")

		TightenDiscoveryRetryForTest(t)

		reg, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", nil)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if reg.ClientID != "fallback-cid" {
			t.Errorf("ClientID = %q", reg.ClientID)
		}
	})

	t.Run("no URL and no env vars", func(t *testing.T) {
		t.Setenv("TOOL_OAUTH_CLIENT_ID", "")
		t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "")
		t.Setenv("MCP_CLIENT_ID", "")
		t.Setenv("MCP_CLIENT_SECRET", "")

		_, err := RegisterClient(context.Background(), "", "http://localhost/cb", nil)
		if err == nil {
			t.Error("expected error when no URL and no env vars")
		}
	})

	t.Run("MCP_CLIENT_ID fallback", func(t *testing.T) {
		t.Setenv("TOOL_OAUTH_CLIENT_ID", "")
		t.Setenv("MCP_CLIENT_ID", "mcp-cid")
		t.Setenv("MCP_CLIENT_SECRET", "mcp-secret")

		reg, err := RegisterClient(context.Background(), "", "http://localhost/cb", nil)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if reg.ClientID != "mcp-cid" {
			t.Errorf("ClientID = %q", reg.ClientID)
		}
		if reg.ClientSecret != "mcp-secret" {
			t.Errorf("ClientSecret = %q", reg.ClientSecret)
		}
	})

	t.Run("preferred auth method from metadata", func(t *testing.T) {
		var receivedMethod string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			receivedMethod, _ = body["token_endpoint_auth_method"].(string)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  "reg-cid",
				"client_secret":              "reg-secret",
				"token_endpoint_auth_method": receivedMethod,
			})
		}))
		defer srv.Close()

		meta := &OAuthMetadata{
			TokenEndpointAuthMethodsSupported: []string{"client_secret_post", "client_secret_basic"},
		}

		reg, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", meta)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if reg.TokenEndpointAuthMethod != "client_secret_post" {
			t.Errorf("TokenEndpointAuthMethod = %q, want client_secret_post", reg.TokenEndpointAuthMethod)
		}
	})
}

func TestRegisterClient_SendsScope(t *testing.T) {
	var gotScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotScope, _ = body["scope"].(string)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "c",
			"client_secret": "s",
		})
	}))
	defer srv.Close()

	_, err := RegisterClientWithConfig(context.Background(), srv.URL+"/register", "http://localhost/cb", nil, RegistrationRequest{
		Scopes: []string{"read", "write"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotScope != "read write" {
		t.Errorf("scope = %q, want %q", gotScope, "read write")
	}
}

func TestRegisterClient_SendsRefreshTokenGrantType(t *testing.T) {
	var gotGrants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if gt, ok := body["grant_types"].([]any); ok {
			for _, v := range gt {
				if s, ok := v.(string); ok {
					gotGrants = append(gotGrants, s)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "c",
			"client_secret": "s",
		})
	}))
	defer srv.Close()

	meta := &OAuthMetadata{
		GrantTypesSupported: []string{"authorization_code", "refresh_token", "client_credentials"},
	}
	_, err := RegisterClientWithConfig(context.Background(), srv.URL+"/register", "http://localhost/cb", meta, RegistrationRequest{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	hasAuthCode := false
	hasRefresh := false
	for _, g := range gotGrants {
		if g == "authorization_code" {
			hasAuthCode = true
		}
		if g == "refresh_token" {
			hasRefresh = true
		}
	}
	if !hasAuthCode {
		t.Error("grant_types missing authorization_code")
	}
	if !hasRefresh {
		t.Errorf("grant_types missing refresh_token; got %v", gotGrants)
	}
}

func TestRegisterClient_OmitsRefreshTokenWhenMetadataRejects(t *testing.T) {
	var gotGrants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if gt, ok := body["grant_types"].([]any); ok {
			for _, v := range gt {
				if s, ok := v.(string); ok {
					gotGrants = append(gotGrants, s)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "c",
			"client_secret": "s",
		})
	}))
	defer srv.Close()

	meta := &OAuthMetadata{
		GrantTypesSupported: []string{"authorization_code"},
	}
	_, err := RegisterClientWithConfig(context.Background(), srv.URL+"/register", "http://localhost/cb", meta, RegistrationRequest{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	for _, g := range gotGrants {
		if g == "refresh_token" {
			t.Errorf("grant_types should not include refresh_token when metadata omits it; got %v", gotGrants)
		}
	}
}

func TestRegisterClient_SendsInitialAccessToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "c",
			"client_secret": "s",
		})
	}))
	defer srv.Close()

	t.Setenv("TOOL_OAUTH_INITIAL_ACCESS_TOKEN", "iat-abc123")

	_, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotAuth != "Bearer iat-abc123" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer iat-abc123")
	}
}

func TestRegisterClient_ExplicitTokenOverridesEnv(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":     "c",
			"client_secret": "s",
		})
	}))
	defer srv.Close()

	t.Setenv("TOOL_OAUTH_INITIAL_ACCESS_TOKEN", "env-token")

	_, _ = RegisterClientWithConfig(context.Background(), srv.URL+"/register", "http://localhost/cb", nil, RegistrationRequest{
		InitialAccessToken: "explicit-token",
	})
	if gotAuth != "Bearer explicit-token" {
		t.Errorf("Authorization header = %q, want Bearer explicit-token", gotAuth)
	}
}

func TestRegisterClient_AuthMethodPreferenceOrder(t *testing.T) {
	tests := []struct {
		supported []string
		want      string
	}{
		{[]string{"client_secret_post", "client_secret_basic"}, "client_secret_post"},
		{[]string{"none", "client_secret_post"}, "none"},
		{[]string{"client_secret_basic", "client_secret_post", "none"}, "client_secret_basic"},
		{[]string{"none"}, "none"},
		{[]string{"private_key_jwt"}, "client_secret_basic"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.supported, "_"), func(t *testing.T) {
			var gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				gotMethod, _ = body["token_endpoint_auth_method"].(string)
				resp := map[string]any{
					"client_id":                  "c",
					"token_endpoint_auth_method": gotMethod,
				}
				if gotMethod != "none" {
					resp["client_secret"] = "s"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(resp)
			}))
			defer srv.Close()

			meta := &OAuthMetadata{TokenEndpointAuthMethodsSupported: tt.supported}
			_, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", meta)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if gotMethod != tt.want {
				t.Errorf("gotMethod = %q, want %q", gotMethod, tt.want)
			}
		})
	}
}

func TestSelectTokenAuthMethod(t *testing.T) {
	tests := []struct {
		name      string
		supported []string
		want      string
	}{
		{"miro lists post first", []string{"client_secret_post", "client_secret_basic"}, "client_secret_post"},
		{"canva lists basic first", []string{"client_secret_basic", "client_secret_post", "none"}, "client_secret_basic"},
		{"datadog post then none", []string{"client_secret_post", "none"}, "client_secret_post"},
		{"none only", []string{"none"}, "none"},
		{"empty defaults basic", nil, "client_secret_basic"},
		{"unsupported only defaults basic", []string{"private_key_jwt", "tls_client_auth"}, "client_secret_basic"},
		{"skips unsupported picks first supported", []string{"private_key_jwt", "none"}, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SelectTokenAuthMethod(tt.supported); got != tt.want {
				t.Errorf("SelectTokenAuthMethod(%v) = %q, want %q", tt.supported, got, tt.want)
			}
		})
	}
}

func TestClientRegistration_IsExpired(t *testing.T) {
	now := time.Now().Unix()
	cases := []struct {
		name string
		reg  *ClientRegistration
		want bool
	}{
		{"nil", nil, false},
		{"from env (never expires)", &ClientRegistration{FromEnv: true, ClientSecretExpiresAt: 0}, false},
		{"expires_at=0 (never expires)", &ClientRegistration{ClientSecretExpiresAt: 0}, false},
		{"expires_at=-1 (sentinel, never)", &ClientRegistration{ClientSecretExpiresAt: -1}, false},
		{"future (far)", &ClientRegistration{ClientSecretExpiresAt: now + 86400}, false},
		{"future within buffer", &ClientRegistration{ClientSecretExpiresAt: now + 30}, true},
		{"past", &ClientRegistration{ClientSecretExpiresAt: now - 10}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.reg.IsExpired(); got != tc.want {
				t.Errorf("IsExpired() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateRegistrationResponse(t *testing.T) {
	tests := []struct {
		name    string
		reg     *ClientRegistration
		wantErr bool
	}{
		{"nil response", nil, true},
		{"missing client_id", &ClientRegistration{}, true},
		{"missing client_secret with basic",
			&ClientRegistration{ClientID: "c", TokenEndpointAuthMethod: "client_secret_basic"},
			true,
		},
		{"missing client_secret with post",
			&ClientRegistration{ClientID: "c", TokenEndpointAuthMethod: "client_secret_post"},
			true,
		},
		{"missing client_secret with none is OK",
			&ClientRegistration{ClientID: "c", TokenEndpointAuthMethod: "none"},
			false,
		},
		{"full response",
			&ClientRegistration{ClientID: "c", ClientSecret: "s", TokenEndpointAuthMethod: "client_secret_basic"},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRegistrationResponse(tt.reg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRegistrationResponse: err=%v, wantErr=%v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidRegistrationResponse) {
				t.Errorf("err should wrap ErrInvalidRegistrationResponse, got %v", err)
			}
		})
	}
}

func TestRegisterClient_MissingClientIDInResponse_FailsValidation(t *testing.T) {
	t.Setenv("TOOL_OAUTH_CLIENT_ID", "")
	t.Setenv("MCP_CLIENT_ID", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"client_secret":"s"}`))
	}))
	defer srv.Close()

	_, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", nil)
	if err == nil {
		t.Fatal("expected ErrInvalidRegistrationResponse when client_id is missing")
	}
	if !errors.Is(err, ErrInvalidRegistrationResponse) {
		t.Errorf("err = %v, want ErrInvalidRegistrationResponse", err)
	}
}

func TestRegisterClient_5xx_FallsBackToEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"server_error","error_description":"temporary failure"}`))
	}))
	defer srv.Close()

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "env-cid")
	t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "env-secret")

	reg, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if reg.ClientID != "env-cid" {
		t.Errorf("ClientID = %q, want env-cid", reg.ClientID)
	}
}

func TestNormaliseCacheKey(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://Example.com/resource", "https://example.com/resource"},
		{"https://Example.com/resource/", "https://example.com/resource"},
		{"https://EXAMPLE.COM/", "https://example.com/"},
		{"https://example.com", "https://example.com/"},
		{"https://a.example.com/path/", "https://a.example.com/path"},
		{"not a url", "not a url"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normaliseCacheKey(tt.in); got != tt.want {
				t.Errorf("normaliseCacheKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDiscoverOAuthMetadata_NormalisedCacheKey_Dedupes(t *testing.T) {
	clearDiscoveryCacheForTest(t)

	var hits atomic.Int64
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "mock",
				"authorization_servers": []string{srv.URL},
			})
		case "/.well-known/oauth-authorization-server":
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/authorize",
				"token_endpoint":         srv.URL + "/token",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if _, err := DiscoverOAuthMetadata(context.Background(), srv.URL); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := DiscoverOAuthMetadata(context.Background(), srv.URL+"/"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("metadata fetch count = %d, want 1 (normalised keys must dedupe)", got)
	}
}

func TestFetchJSON_RetriesOn5xxThenSucceeds(t *testing.T) {
	prev := discoveryRetryConfig
	discoveryRetryConfig = retryConfig{MaxAttempts: 3, BaseDelay: 1 * time.Millisecond, MaxDelay: 10 * time.Millisecond}
	t.Cleanup(func() { discoveryRetryConfig = prev })

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := fetchJSON(context.Background(), srv.URL, "test")
	if err != nil {
		t.Fatalf("fetchJSON: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %s", body)
	}
	if hits.Load() != 3 {
		t.Errorf("hits = %d, want 3 (2 retries + 1 success)", hits.Load())
	}
}

func TestFetchJSON_RetriesOn429(t *testing.T) {
	prev := discoveryRetryConfig
	discoveryRetryConfig = retryConfig{MaxAttempts: 2, BaseDelay: 1 * time.Millisecond, MaxDelay: 10 * time.Millisecond}
	t.Cleanup(func() { discoveryRetryConfig = prev })

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := fetchJSON(context.Background(), srv.URL, "test"); err != nil {
		t.Fatalf("fetchJSON: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("hits = %d, want 2", hits.Load())
	}
}

func TestFetchJSON_DoesNotRetry4xx(t *testing.T) {
	prev := discoveryRetryConfig
	discoveryRetryConfig = retryConfig{MaxAttempts: 3, BaseDelay: 1 * time.Millisecond, MaxDelay: 10 * time.Millisecond}
	t.Cleanup(func() { discoveryRetryConfig = prev })

	for _, code := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(code)
			}))
			defer srv.Close()

			_, err := fetchJSON(context.Background(), srv.URL, "test")
			if err == nil {
				t.Error("expected error")
			}
			if hits.Load() != 1 {
				t.Errorf("hits = %d, want 1 (no retry on 4xx)", hits.Load())
			}
		})
	}
}

func TestFetchJSON_ExhaustsRetries_ReturnsError(t *testing.T) {
	prev := discoveryRetryConfig
	discoveryRetryConfig = retryConfig{MaxAttempts: 3, BaseDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond}
	t.Cleanup(func() { discoveryRetryConfig = prev })

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := fetchJSON(context.Background(), srv.URL, "test"); err == nil {
		t.Error("expected error after retry exhaustion")
	}
	if hits.Load() != 3 {
		t.Errorf("hits = %d, want 3 (MaxAttempts exhausted)", hits.Load())
	}
}

func TestRegisterClient_RetriesOn5xxThenSucceeds(t *testing.T) {
	TightenDiscoveryRetryForTest(t)

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "")
	t.Setenv("MCP_CLIENT_ID", "")

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"cid-after-retry","client_secret":"sec","token_endpoint_auth_method":"client_secret_basic"}`))
	}))
	defer srv.Close()

	reg, err := RegisterClient(context.Background(), srv.URL, "http://localhost/cb", nil)
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if reg.ClientID != "cid-after-retry" {
		t.Errorf("ClientID = %q, want cid-after-retry", reg.ClientID)
	}
	if hits.Load() != 3 {
		t.Errorf("hits = %d, want 3 (2 retries + 1 success)", hits.Load())
	}
}

func TestRegisterClient_DoesNotRetry4xx(t *testing.T) {
	TightenDiscoveryRetryForTest(t)

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "fallback-cid")
	t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "")

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_redirect_uri"}`))
	}))
	defer srv.Close()

	reg, err := RegisterClient(context.Background(), srv.URL, "http://localhost/cb", nil)
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if reg.ClientID != "fallback-cid" {
		t.Errorf("ClientID = %q, want env fallback", reg.ClientID)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (no retry on 4xx)", hits.Load())
	}
}

func TestRegisterClient_DisableEnvFallback(t *testing.T) {
	TightenDiscoveryRetryForTest(t)

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "must-not-leak")
	t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "must-not-leak")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	reg, err := RegisterClientWithConfig(
		context.Background(),
		srv.URL,
		"http://localhost/cb",
		nil,
		RegistrationRequest{DisableEnvFallback: true},
	)
	if err == nil {
		t.Fatalf("expected hard error, got reg=%+v", reg)
	}
	if reg != nil {
		t.Errorf("expected nil registration, got %+v", reg)
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Errorf("error surface leaked env creds: %v", err)
	}
}

func TestRegisterClient_CustomClientName(t *testing.T) {
	var gotClientName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotClientName, _ = body["client_name"].(string)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "cid",
			"client_secret":              "secret",
			"token_endpoint_auth_method": "client_secret_basic",
		})
	}))
	defer srv.Close()

	_, err := RegisterClientWithConfig(
		context.Background(),
		srv.URL,
		"http://localhost/cb",
		nil,
		RegistrationRequest{ClientName: "MCP Discovery Client"},
	)
	if err != nil {
		t.Fatalf("RegisterClientWithConfig: %v", err)
	}
	if gotClientName != "MCP Discovery Client" {
		t.Errorf("client_name = %q, want %q", gotClientName, "MCP Discovery Client")
	}
}

func TestFetchMetadata_ParsesCapabilityFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "https://auth.example.com",
			"authorization_endpoint": "https://auth.example.com/authorize",
			"token_endpoint": "https://auth.example.com/token",
			"code_challenge_methods_supported": ["plain", "S256"],
			"grant_types_supported": ["authorization_code", "refresh_token"],
			"response_types_supported": ["code", "token"]
		}`))
	}))
	defer srv.Close()

	meta, err := fetchMetadata(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchMetadata: %v", err)
	}
	if len(meta.CodeChallengeMethodsSupported) != 2 || meta.CodeChallengeMethodsSupported[1] != "S256" {
		t.Errorf("CodeChallengeMethodsSupported = %v", meta.CodeChallengeMethodsSupported)
	}
	if len(meta.GrantTypesSupported) != 2 {
		t.Errorf("GrantTypesSupported = %v", meta.GrantTypesSupported)
	}
	if len(meta.ResponseTypesSupported) != 2 {
		t.Errorf("ResponseTypesSupported = %v", meta.ResponseTypesSupported)
	}
}

func TestContainsString(t *testing.T) {
	if !containsString([]string{"a", "b"}, "b") {
		t.Error("expected hit")
	}
	if containsString([]string{"a"}, "z") {
		t.Error("expected miss")
	}
	if containsString(nil, "x") {
		t.Error("nil slice should not match")
	}
}

func TestRegisterClient_MalformedJSONResponse_FallsBackWithLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("not-a-valid-json-response"))
	}))
	defer srv.Close()

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "env-fallback-id")
	t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "env-fallback-secret")

	reg, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/cb", nil)
	if err != nil {
		t.Fatalf("expected env fallback on malformed JSON, got error: %v", err)
	}
	if reg == nil || !reg.FromEnv {
		t.Errorf("expected FromEnv=true fallback; got reg=%+v", reg)
	}
	if reg.ClientID != "env-fallback-id" {
		t.Errorf("env fallback client_id = %q, want env-fallback-id", reg.ClientID)
	}
}

func TestCacheMetadata_CachesUnconditionally(t *testing.T) {
	clearDiscoveryCacheForTest(t)

	partial := &OAuthMetadata{
		Issuer:                "https://example.com",
		AuthorizationEndpoint: "https://example.com/authorize",
		TokenEndpoint:         "https://example.com/token",
	}
	cacheMetadata("test-partial", partial)

	DiscoveryCacheMu.Lock()
	gotPartial, okPartial := DiscoveryCache["test-partial"]
	DiscoveryCacheMu.Unlock()
	if !okPartial || gotPartial != partial {
		t.Error("partial metadata should still be cached (Go intentional divergence from Python)")
	}

	complete := &OAuthMetadata{
		Issuer:                "https://example.com",
		AuthorizationEndpoint: "https://example.com/authorize",
		TokenEndpoint:         "https://example.com/token",
		RegistrationEndpoint:  "https://example.com/register",
	}
	cacheMetadata("test-complete", complete)

	DiscoveryCacheMu.Lock()
	gotComplete, okComplete := DiscoveryCache["test-complete"]
	DiscoveryCacheMu.Unlock()
	if !okComplete || gotComplete != complete {
		t.Error("complete metadata must be cached")
	}
}

func TestCacheMetadata_SkipsNil(t *testing.T) {
	clearDiscoveryCacheForTest(t)
	cacheMetadata("nil-key", nil)
	DiscoveryCacheMu.Lock()
	_, ok := DiscoveryCache["nil-key"]
	DiscoveryCacheMu.Unlock()
	if ok {
		t.Error("nil metadata should not be stored")
	}
}
