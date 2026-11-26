// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/freellm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/solaceid"
)

const (
	grantTestNamespace = "grant-ns"
	grantModelAlias    = "free"
	tokenCanary        = "tokcanary"
)

type rotatingIDP struct {
	mu     sync.Mutex
	issued int
	refuse bool
}

func (p *rotatingIDP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "refresh_token" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if p.refuse {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	p.issued++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  fmt.Sprintf("%s-access-%d", tokenCanary, p.issued),
		"refresh_token": fmt.Sprintf("%s-refresh-%d", tokenCanary, p.issued),
		"expires_in":    3600,
	})
}

type strictProvider struct {
	mu       sync.Mutex
	rejected map[string]bool
	seen     []string
}

func (p *strictProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	p.mu.Lock()
	p.seen = append(p.seen, token)
	rejected := p.rejected[token]
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if rejected {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid token","type":"authentication_error"}}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion", "created": 0, "model": "free-model",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "ok"}}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func (p *strictProvider) tokens() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

type grantStack struct {
	core     *platform.PlatformCore
	grantID  string
	idp      *rotatingIDP
	provider *strictProvider
	registry interface {
		ResolveOrFetch(ctx context.Context, alias string) (llm.Client, error)
	}
	logs *syncBuffer
	brk  broker.Broker
}

func newGrantStack(t *testing.T) *grantStack {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	ctx := t.Context()
	idp := &rotatingIDP{}
	idpServer := httptest.NewServer(idp)
	t.Cleanup(idpServer.Close)
	provider := &strictProvider{rejected: map[string]bool{}}
	providerServer := httptest.NewServer(provider)
	t.Cleanup(providerServer.Close)

	brk := broker.NewDevBroker(broker.WithDevBrokerLogger(logger))
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	deps := &entplatform.RouteDeps{}
	core := platform.NewPlatformCore(map[string]any{
		"namespace":    grantTestNamespace,
		"fastapi_host": "127.0.0.1",
		"fastapi_port": 0,
		"database_url": store.TestDBURL(t),
	}, platform.ExtensionPoints{
		RegisterRoutes: func(mux *http.ServeMux, core *platform.PlatformCore) {
			entplatform.RegisterRoutesWithDeps(mux, core, deps)
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(logger)).Services())
	core.AllowLoopbackGrantEndpoints()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("platform init: %v", err)
	}
	t.Cleanup(func() { _ = core.DB().Close() })

	grant := &store.ModelOAuthGrant{
		Name:                   "Example ID",
		Flow:                   store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer:                 idpServer.URL + "/",
		TokenEndpoint:          idpServer.URL + "/oauth/token",
		ClientID:               "desktop",
		APIBase:                providerServer.URL,
		AccountSubject:         "user|1",
		RefreshToken:           tokenCanary + "-refresh-0",
		AccessToken:            tokenCanary + "-access-0",
		AccessTokenExpiresTime: time.Now().UnixMilli(),
	}
	if err := core.TokenBroker().Create(ctx, grant, "desktop"); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	apiBase := providerServer.URL + "/v1"
	if err := store.NewModelConfigRepo(core.DB()).Create(ctx, &store.ModelConfiguration{
		Alias:           grantModelAlias,
		Provider:        "openai",
		ModelName:       "free-model",
		APIBase:         &apiBase,
		ModelAuthType:   platform.AuthTypeOAuth2Delegated,
		ModelAuthConfig: fmt.Sprintf(`{"type":"oauth2_delegated","grant_id":%q}`, grant.ID),
		ModelParams:     `{}`,
		CreatedBy:       "desktop",
		UpdatedBy:       "desktop",
	}); err != nil {
		t.Fatalf("create model: %v", err)
	}

	ctrlRT := runtime.NewRuntime(brk, nil, runtime.WithLogger(logger))
	ctrlSvc := control.NewService(ctrlRT, grantTestNamespace)
	ctrlRT.Register("control", ctrlSvc)
	ctrlRT.Register("platform", entplatform.NewManageablePlatform(core, deps, logger))
	if err := ctrlSvc.Init(ctx, nil); err != nil {
		t.Fatalf("control init: %v", err)
	}
	if err := ctrlSvc.Start(ctx); err != nil {
		t.Fatalf("control start: %v", err)
	}
	t.Cleanup(func() { _ = ctrlSvc.Stop(context.Background()) })

	registry := bootstrap.NewBindingsRegistry(brk, grantTestNamespace, logger)
	t.Cleanup(registry.Close)

	return &grantStack{core: core, grantID: grant.ID, idp: idp, provider: provider, registry: registry, logs: logs, brk: brk}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (s *grantStack) chat(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := s.registry.ResolveOrFetch(ctx, grantModelAlias)
	if err != nil {
		t.Fatalf("resolve %s: %v", grantModelAlias, err)
	}
	_, err = client.ChatCompletion(ctx, &llm.ChatRequest{Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}})
	return err
}

