// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveIdentity_EmailViaLookup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id":    "user-123",
			"name":  "Test User",
			"email": "test@example.com",
		})
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())
	id := cache.ResolveIdentity(context.Background(), "user-123", "Test User", "aad-123", srv.URL, "conv-123")

	if id.UserID() != "test@example.com" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "test@example.com")
	}
	if id.Email() != "test@example.com" {
		t.Errorf("Email = %q, want %q", id.Email(), "test@example.com")
	}
	if id.AuthMethod() != "teams" {
		t.Errorf("AuthMethod = %q, want %q", id.AuthMethod(), "teams")
	}
	if id.Name() != "Test User" {
		t.Errorf("Name = %q, want %q", id.Name(), "Test User")
	}
}

func TestResolveIdentity_FallsBackToUPN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id":                "user-123",
			"name":              "Test User",
			"userPrincipalName": "testuser@contoso.com",
		})
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())
	id := cache.ResolveIdentity(context.Background(), "user-123", "Test User", "", srv.URL, "conv-123")

	if id.UserID() != "testuser@contoso.com" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "testuser@contoso.com")
	}
}

func TestResolveIdentity_FallsBackToAAD(t *testing.T) {
	cache := NewEmailCache("", 0, nil, slog.Default())
	id := cache.ResolveIdentity(context.Background(), "user-123", "Test User", "aad-object-456", "", "")

	if id.UserID() != "aad-object-456" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "aad-object-456")
	}
}

func TestResolveIdentity_FallsBackToTeamsUserID(t *testing.T) {
	cache := NewEmailCache("", 0, nil, slog.Default())
	id := cache.ResolveIdentity(context.Background(), "user-123", "Test User", "", "", "")

	if id.UserID() != "teams:user-123" {
		t.Errorf("UserID = %q, want %q", id.UserID(), "teams:user-123")
	}
}

func TestEmailCache_CachesResults(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
			return
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id":    "user-123",
			"email": "cached@example.com",
		})
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())

	id1 := cache.ResolveIdentity(context.Background(), "user-123", "User", "aad-123", srv.URL, "conv-123")
	if id1.Email() != "cached@example.com" {
		t.Fatalf("first call: Email = %q, want %q", id1.Email(), "cached@example.com")
	}
	firstCallCount := callCount

	id2 := cache.ResolveIdentity(context.Background(), "user-123", "User", "aad-123", srv.URL, "conv-123")
	if id2.Email() != "cached@example.com" {
		t.Fatalf("second call: Email = %q, want %q", id2.Email(), "cached@example.com")
	}
	if callCount != firstCallCount {
		t.Errorf("expected cache hit, but member lookup was called %d times total", callCount)
	}
}

func TestEmailCache_CachesNegativeResults(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
			return
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id":   "user-no-email",
			"name": "No Email User",
		})
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())

	id1 := cache.ResolveIdentity(context.Background(), "user-no-email", "User", "aad-456", srv.URL, "conv-123")
	if id1.UserID() != "aad-456" {
		t.Fatalf("first call: UserID = %q, want %q", id1.UserID(), "aad-456")
	}
	firstCallCount := callCount

	id2 := cache.ResolveIdentity(context.Background(), "user-no-email", "User", "aad-456", srv.URL, "conv-123")
	if id2.UserID() != "aad-456" {
		t.Fatalf("second call: UserID = %q, want %q", id2.UserID(), "aad-456")
	}
	if callCount != firstCallCount {
		t.Errorf("expected negative cache hit, but member lookup was called %d times total", callCount)
	}
}

func TestResolveIdentity_AlwaysUsesTeamsUserIDAsLookupKey(t *testing.T) {
	var lookupPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
			return
		}
		lookupPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id":    "aad-obj-789",
			"email": "user@example.com",
		})
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 0, bc, slog.Default())
	cache.ResolveIdentity(context.Background(), "user-123", "User", "aad-obj-789", srv.URL, "conv-123")

	expected := "/v3/conversations/conv-123/members/user-123"
	if lookupPath != expected {
		t.Errorf("lookup path = %q, want %q", lookupPath, expected)
	}
}

