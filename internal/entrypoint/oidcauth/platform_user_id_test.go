// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const matchedUserID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"

func resolvedAs(resolution identity.LookupResolution) auth.AuthorizationResolution {
	return auth.AuthorizationResolution{Scopes: []string{"scope"}, Resolution: resolution, ResolvedUserID: matchedUserID}
}

var platformUserCases = []struct {
	name       string
	resolution identity.LookupResolution
	want       string
}{
	{"matched", identity.LookupMatched, matchedUserID},
	{"ambiguous", identity.LookupAmbiguous, ""},
	{"responder that does not say", identity.LookupUnknown, ""},
}

func loginAndAuthenticate(t *testing.T, p *Provider) *auth.Subject {
	t.Helper()
	callback := httptest.NewRecorder()
	p.handleCallback(callback, httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=auth-code", nil))
	_, fragment, _ := strings.Cut(callback.Header().Get("Location"), "#")
	minted, err := url.ParseQuery(fragment)
	if err != nil || minted.Get("sam_access_token") == "" {
		t.Fatalf("callback minted no token: status=%d fragment=%q", callback.Code, fragment)
	}

	var got *auth.Subject
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = auth.SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+minted.Get("sam_access_token"))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if got == nil {
		t.Fatal("the minted token authenticated no identity")
	}
	return got
}

func TestALoginCarriesThePlatformUserItResolvedToIntoTheSAMToken(t *testing.T) {
	for _, tc := range platformUserCases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := mintProviderWithCache(t, resolvedAs(tc.resolution))

			if got := loginAndAuthenticate(t, p).PlatformUserID(); got != tc.want {
				t.Errorf("PlatformUserID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTokenValidatorReadsThePlatformUserFromTheSAMToken(t *testing.T) {
	signer := setupTestSigner(t)
	authz, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "none"}, "gw-test", slog.Default())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	v := NewTokenValidator(signer, authz, nil, "gw-1", slog.Default())

	for _, tc := range []struct {
		name, claim string
	}{
		{"a token minted with the claim", matchedUserID},
		{"a token minted before the claim existed", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token, err := MintSAMToken(signer, MintClaims{UserID: "alice@example.com", PlatformUserID: tc.claim}, time.Hour)
			if err != nil {
				t.Fatalf("MintSAMToken: %v", err)
			}
			id, err := v.Validate(t.Context(), token)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if id.PlatformUserID() != tc.claim || id.UserID() != "alice@example.com" {
				t.Errorf("identity = %q/%q, want %q/alice@example.com", id.PlatformUserID(), id.UserID(), tc.claim)
			}
		})
	}
}

func TestAnIdPBearerCarriesOnlyAMatchedPlatformUser(t *testing.T) {
	for _, tc := range platformUserCases {
		t.Run(tc.name, func(t *testing.T) {
			const idpToken = "idp-bearer-xyz"
			p := newProviderWithFakeOIDC(t, &fakeOIDCClient{
				expectedToken: idpToken,
				userInfo:      &UserInfo{Sub: "user-1", Email: "alice@example.com", Name: "Alice"},
			})
			svc, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1", testLogger())
			if err != nil {
				t.Fatalf("NewAuthorizationService: %v", err)
			}
			svc.SetAuthorizationClient(&stubAuthzClient{resolution: resolvedAs(tc.resolution)})
			p.authzSvc = svc

			id, err := NewTokenValidator(p.signer, svc, p, "gw-1", slog.Default()).Validate(t.Context(), idpToken)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if id.PlatformUserID() != tc.want {
				t.Errorf("PlatformUserID = %q, want %q", id.PlatformUserID(), tc.want)
			}
		})
	}
}

func TestAnMCPLoginCarriesThePlatformUserItResolvedToIntoTheValidatedIdentity(t *testing.T) {
	for _, tc := range platformUserCases {
		t.Run(tc.name, func(t *testing.T) {
			signer := setupTestSigner(t)
			svc, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1", testLogger())
			if err != nil {
				t.Fatalf("NewAuthorizationService: %v", err)
			}
			svc.SetAuthorizationClient(&stubAuthzClient{resolution: resolvedAs(tc.resolution)})

			_, _, platformUserID, err := NewMCPAuthzResolver(svc).Lookup(t.Context(), "alice@example.com", nil)
			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			token, err := NewMCPSAMMinter(signer, time.Hour).MintForUser("alice@example.com", platformUserID, "alice@example.com", "Alice", nil, nil, time.Hour)
			if err != nil {
				t.Fatalf("MintForUser: %v", err)
			}
			id, err := NewTokenValidator(signer, svc, nil, "gw-1", testLogger()).Validate(t.Context(), token)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if id.PlatformUserID() != tc.want {
				t.Errorf("PlatformUserID = %q, want %q", id.PlatformUserID(), tc.want)
			}
		})
	}
}