func (s *grantStack) assertLogsOmitTokens(t *testing.T) {
	t.Helper()
	s.logs.mu.Lock()
	defer s.logs.mu.Unlock()
	if !strings.Contains(s.logs.buf.String(), "grantID=") {
		t.Fatal("no grant log line was captured, so the check below would pass vacuously")
	}
	if strings.Contains(s.logs.buf.String(), tokenCanary) {
		t.Errorf("a token value reached the logs")
	}
}

func TestModelGrant_AgentCallsWithAPlatformRefreshedToken(t *testing.T) {
	s := newGrantStack(t)
	s.provider.rejected[tokenCanary+"-access-1"] = true

	if err := s.chat(t); err != nil {
		t.Fatalf("chat: %v", err)
	}

	want := []string{tokenCanary + "-access-1", tokenCanary + "-access-2"}
	if got := s.provider.tokens(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("provider saw %v, want %v", got, want)
	}
	row, err := s.core.TokenBroker().Get(t.Context(), s.grantID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}
	if row.RefreshToken != tokenCanary+"-refresh-2" || row.Version != 5 {
		t.Errorf("stored refresh %q at v%d; want the second rotation at version 5 (two refreshes, each claimed then saved)", row.RefreshToken, row.Version)
	}

	if err := s.chat(t); err != nil {
		t.Fatalf("second chat: %v", err)
	}
	if s.idp.issued != 2 {
		t.Errorf("refreshes = %d; a token with time left must be reused", s.idp.issued)
	}
	s.assertLogsOmitTokens(t)
}

func TestModelGrant_ARefusedRefreshAsksTheUserToGrantAgain(t *testing.T) {
	s := newGrantStack(t)
	s.idp.refuse = true

	err := s.chat(t)
	var llmErr *llm.Error
	if !errors.As(err, &llmErr) || !llmErr.IsReauthorizationRequired() {
		t.Fatalf("err = %v, want the reauthorization-required error", err)
	}
	if got := llm.UserFacingErrorMessage(err); got != llm.ReauthorizationRequiredUserMessage {
		t.Errorf("chat message = %q", got)
	}
	if len(s.provider.tokens()) != 0 {
		t.Errorf("provider saw %v; nothing may reach it without a token", s.provider.tokens())
	}
	row, getErr := s.core.TokenBroker().Get(t.Context(), s.grantID)
	if getErr != nil {
		t.Fatalf("get grant: %v", getErr)
	}
	if row.Status != store.ModelOAuthGrantStatusReauthorizationRequired {
		t.Errorf("status = %q, want reauthorization_required", row.Status)
	}
	s.assertLogsOmitTokens(t)
}

func TestModelGrant_AMissingGrantAsksTheUserToGrantAgain(t *testing.T) {
	s := newGrantStack(t)
	if err := s.core.TokenBroker().Delete(t.Context(), s.grantID); err != nil {
		t.Fatalf("delete grant: %v", err)
	}

	err := s.chat(t)
	var llmErr *llm.Error
	if !errors.As(err, &llmErr) || !llmErr.IsReauthorizationRequired() {
		t.Fatalf("err = %v, want the reauthorization-required error", err)
	}
	if got := llm.UserFacingErrorMessage(err); got != llm.ReauthorizationRequiredUserMessage {
		t.Errorf("chat message = %q", got)
	}
}

