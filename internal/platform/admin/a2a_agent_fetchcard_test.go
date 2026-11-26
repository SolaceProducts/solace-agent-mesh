// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newSvcForTest(t *testing.T) (*A2AAgentService, *store.A2AAgentRepo) {
	t.Helper()
	db := testOpenDB(t)
	a2aRepo := store.NewA2AAgentRepo(db)
	agentDeployRepo := store.NewAgentDeploymentRepo(db)
	agentRepo := store.NewAgentRepo(db)
	svc := NewA2AAgentService(a2aRepo, agentDeployRepo, agentRepo, nil, nil, plainTestHTTPClient(), "test-ns", slog.Default())
	return svc, a2aRepo
}

func TestFetchCard_UsesStoredBearerCredentials(t *testing.T) {
	t.Parallel()

	var (
		mu             sync.Mutex
		receivedHeader string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedHeader = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = io.WriteString(w, `{"name":"test","description":"a test agent","skills":[{"id":"s1","name":"s1","description":"d"}],"defaultInputModes":["text"],"defaultOutputModes":["text"]}`)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)
	ctx := t.Context()

	const realToken = "super-real-token"

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:         "E2E test A2A agent for fixture purposes",
		AgentURL:            srv.URL,
		Name:                "bearer-round-trip",
		AgentCardLocation:   "well_known",
		AgentCardAuthType:   "bearer",
		AgentCardAuthConfig: map[string]any{"token": realToken},
		AgentAuthConfig:     map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	authConfig, _ := got.AgentCardAuthConfig.(map[string]any)
	if authConfig["token"] != redactedSentinel {
		t.Fatalf("expected token redacted, got %v", authConfig["token"])
	}

	echoed := map[string]any{"token": redactedSentinel}
	_, err = svc.Update(ctx, created.ID, UpdateRemoteAgentRequest{
		Description:         "updated description",
		AgentCardAuthConfig: echoed,
	}, "tester")
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	withCard, err := svc.GetByIDWithCard(ctx, created.ID, true)
	if err != nil {
		t.Fatalf("get with fetch: %v", err)
	}
	if withCard.CardFetchError != "" {
		t.Fatalf("unexpected card fetch error: %s", withCard.CardFetchError)
	}
	if withCard.CardData == nil {
		t.Fatal("want card data, got nil")
	}
	mu.Lock()
	hdr := receivedHeader
	mu.Unlock()
	if hdr != "Bearer "+realToken {
		t.Errorf("external server Authorization = %q, want 'Bearer %s'", hdr, realToken)
	}
}

