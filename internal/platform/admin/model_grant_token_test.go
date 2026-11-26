// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type controlAccessRecorder struct {
	auth.NoopAuditLogger
	mu     sync.Mutex
	events []auth.ControlAccessEvent
}

func (r *controlAccessRecorder) LogControlAccess(_ context.Context, event auth.ControlAccessEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *controlAccessRecorder) take() []auth.ControlAccessEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := r.events
	r.events = nil
	return events
}

func grantTokenPlatform(t *testing.T) (mp *ManageablePlatform, grantID string, refreshes *atomic.Int32, audit *controlAccessRecorder) {
	t.Helper()
	audit = &controlAccessRecorder{}
	refreshes = &atomic.Int32{}
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "expires_in": 3600})
	}))
	t.Cleanup(idp.Close)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": platformCoreDBURL(t)},
		platpkg.ExtensionPoints{},
	)
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithAuditLogger(audit)).Services())
	core.AllowLoopbackGrantEndpoints()
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}

	grant := &store.ModelOAuthGrant{
		Name:                   "Example ID",
		Flow:                   store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer:                 idp.URL + "/",
		TokenEndpoint:          idp.URL + "/oauth/token",
		ClientID:               "desktop",
		APIBase:                "https://llm.example.com",
		AccountSubject:         "user|1",
		RefreshToken:           "refresh-0",
		AccessToken:            "access-0",
		AccessTokenExpiresTime: time.Now().UnixMilli(),
	}
	if err := core.TokenBroker().Create(t.Context(), grant, "desktop"); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return NewManageablePlatform(core, &RouteDeps{}, slog.Default()), grant.ID, refreshes, audit
}

func TestHandleManagement_GrantToken(t *testing.T) {
	t.Parallel()
	mp, grantID, refreshes, audit := grantTokenPlatform(t)
	ctx := t.Context()
	tokenPath := []string{tokenbroker.ControlResource, grantID, tokenbroker.ControlAction}
	modelReader := &auth.Identity{UserID: "awe", Scopes: []string{rbac.ScopeModelConfigRead}}
	wantAudit := func(t *testing.T, userID string, allowed bool) {
		t.Helper()
		events := audit.take()
		want := auth.ControlAccessEvent{UserID: userID, Method: "post", AppName: "platform/" + tokenbroker.ControlResource + "/" + grantID, Allowed: allowed}
		if len(events) != 1 || events[0] != want {
			t.Errorf("audit events = %+v, want only %+v", events, want)
		}
	}

	t.Run("refuses an identity without the model config read scope before any refresh", func(t *testing.T) {
		_, err := mp.HandleManagement(ctx, "post", tokenPath, nil, &auth.Identity{UserID: "skill-reader", Scopes: []string{rbac.ScopeSkillRead}})
		if err == nil || !strings.Contains(err.Error(), "insufficient permissions") {
			t.Errorf("skill-only identity: err = %v, want insufficient permissions", err)
		}
		wantAudit(t, "skill-reader", false)
		if n := refreshes.Load(); n != 0 {
			t.Errorf("the provider saw %d refreshes for a refused request, want 0", n)
		}
	})
	t.Run("refuses an id that is not a UUID before any refresh", func(t *testing.T) {
		_, err := mp.HandleManagement(ctx, "post", []string{tokenbroker.ControlResource, "not-a-uuid", tokenbroker.ControlAction}, nil, modelReader)
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("err = %v, want a ValidationError", err)
		}
		if n := refreshes.Load(); n != 0 {
			t.Errorf("the provider saw %d refreshes for a malformed id, want 0", n)
		}
	})
	t.Run("refuses get", func(t *testing.T) {
		_, err := mp.HandleManagement(ctx, "get", tokenPath, nil, modelReader)
		if err == nil || !strings.Contains(err.Error(), "method not allowed") {
			t.Errorf("err = %v, want method not allowed", err)
		}
	})
	t.Run("reports an unknown grant as not found", func(t *testing.T) {
		unknown := []string{tokenbroker.ControlResource, "00000000-0000-0000-0000-000000000001", tokenbroker.ControlAction}
		if _, err := mp.HandleManagement(ctx, "post", unknown, nil, modelReader); !errors.Is(err, control.ErrResourceNotFound) {
			t.Errorf("err = %v, want control.ErrResourceNotFound", err)
		}
	})
	t.Run("hands a model config reader the grant's token", func(t *testing.T) {
		audit.take()
		result, err := mp.HandleManagement(ctx, "post", tokenPath, json.RawMessage(`{}`), modelReader)
		if err != nil {
			t.Fatalf("HandleManagement: %v", err)
		}
		resp, ok := result.(tokenbroker.TokenResponse)
		if !ok {
			t.Fatalf("result type = %T, want tokenbroker.TokenResponse", result)
		}
		if resp.AccessToken != "access-1" || refreshes.Load() != 1 {
			t.Errorf("token = %q after %d refreshes, want access-1 from one refresh", resp.AccessToken, refreshes.Load())
		}
		wantAudit(t, "awe", true)
	})
}

