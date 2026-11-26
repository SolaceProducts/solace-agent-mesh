// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type ledgerRow struct {
	UserID        *string `db:"user_id"`
	UserSub       *string `db:"user_sub"`
	Channel       *string `db:"channel"`
	TriggerKind   *string `db:"trigger_kind"`
	TaskID        *string `db:"task_id"`
	Surface       string  `db:"surface"`
	ModelConfigID *string `db:"model_config_id"`
	ModelName     *string `db:"model_name"`
	OutputCost    *int64  `db:"output_cost"`
}

func filedRows(t *testing.T, db *store.DB, surface spend.Surface, want int) []ledgerRow {
	t.Helper()
	return spendtest.FiledRows[ledgerRow](t, db, string(surface), want,
		`SELECT user_id, user_sub, channel, trigger_kind, task_id, surface, model_config_id, model_name, output_cost
		 FROM token_spend_ledger WHERE surface = ?`, string(surface))
}

func holds(got *string, want string) bool {
	if want == "" {
		return got == nil
	}
	return got != nil && *got == want
}

func assertDirectAction(t *testing.T, row ledgerRow, wantUserID, wantUserSub string) {
	t.Helper()
	if !holds(row.UserID, wantUserID) || !holds(row.UserSub, wantUserSub) {
		t.Errorf("%s booked to user %v/%v, want %q/%q", row.Surface, row.UserID, row.UserSub, wantUserID, wantUserSub)
	}
	if !holds(row.TriggerKind, string(spend.TriggerRequest)) || row.TaskID != nil {
		t.Errorf("%s has trigger %v and task %v, want %s and none: a user's direct action", row.Surface, row.TriggerKind, row.TaskID, spend.TriggerRequest)
	}
	if !holds(row.Channel, httputil.SurfaceWebUI) {
		t.Errorf("%s channel %v, want the client surface %s", row.Surface, row.Channel, httputil.SurfaceWebUI)
	}
}

type spendPlatform struct {
	core     *platpkg.PlatformCore
	provider *httptest.Server
	admin    *samauth.Identity
}

func billingProvider(t *testing.T, content, finishReason string) *httptest.Server {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "created": 1, "model": "gpt-4o",
		"choices": []map[string]any{{
			"index": 0, "finish_reason": finishReason,
			"message": map[string]any{"role": "assistant", "content": content},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 4, "total_tokens": 14},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

const generatedAgent = `{"name":"Sales Helper","description":"Helps with sales"}`

func startSpendPlatform(t *testing.T, provider *httptest.Server) *spendPlatform {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	reg := features.NewRegistry()

	p := &spendPlatform{provider: provider, admin: &samauth.Identity{
		UserID: "admin-sub", Scopes: []string{"*:*:*"},
		Claims: map[string]any{trust.ClaimPlatformUserID: idgen.New()},
	}}
	p.core = platpkg.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": platformCoreDBURL(t),
	}, platpkg.ExtensionPoints{
		RegisterRoutes: func(mux *http.ServeMux, core *platpkg.PlatformCore) {
			RegisterRoutesWithDeps(mux, core, &RouteDeps{FeatureRegistry: reg})
		},
		AuthMiddleware: NewAuthMiddleware(&countingAuthenticator{id: p.admin}, nil, nil, slog.Default()),
	})
	p.core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)).Services())
	if err := p.core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform: %v", err)
	}
	if err := p.core.Start(t.Context()); err != nil {
		t.Fatalf("start platform: %v", err)
	}
	t.Cleanup(func() { _ = p.core.Stop(context.Background()) })
	return p
}

func (p *spendPlatform) adminRowID() string {
	return p.admin.Claims[trust.ClaimPlatformUserID].(string)
}