func TestModelGrant_TheControlPlaneSavesAndDeletesAGrant(t *testing.T) {
	s := newGrantStack(t)
	grants := tokenbroker.NewControlPlaneGrants(s.brk, grantTestNamespace)
	row, err := s.core.TokenBroker().Get(t.Context(), s.grantID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}
	signIn := func(subject, refreshToken string) tokenbroker.SaveRequest {
		return tokenbroker.SaveRequest{
			Name: row.Name, Flow: row.Flow, Issuer: row.Issuer, TokenEndpoint: row.TokenEndpoint, ClientID: row.ClientID,
			APIBase: row.APIBase, AccountSubject: subject, RefreshToken: refreshToken,
		}
	}

	if err = grants.Save(t.Context(), s.grantID, signIn("user|1", tokenCanary+"-refresh-again")); err != nil {
		t.Fatalf("save the same account's sign-in: %v", err)
	}
	saved, err := s.core.TokenBroker().Get(t.Context(), s.grantID)
	if err != nil || saved.RefreshToken != tokenCanary+"-refresh-again" {
		t.Fatalf("after saving: %+v, %v; want the new refresh token", saved, err)
	}

	err = grants.Save(t.Context(), s.grantID, signIn("user|2", tokenCanary+"-refresh-other"))
	if !errors.Is(err, tokenbroker.ErrGrantInUse) {
		t.Fatalf("save another account's sign-in = %v, want ErrGrantInUse", err)
	}

	if err = grants.Delete(t.Context(), s.grantID); !errors.Is(err, tokenbroker.ErrGrantInUse) {
		t.Fatalf("delete a grant a model names = %v, want ErrGrantInUse", err)
	}
	models := store.NewModelConfigRepo(s.core.DB())
	model, err := models.GetByAlias(t.Context(), grantModelAlias)
	if err != nil || model == nil {
		t.Fatalf("get the model naming the grant: %+v, %v", model, err)
	}
	if err = models.Delete(t.Context(), model.ID); err != nil {
		t.Fatalf("delete the model: %v", err)
	}
	if err = grants.Delete(t.Context(), s.grantID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err = s.core.TokenBroker().Get(t.Context(), s.grantID); !errors.Is(err, tokenbroker.ErrNotFound) {
		t.Fatalf("after delete: %v, want ErrNotFound", err)
	}
	if err = grants.Delete(t.Context(), s.grantID); err != nil {
		t.Fatalf("delete an absent grant: %v, want success", err)
	}
}

func TestModelGrant_TheFreeModelsGrantIsSavedRefreshedAndDeleted(t *testing.T) {
	s := newGrantStack(t)
	ctx := t.Context()
	seeded, err := s.core.TokenBroker().Get(ctx, s.grantID)
	if err != nil {
		t.Fatalf("get the seeded grant: %v", err)
	}
	free := freellm.NewPlatform(
		freellm.Config{BaseURL: seeded.APIBase, GeneralModel: "free-model", PlanningModel: "free-model", Audience: "https://llm.example"},
		solaceid.Config{Issuer: seeded.Issuer, ClientID: seeded.ClientID},
		func(context.Context) (string, error) { return seeded.TokenEndpoint, nil },
		tokenbroker.NewControlPlaneGrants(s.brk, grantTestNamespace),
		tokenbroker.NewControlPlaneTokens(s.brk, grantTestNamespace),
	)

	signedIn := activation.SignInResult{Subject: "user|ada", Email: "ada@example.com", Tokens: activation.FreeModelTokens{
		RefreshToken: tokenCanary + "-refresh-signin",
		AccessToken:  tokenCanary + "-access-signin",
		ExpiresAt:    time.Now().Add(time.Hour),
	}}
	if err = free.SaveGrant(ctx, signedIn); err != nil {
		t.Fatalf("save the grant a sign-in produced: %v", err)
	}
	saved, err := s.core.TokenBroker().Get(ctx, freellm.GrantID)
	if err != nil || saved.AccountSubject != signedIn.Subject || saved.AccessToken != signedIn.Tokens.AccessToken {
		t.Fatalf("after saving: %+v, %v; want the sign-in's grant", saved, err)
	}

	if err = free.Refresh(ctx); err != nil {
		t.Fatalf("forced refresh: %v", err)
	}
	refreshed, err := s.core.TokenBroker().Get(ctx, freellm.GrantID)
	if err != nil || refreshed.AccessToken == signedIn.Tokens.AccessToken || refreshed.RefreshToken == signedIn.Tokens.RefreshToken {
		t.Fatalf("after the forced refresh: %+v, %v; want tokens the sign-in service issued", refreshed, err)
	}

	if err = free.DeleteGrant(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err = s.core.TokenBroker().Get(ctx, freellm.GrantID); !errors.Is(err, tokenbroker.ErrNotFound) {
		t.Fatalf("after delete: %v, want ErrNotFound", err)
	}
	s.assertLogsOmitTokens(t)
}