func TestFetchCard_ApikeyCustomHeader(t *testing.T) {
	t.Parallel()

	var (
		mu         sync.Mutex
		hdrKey     string
		hdrCustom  string
		customName = "X-Special-Key"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hdrKey = r.Header.Get("X-API-Key")
		hdrCustom = r.Header.Get(customName)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"name":"n","description":"d","skills":[{"id":"s","name":"s","description":"d"}]}`)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)

	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          srv.URL,
		Name:              "custom-header",
		AgentCardLocation: "well_known",
		AgentCardAuthType: "apikey",
		AgentCardAuthConfig: map[string]any{
			"apiKey":     "k-1",
			"headerName": customName,
		},
		AgentAuthConfig: map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := svc.GetByIDWithCard(t.Context(), created.ID, true); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hdrCustom != "k-1" {
		t.Errorf("%s = %q, want k-1", customName, hdrCustom)
	}
	if hdrKey != "" {
		t.Errorf("default X-API-Key should not be used when custom header specified: %q", hdrKey)
	}
}

func TestFetchCard_BasicAuth(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		user string
		pass string
		ok   bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, b := r.BasicAuth()
		mu.Lock()
		user = u
		pass = p
		ok = b
		mu.Unlock()
		_, _ = io.WriteString(w, `{"name":"n","description":"d","skills":[{"id":"s","name":"s","description":"d"}]}`)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          srv.URL,
		Name:              "basic-agent",
		AgentCardLocation: "well_known",
		AgentCardAuthType: "basic",
		AgentCardAuthConfig: map[string]any{
			"username": "alice",
			"password": "s3cret",
		},
		AgentAuthConfig: map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.GetByIDWithCard(t.Context(), created.ID, true); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !ok || user != "alice" || pass != "s3cret" {
		t.Errorf("basic auth = (%q, %q, %v), want (alice, s3cret, true)", user, pass, ok)
	}
}

func TestFetchCard_Error_ReturnsCardFetchErrorNotFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          srv.URL,
		Name:              "failing-fetch",
		AgentCardLocation: "well_known",
		AgentCardAuthType: "none",
		AgentAuthConfig:   map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.GetByIDWithCard(t.Context(), created.ID, true)
	if err != nil {
		t.Fatalf("GetByIDWithCard must not error on fetch failure, got: %v", err)
	}
	if got.CardFetchError == "" {
		t.Error("expected CardFetchError to be set on fetch failure")
	}
	if got.CardData != nil {
		t.Error("expected CardData nil on fetch failure")
	}
}

func TestFetchCard_LocationNone_Skipped(t *testing.T) {
	t.Parallel()
	svc, _ := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com",
		Name:              "none-location",
		AgentCardLocation: "none",
		Skills:            []any{map[string]any{"name": "s"}},
		AgentAuthConfig:   map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := svc.GetByIDWithCard(t.Context(), created.ID, true)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CardData != nil {
		t.Errorf("fetchCard with location=none should not populate CardData, got %+v", got.CardData)
	}
	if got.CardFetchError != "" {
		t.Errorf("no fetch attempted → no error, got %q", got.CardFetchError)
	}
}

func TestFetchCard_False_NoFetchAttempted(t *testing.T) {
	t.Parallel()

	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          srv.URL,
		Name:              "no-fetch",
		AgentCardLocation: "well_known",
		AgentCardAuthType: "none",
		AgentAuthConfig:   map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.GetByIDWithCard(t.Context(), created.ID, false); err != nil {
		t.Fatalf("get: %v", err)
	}
	if called {
		t.Error("external server must not be hit when fetchCard=false")
	}
}

func TestUpdate_SecretPreservation_Apikey(t *testing.T) {
	t.Parallel()
	svc, repo := newSvcForTest(t)
	const realKey = "my-real-api-key-xyz"

	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com",
		Name:              "apikey-agent",
		AgentCardLocation: "none",
		AgentCardAuthType: "apikey",
		AgentCardAuthConfig: map[string]any{
			"apiKey":     realKey,
			"headerName": "X-API-Key",
		},
		Skills:          []any{map[string]any{"name": "s"}},
		AgentAuthConfig: map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	echoed := map[string]any{
		"apiKey":     redactedSentinel,
		"headerName": "X-API-Key",
	}
	if _, updErr := svc.Update(t.Context(), created.ID, UpdateRemoteAgentRequest{
		AgentCardAuthConfig: echoed,
	}, "tester"); updErr != nil {
		t.Fatalf("update: %v", updErr)
	}

	raw, err := repo.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("repo get: %v", err)
	}
	if raw.AgentCardAuthConfig == nil {
		t.Fatal("auth config is nil after update")
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*raw.AgentCardAuthConfig), &stored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	apiKey, _ := stored["api_key"].(string)
	if apiKey != realKey {
		t.Errorf("stored api_key = %q, want %q (original preserved)", apiKey, realKey)
	}
}

func TestUpdate_ReplacesOnNewSecret(t *testing.T) {
	t.Parallel()
	svc, repo := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:         "E2E test A2A agent for fixture purposes",
		AgentURL:            "https://example.com",
		Name:                "replace-agent",
		AgentCardLocation:   "none",
		AgentCardAuthType:   "bearer",
		AgentCardAuthConfig: map[string]any{"token": "first-token"},
		Skills:              []any{map[string]any{"name": "s"}},
		AgentAuthConfig:     map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(t.Context(), created.ID, UpdateRemoteAgentRequest{
		AgentCardAuthConfig: map[string]any{"token": "second-token"},
	}, "tester"); err != nil {
		t.Fatalf("update: %v", err)
	}
	raw, _ := repo.GetByID(t.Context(), created.ID)
	var stored map[string]any
	_ = json.Unmarshal([]byte(*raw.AgentCardAuthConfig), &stored)
	if stored["token"] != "second-token" {
		t.Errorf("token = %v, want second-token", stored["token"])
	}
}

func TestUpdate_NoAuthConfigChangeWhenFieldOmitted(t *testing.T) {
	t.Parallel()
	svc, repo := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:         "E2E test A2A agent for fixture purposes",
		AgentURL:            "https://example.com",
		Name:                "omitted-field",
		AgentCardLocation:   "none",
		AgentCardAuthType:   "bearer",
		AgentCardAuthConfig: map[string]any{"token": "keep-me"},
		Skills:              []any{map[string]any{"name": "s"}},
		AgentAuthConfig:     map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(t.Context(), created.ID, UpdateRemoteAgentRequest{
		Description: "new description",
	}, "tester"); err != nil {
		t.Fatalf("update: %v", err)
	}
	raw, _ := repo.GetByID(t.Context(), created.ID)
	if !strings.Contains(*raw.AgentCardAuthConfig, "keep-me") {
		t.Errorf("stored auth config changed when field omitted: %s", *raw.AgentCardAuthConfig)
	}
}

func TestUpdate_OptionalCardAuth_EmptyStringClearsSecret(t *testing.T) {
	t.Parallel()
	svc, repo := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:         "E2E test A2A agent for fixture purposes",
		AgentURL:            "https://example.com",
		Name:                "clear-optional",
		AgentCardLocation:   "none",
		AgentCardAuthType:   "bearer",
		AgentCardAuthConfig: map[string]any{"token": "secret-to-clear"},
		Skills:              []any{map[string]any{"name": "s"}},
		AgentAuthConfig:     map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(t.Context(), created.ID, UpdateRemoteAgentRequest{
		AgentCardAuthConfig: map[string]any{"token": ""},
	}, "tester"); err != nil {
		t.Fatalf("update: %v", err)
	}
	raw, _ := repo.GetByID(t.Context(), created.ID)
	var stored map[string]any
	_ = json.Unmarshal([]byte(*raw.AgentCardAuthConfig), &stored)
	if v, present := stored["token"]; !present {
		t.Errorf("expected token key to be present as explicit null, got %v", stored)
	} else if v != nil {
		t.Errorf("token = %v, want null (cleared)", v)
	}
}

func TestUpdate_RequiredAgentAuth_EmptyStringPreserves(t *testing.T) {
	t.Parallel()
	svc, repo := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com",
		Name:              "required-empty",
		AgentCardLocation: "none",
		AgentAuthType:     "bearer",
		AgentAuthConfig:   map[string]any{"token": "keep-required"},
		Skills:            []any{map[string]any{"name": "s"}},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(t.Context(), created.ID, UpdateRemoteAgentRequest{
		AgentAuthConfig: map[string]any{"token": ""},
	}, "tester"); err != nil {
		t.Fatalf("update: %v", err)
	}
	raw, _ := repo.GetByID(t.Context(), created.ID)
	var stored map[string]any
	_ = json.Unmarshal([]byte(*raw.AgentAuthConfig), &stored)
	if stored["token"] != "keep-required" {
		t.Errorf("token = %v, want preserved keep-required", stored["token"])
	}
}

func TestUpdate_MixedPayload_NoSentinelLeakInDB(t *testing.T) {
	t.Parallel()
	svc, repo := newSvcForTest(t)
	created, err := svc.Create(t.Context(), CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          "https://example.com",
		Name:              "mixed-payload",
		AgentCardLocation: "none",
		AgentCardAuthType: "apikey",
		AgentCardAuthConfig: map[string]any{
			"apiKey":     "orig-key",
			"headerName": "X-Custom",
		},
		Skills:          []any{map[string]any{"name": "s"}},
		AgentAuthConfig: map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(t.Context(), created.ID, UpdateRemoteAgentRequest{
		AgentCardAuthConfig: map[string]any{
			"apiKey":     "new-key",
			"headerName": "X-Custom",
			"bonusField": redactedSentinel,
		},
	}, "tester"); err != nil {
		t.Fatalf("update: %v", err)
	}
	raw, _ := repo.GetByID(t.Context(), created.ID)
	var stored map[string]any
	_ = json.Unmarshal([]byte(*raw.AgentCardAuthConfig), &stored)
	if stored["api_key"] != "new-key" {
		t.Errorf("api_key = %v, want new-key", stored["api_key"])
	}
	for k, v := range stored {
		if s, ok := v.(string); ok && s == redactedSentinel {
			t.Errorf("field %q leaked sentinel literal into DB: %v", k, stored)
		}
	}
}

func TestGetByIDWithCard_WellKnownLegacyFallback(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/.well-known/agent.json" {
			_, _ = io.WriteString(w, `{"name":"legacy","description":"legacy card path agent","skills":[],"defaultInputModes":["text"],"defaultOutputModes":["text"]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          srv.URL,
		Name:              "legacy-well-known",
		AgentCardLocation: "well_known",
		AgentCardAuthType: "none",
		AgentAuthConfig:   map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := svc.GetByIDWithCard(ctx, created.ID, true)
	if err != nil {
		t.Fatalf("GetByIDWithCard: %v", err)
	}
	if resp.CardFetchError != "" {
		t.Fatalf("CardFetchError = %q, want empty (legacy fallback should succeed)", resp.CardFetchError)
	}
	if resp.CardData == nil || resp.CardData.Name != "legacy" {
		t.Fatalf("CardData = %+v, want card fetched from legacy path", resp.CardData)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"/.well-known/agent-card.json", "/.well-known/agent.json"}
	if len(seen) != len(want) {
		t.Fatalf("request paths = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("request paths = %v, want %v", seen, want)
		}
	}
}

func TestGetByIDWithCard_WellKnownNoFallbackOnAuthError(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateRemoteAgentRequest{
		Description:       "E2E test A2A agent for fixture purposes",
		AgentURL:          srv.URL,
		Name:              "auth-error-well-known",
		AgentCardLocation: "well_known",
		AgentCardAuthType: "none",
		AgentAuthConfig:   map[string]any{},
	}, "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := svc.GetByIDWithCard(ctx, created.ID, true)
	if err != nil {
		t.Fatalf("GetByIDWithCard: %v", err)
	}
	if resp.CardFetchError == "" {
		t.Fatalf("CardFetchError = empty, want a 401 error surfaced")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "/.well-known/agent-card.json" {
		t.Fatalf("request paths = %v, want a single request to /.well-known/agent-card.json (no fallback on 401)", seen)
	}
}

func TestWellKnownCardURLs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		agentURL string
		want     []string
	}{
		{
			name:     "no trailing slash",
			agentURL: "https://host:8080",
			want:     []string{"https://host:8080/.well-known/agent-card.json", "https://host:8080/.well-known/agent.json"},
		},
		{
			name:     "trailing slash trimmed",
			agentURL: "https://host:8080/",
			want:     []string{"https://host:8080/.well-known/agent-card.json", "https://host:8080/.well-known/agent.json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := wellKnownCardURLs(tt.agentURL)
			if len(got) != len(tt.want) {
				t.Fatalf("wellKnownCardURLs(%q) = %v, want %v", tt.agentURL, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("wellKnownCardURLs(%q) = %v, want %v", tt.agentURL, got, tt.want)
				}
			}
		})
	}
}