func TestHandleManagement_SaveAndDeleteGrant(t *testing.T) {
	t.Parallel()
	mp, grantID, _, audit := grantTokenPlatform(t)
	ctx := t.Context()
	grantPath := []string{tokenbroker.ControlResource, grantID}
	modelEditor := &auth.Identity{UserID: "desktop-user", Scopes: []string{rbac.ScopeModelConfigUpdate, rbac.ScopeModelConfigDelete}}
	stored := func(t *testing.T) *store.ModelOAuthGrant {
		t.Helper()
		row, err := mp.TokenBroker().Get(ctx, grantID)
		if err != nil {
			t.Fatalf("get grant: %v", err)
		}
		return row
	}
	existing := stored(t)
	signIn := func(subject, refreshToken string) json.RawMessage {
		body, err := json.Marshal(tokenbroker.SaveRequest{
			Name: existing.Name, Flow: existing.Flow, Issuer: existing.Issuer, TokenEndpoint: existing.TokenEndpoint,
			ClientID: existing.ClientID, APIBase: existing.APIBase, AccountSubject: subject, RefreshToken: refreshToken,
		})
		if err != nil {
			t.Fatalf("encode save request: %v", err)
		}
		return body
	}
	wantAudit := func(t *testing.T, method string, allowed bool) {
		t.Helper()
		events := audit.take()
		want := auth.ControlAccessEvent{UserID: "desktop-user", Method: method, AppName: "platform/" + tokenbroker.ControlResource + "/" + grantID, Allowed: allowed}
		if len(events) != 1 || events[0] != want {
			t.Errorf("audit events = %+v, want only %+v", events, want)
		}
	}

	t.Run("refuses a save by an identity that can only read model credentials", func(t *testing.T) {
		audit.take()
		reader := &auth.Identity{UserID: "desktop-user", Scopes: []string{rbac.ScopeModelConfigRead}}
		if _, err := mp.HandleManagement(ctx, "put", grantPath, signIn("user|1", "refresh-x"), reader); err == nil || !strings.Contains(err.Error(), "insufficient permissions") {
			t.Errorf("err = %v, want insufficient permissions", err)
		}
		wantAudit(t, "put", false)
		if stored(t).RefreshToken == "refresh-x" {
			t.Error("a refused save was stored")
		}
	})
	t.Run("refuses a discard by an identity that can only update models", func(t *testing.T) {
		audit.take()
		updater := &auth.Identity{UserID: "desktop-user", Scopes: []string{rbac.ScopeModelConfigUpdate}}
		if _, err := mp.HandleManagement(ctx, "delete", grantPath, nil, updater); err == nil || !strings.Contains(err.Error(), "insufficient permissions") {
			t.Errorf("err = %v, want insufficient permissions", err)
		}
		wantAudit(t, "delete", false)
		stored(t)
	})
	t.Run("refuses a save whose body is not a grant", func(t *testing.T) {
		if _, err := mp.HandleManagement(ctx, "put", grantPath, json.RawMessage(`{"name":`), modelEditor); err == nil || !strings.Contains(err.Error(), "decode OAuth grant") {
			t.Errorf("err = %v, want a decode error", err)
		}
		if stored(t).RefreshToken != existing.RefreshToken {
			t.Error("a malformed save changed the grant")
		}
	})
	t.Run("saves a sign-in by the grant's account", func(t *testing.T) {
		audit.take()
		if _, err := mp.HandleManagement(ctx, "put", grantPath, signIn("user|1", "refresh-2"), modelEditor); err != nil {
			t.Fatalf("put: %v", err)
		}
		wantAudit(t, "put", true)
		if row := stored(t); row.RefreshToken != "refresh-2" || row.UpdatedBy != "desktop-user" {
			t.Errorf("stored %s by %s, want refresh-2 by desktop-user", row.RefreshToken, row.UpdatedBy)
		}
	})
	t.Run("refuses another account's sign-in while a model names the grant", func(t *testing.T) {
		apiBase := existing.APIBase
		if err := store.NewModelConfigRepo(mp.DB()).Create(ctx, &store.ModelConfiguration{
			Alias: "general", Provider: "openai", ModelName: "free", APIBase: &apiBase,
			ModelAuthType: platpkg.AuthTypeOAuth2Delegated, ModelAuthConfig: `{"grant_id":"` + grantID + `"}`, ModelParams: "{}",
		}); err != nil {
			t.Fatalf("create model: %v", err)
		}
		if _, err := mp.HandleManagement(ctx, "put", grantPath, signIn("user|2", "refresh-3"), modelEditor); !errors.Is(err, tokenbroker.ErrGrantInUse) {
			t.Errorf("err = %v, want tokenbroker.ErrGrantInUse", err)
		}
		if row := stored(t); row.AccountSubject != "user|1" {
			t.Errorf("the grant moved to %s", row.AccountSubject)
		}
	})
	t.Run("refuses a path with an action", func(t *testing.T) {
		if _, err := mp.HandleManagement(ctx, "delete", []string{tokenbroker.ControlResource, grantID, tokenbroker.ControlAction}, nil, modelEditor); err == nil || !strings.Contains(err.Error(), "method not allowed") {
			t.Errorf("err = %v, want method not allowed", err)
		}
	})
	t.Run("refuses to discard a grant a model names", func(t *testing.T) {
		audit.take()
		if _, err := mp.HandleManagement(ctx, "delete", grantPath, nil, modelEditor); !errors.Is(err, tokenbroker.ErrGrantInUse) {
			t.Errorf("err = %v, want tokenbroker.ErrGrantInUse", err)
		}
		wantAudit(t, "delete", true)
		stored(t)
	})
	t.Run("discards the grant once no model names it", func(t *testing.T) {
		models := store.NewModelConfigRepo(mp.DB())
		general, err := models.GetByAlias(ctx, "general")
		if err != nil || general == nil {
			t.Fatalf("get the model naming the grant: %+v, %v", general, err)
		}
		if err := models.Delete(ctx, general.ID); err != nil {
			t.Fatalf("delete the model: %v", err)
		}
		audit.take()
		if _, err := mp.HandleManagement(ctx, "delete", grantPath, nil, modelEditor); err != nil {
			t.Fatalf("delete: %v", err)
		}
		wantAudit(t, "delete", true)
		if _, err := mp.TokenBroker().Get(ctx, grantID); !errors.Is(err, tokenbroker.ErrNotFound) {
			t.Errorf("get after delete = %v, want ErrNotFound", err)
		}
		if _, err := mp.HandleManagement(ctx, "delete", grantPath, nil, modelEditor); err != nil {
			t.Errorf("deleting an absent grant = %v, want success", err)
		}
	})
}