func (p *spendPlatform) pointGeneralAtTheProvider(t *testing.T) *ModelConfigResponse {
	t.Helper()
	svc := NewModelConfigService(store.NewModelConfigRepo(p.core.DB()), slog.Default())
	general, err := svc.GetByAlias(t.Context(), aiAssistantModelAlias)
	if err != nil || general == nil {
		t.Fatalf("seeded general model = %v, %v", general, err)
	}
	provider, model, base, authType := "openai", "gpt-4o", p.provider.URL, "apikey"
	saved, err := svc.Update(t.Context(), general.ID, &UpdateModelConfigRequest{
		Provider: &provider, ModelName: &model, APIBase: &base, AuthType: &authType,
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk"},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	price := pricing.Store(&pricing.ModelPrice{Input: mustSpendAmount(t, "3"), Output: mustSpendAmount(t, "15")})
	if _, err := p.core.DB().ExecContext(t.Context(),
		`UPDATE model_configurations SET price_input = ?, price_output = ? WHERE id = ?`, *price.Input, *price.Output, saved.ID); err != nil {
		t.Fatal(err)
	}
	return saved
}

func (p *spendPlatform) post(t *testing.T, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceWebUI)
	rec := httptest.NewRecorder()
	p.core.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func mustSpendAmount(t *testing.T, s string) pricing.Amount {
	t.Helper()
	a, err := pricing.ParseAmount(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestThePlatformsRoutesBookAnAdminsLLMCallsToThemPricedByTheConfigurationThatServedThem(t *testing.T) {
	p := startSpendPlatform(t, billingProvider(t, generatedAgent, "stop"))
	general := p.pointGeneralAtTheProvider(t)

	for path, body := range map[string]string{
		"/api/v1/platform/aiAssistant/generateAgent": `{"description":"a test agent that does sales things"}`,
	} {
		if status := p.post(t, path, body); status != http.StatusOK {
			t.Fatalf("POST %s = %d, want 200", path, status)
		}
	}

	for _, surface := range []spend.Surface{spend.SurfaceAgentGenerator} {
		row := filedRows(t, p.core.DB(), surface, 1)[0]
		assertDirectAction(t, row, p.adminRowID(), "")
		if !holds(row.ModelConfigID, general.ID) || row.OutputCost == nil {
			t.Errorf("%s priced by %v at %v, want the configuration %s that served it", surface, row.ModelConfigID, row.OutputCost, general.ID)
		}
	}
}

func TestAGeneratorCallTheProviderBilledIsRecordedWhenItsOutputIsUnusable(t *testing.T) {
	for name, output := range map[string]struct{ content, finishReason string }{
		"not JSON":  {content: "It is 3pm.", finishReason: "stop"},
		"truncated": {content: `{"name":"Sales Hel`, finishReason: "length"},
	} {
		t.Run(name, func(t *testing.T) {
			p := startSpendPlatform(t, billingProvider(t, output.content, output.finishReason))
			general := p.pointGeneralAtTheProvider(t)

			status := p.post(t, "/api/v1/platform/aiAssistant/generateAgent", `{"description":"a test agent that does sales things"}`)

			if status == http.StatusOK {
				t.Fatal("generation succeeded, want it to fail on the unusable output")
			}
			row := filedRows(t, p.core.DB(), spend.SurfaceAgentGenerator, 1)[0]
			assertDirectAction(t, row, p.adminRowID(), "")
			if !holds(row.ModelConfigID, general.ID) || row.OutputCost == nil {
				t.Errorf("priced by %v at %v, want the configuration %s that served it", row.ModelConfigID, row.OutputCost, general.ID)
			}
		})
	}
}

type spendLedger struct {
	db        *store.DB
	publisher *spend.Publisher
}

func newSpendLedger(t *testing.T) *spendLedger {
	t.Helper()
	db := testOpenDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	spendtest.StartGovernor(t, brk, "test-ns", db)
	publisher := spend.NewPublisher(brk, "test-ns", slog.Default())
	t.Cleanup(func() { _ = publisher.Close(context.Background()) })
	return &spendLedger{db: db, publisher: publisher}
}

func (l *spendLedger) connectionHandler() *modelConfigHandler {
	svc := NewModelConfigService(store.NewModelConfigRepo(l.db), slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	return &modelConfigHandler{svc: svc, logger: slog.Default(), spendPublisher: l.publisher, budgetGate: budgettest.NewGate()}
}

func TestAConnectionTestOfASavedConfigIsPricedByItOnlyWhileItRunsTheSavedProviderAndModel(t *testing.T) {
	t.Parallel()
	l := newSpendLedger(t)
	h := l.connectionHandler()
	saved, err := h.svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias: "saved", Provider: "openai", ModelName: "gpt-4o", APIBase: "http://127.0.0.1:1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk"},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		provider, model string
		priced          bool
	}{
		"as saved":                            {priced: true},
		"naming the saved provider and model": {provider: "openai", model: "gpt-4o", priced: true},
		"with another provider":               {provider: "anthropic", model: "gpt-4o"},
		"with another model":                  {provider: "openai", model: "gpt-4o-large"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			probe, err := h.buildProbeConfig(t.Context(), &ModelConfigTestRequest{ModelID: saved.ID, Provider: c.provider, ModelName: c.model}, nil)
			if err != nil {
				t.Fatalf("build probe: %v", err)
			}
			if priced := probe.configID == saved.ID; priced != c.priced || (!priced && probe.configID != "") {
				t.Errorf("probe priced by %q, want saved config priced = %t", probe.configID, c.priced)
			}
		})
	}
}