func TestALoginCarriesTheUserRowItsUpsertWroteOverTheLookup(t *testing.T) {
	for _, tc := range platformUserCases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := mintProviderWithCache(t, resolvedAs(tc.resolution))
			p.SetUserUpsertClient(&fakeUpsertClient{ch: make(chan auth.UserRecord, 4)})

			if got := loginAndAuthenticate(t, p).PlatformUserID(); got != "user-id-fake" {
				t.Errorf("PlatformUserID = %q, want the row the login upserted, whatever the lookup said", got)
			}
		})
	}
}

type signedIdentities struct {
	mu  sync.Mutex
	got []*auth.Subject
}

func (s *signedIdentities) SignTask(identity *auth.Subject, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, identity)
	return "signed-token", nil
}

func (s *signedIdentities) first() *auth.Subject {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.got) == 0 {
		return nil
	}
	return s.got[0]
}

func TestAnAPIKeyCallersTaskIsSubmittedForTheKeyOwnersRow(t *testing.T) {
	const ownerRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c51"
	p, _ := keyProvider(t, &recordingKeyAuthority{resolution: apikey.Resolution{
		KeyID: "key-1", OwnerID: ownerRow, OwnerSub: "system:ci-deployer", Scopes: []string{"*"},
	}})
	core, brk, requests := submittingCore(t)
	signer := &signedIdentities{}
	core.SetTaskSigner(signer)
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		msg := a2a.Message{Kind: "message", MessageID: "msg-1", Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewTextPart("hi")}}
		caller, ok := auth.SubjectFromContext(r.Context())
		if !ok {
			t.Error("middleware put no caller in the request context")
		}
		if err := core.SubmitTask(r.Context(), "test-agent", "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c52", msg, caller); err != nil {
			t.Errorf("SubmitTask: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/message:stream", nil)
	req.Header.Set("Authorization", "Bearer "+validKeyPlaintext)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, requests)
	if err != nil {
		t.Fatalf("no task reached the agent: %v", err)
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserUUID; got != ownerRow {
		t.Errorf("published userUuid = %q, want the key owner's users.id %s", got, ownerRow)
	}
	if signed := signer.first(); signed == nil || signed.PlatformUserID() != ownerRow {
		t.Errorf("signed identity = %+v, want the key owner's users.id %s", signed, ownerRow)
	}
}

func TestAScheduledTaskIsSubmittedForOnlyAMatchedPlatformUser(t *testing.T) {
	for _, tc := range platformUserCases {
		if tc.resolution == identity.LookupAmbiguous {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			authz, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1", testLogger())
			if err != nil {
				t.Fatalf("NewAuthorizationService: %v", err)
			}
			authz.SetAuthorizationClient(&stubAuthzClient{resolution: auth.AuthorizationResolution{
				Scopes: []string{"*"}, Resolution: tc.resolution, ResolvedUserID: matchedUserID,
			}})
			core, brk, requests := submittingCore(t)
			core.SetIdentityResolver(NewIdentityResolver(authz, nil))
			signer := &signedIdentities{}
			core.SetTaskSigner(signer)
			task := &store.ScheduledTask{
				ID: "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c50", Name: "nightly", Namespace: "test-ns", CreatedBy: "alice@example.com",
				ScheduleType: "cron", ScheduleExpression: "0 * * * *", TargetAgentName: "test-agent",
				TaskMessage: `[{"type":"text","text":"run"}]`, Enabled: true,
			}
			if err = store.NewScheduledTaskRepo(core.Store()).Create(t.Context(), task); err != nil {
				t.Fatalf("create task: %v", err)
			}

			if _, err = core.ScheduledTasks().TriggerTaskNow(t.Context(), task.ID, "alice@example.com"); err != nil {
				t.Fatalf("TriggerTaskNow: %v", err)
			}
			if err = core.ScheduledTasks().RunPasses(t.Context()); err != nil {
				t.Fatalf("RunPasses: %v", err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			msg, err := brk.Receive(ctx, requests)
			if err != nil {
				t.Fatalf("no task reached the agent: %v", err)
			}
			if got := a2a.DecodeUserProperties(msg.UserProperties).UserUUID; got != tc.want {
				t.Errorf("published userUuid = %q, want %q", got, tc.want)
			}
			if signed := signer.first(); signed == nil || signed.PlatformUserID() != tc.want {
				t.Errorf("signed identity = %+v, want platform user %q", signed, tc.want)
			}
		})
	}
}

type schedulerHostAdapter struct{ entrypoint.LoggingAdapter }

func (*schedulerHostAdapter) HostsScheduler() bool { return true }

func submittingCore(t *testing.T) (*entrypoint.GatewayCore, *broker.DevBroker, broker.Queue) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	core := entrypoint.NewGatewayCore(&schedulerHostAdapter{}, map[string]any{
		"namespace":         "test-ns",
		"gateway_id":        "test-gw",
		"session_service":   map[string]any{"type": "sql", "database_url": "sqlite:///" + t.TempDir() + "/gateway.db"},
		"scheduler_service": map[string]any{"enabled": true},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "test-agent"})
	requests, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(requests, a2a.AgentRequestTopic("test-ns", "test-agent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return core, brk, requests
}