func TestFetchCardUsingStored_Branches(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"name":"custom","description":"custom card path agent","skills":[],"defaultInputModes":["text"],"defaultOutputModes":["text"]}`)
	}))
	t.Cleanup(srv.Close)

	svc, _ := newSvcForTest(t)
	ctx := t.Context()
	customURL := srv.URL + "/agent/card.json"

	t.Run("custom success", func(t *testing.T) {
		card, err := svc.fetchCardUsingStored(ctx, &store.A2AAgent{
			AgentURL:          srv.URL,
			AgentCardLocation: "custom",
			AgentCardURL:      &customURL,
		})
		if err != nil {
			t.Fatalf("custom: %v", err)
		}
		if card == nil || card.Name != "custom" {
			t.Fatalf("card = %+v, want custom card", card)
		}
	})

	t.Run("custom missing url", func(t *testing.T) {
		if _, err := svc.fetchCardUsingStored(ctx, &store.A2AAgent{
			AgentURL:          srv.URL,
			AgentCardLocation: "custom",
		}); err == nil {
			t.Fatal("expected error when custom card URL is nil")
		}
	})

	t.Run("unknown location", func(t *testing.T) {
		if _, err := svc.fetchCardUsingStored(ctx, &store.A2AAgent{
			AgentURL:          srv.URL,
			AgentCardLocation: "bogus",
		}); err == nil {
			t.Fatal("expected error for unknown card location")
		}
	})
}

func TestFetchCardAt_Errors(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/notjson":
			_, _ = io.WriteString(w, "not json at all")
		case "/noname":
			_, _ = io.WriteString(w, `{"description":"has description only"}`)
		case "/nodesc":
			_, _ = io.WriteString(w, `{"name":"has name only"}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)

	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	svc, _ := newSvcForTest(t)
	ctx := t.Context()

	tests := []struct {
		name       string
		url        string
		authType   string
		wantStatus int
	}{
		{"invalid request URL", "http://\x7f/bad", "none", 0},
		{"auth config error", srv.URL + "/x", "bearer", 0},
		{"transport error", closedURL + "/x", "none", 0},
		{"non-200 status", srv.URL + "/status500", "none", http.StatusInternalServerError},
		{"invalid json", srv.URL + "/notjson", "none", http.StatusOK},
		{"missing name", srv.URL + "/noname", "none", http.StatusOK},
		{"missing description", srv.URL + "/nodesc", "none", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			card, status, err := svc.fetchCardAt(ctx, tt.url, tt.authType, nil, nil)
			if err == nil {
				t.Fatalf("expected error, got card %+v", card)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
		})
	}
}