func TestResolveIdentity_HTTPError_FallsBackToAAD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())
	id := cache.ResolveIdentity(context.Background(), "user-123", "User", "aad-guid-123", srv.URL, "conv-123")

	if id.UserID() != "aad-guid-123" {
		t.Errorf("UserID = %q, want %q (AAD fallback)", id.UserID(), "aad-guid-123")
	}
	if id.Metadata()["source"] != "azure_ad" {
		t.Errorf("source = %v, want %q", id.Metadata()["source"], "azure_ad")
	}
}

func TestEmailCache_DoesNotCacheTransientFailures(t *testing.T) {
	callCount := 0
	failFirst := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		callCount++
		if failFirst {
			failFirst = false
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id": "user-123", "email": "recovered@example.com",
		})
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())

	id1 := cache.ResolveIdentity(context.Background(), "user-123", "User", "aad-789", srv.URL, "conv-123")
	if id1.UserID() != "aad-789" {
		t.Fatalf("first call: UserID = %q, want aad-789 (transient failure → AAD fallback)", id1.UserID())
	}

	id2 := cache.ResolveIdentity(context.Background(), "user-123", "User", "aad-789", srv.URL, "conv-123")
	if id2.Email() != "recovered@example.com" {
		t.Errorf("second call: Email = %q, want recovered@example.com (transient failure must not be cached)", id2.Email())
	}
	if callCount < 2 {
		t.Errorf("expected member lookup to be retried after transient failure; callCount = %d", callCount)
	}
}

func TestEmailCache_CachesDefinitiveClientError(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		callCount++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())

	id1 := cache.ResolveIdentity(context.Background(), "user-403", "User", "aad-403", srv.URL, "conv-123")
	if id1.UserID() != "aad-403" {
		t.Fatalf("first call: UserID = %q, want aad-403 (403 → AAD fallback)", id1.UserID())
	}
	firstCallCount := callCount

	id2 := cache.ResolveIdentity(context.Background(), "user-403", "User", "aad-403", srv.URL, "conv-123")
	if id2.UserID() != "aad-403" {
		t.Fatalf("second call: UserID = %q, want aad-403", id2.UserID())
	}
	if callCount != firstCallCount {
		t.Errorf("expected definitive 403 to be cached, but member lookup was called %d times total", callCount)
	}
}

func TestEmailCache_DoesNotCacheRateLimit(t *testing.T) {
	callCount := 0
	limited := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		callCount++
		if limited {
			limited = false
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"id": "user-429", "email": "recovered@example.com",
		})
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	cache := NewEmailCache("tenant-123", 5*time.Minute, bc, slog.Default())

	id1 := cache.ResolveIdentity(context.Background(), "user-429", "User", "aad-429", srv.URL, "conv-123")
	if id1.UserID() != "aad-429" {
		t.Fatalf("first call: UserID = %q, want aad-429 (429 → AAD fallback)", id1.UserID())
	}

	id2 := cache.ResolveIdentity(context.Background(), "user-429", "User", "aad-429", srv.URL, "conv-123")
	if id2.Email() != "recovered@example.com" {
		t.Errorf("second call: Email = %q, want recovered@example.com (429 must not be cached)", id2.Email())
	}
	if callCount < 2 {
		t.Errorf("expected member lookup retried after 429; callCount = %d", callCount)
	}
}

func TestResolveIdentity_SurfacesSourceMetadata(t *testing.T) {
	cache := NewEmailCache("", 0, nil, slog.Default())

	id := cache.ResolveIdentity(context.Background(), "user-1", "User", "aad-1", "", "")
	if id.Metadata()["source"] != "azure_ad" {
		t.Errorf("aad source = %v, want azure_ad", id.Metadata()["source"])
	}

	id = cache.ResolveIdentity(context.Background(), "user-2", "User", "", "", "")
	if id.Metadata()["source"] != "teams_fallback" {
		t.Errorf("fallback source = %v, want teams_fallback", id.Metadata()["source"])
	}
	if id.UserID() != "teams:user-2" {
		t.Errorf("fallback userID = %q, want teams:user-2", id.UserID())
	}
}
