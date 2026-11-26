// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendtest"
)

type spendingLLM struct {
	mockLLMClient
	served string
}

func (m *spendingLLM) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	resp, err := m.mockLLMClient.ChatCompletion(ctx, req)
	if resp != nil {
		resp.Usage = &llm.Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150}
		resp.Model = m.served
	}
	return resp, err
}

type spendGateway struct {
	core       *entrypoint.GatewayCore
	sessRepo   *store.SessionRepo
	ledger     *platstore.DB
	boundID    string
	defaultLLM *llm.ReconfigurableClient
	broker     *gatedSpendBroker
}

const servedModel = "gpt-served"

func startSpendGateway(t *testing.T, client llm.Client, appConfig map[string]any) *spendGateway {
	t.Helper()
	return newSpendGateway(t, client, appConfig, true)
}

func startStandaloneSpendGateway(t *testing.T, client llm.Client) *spendGateway {
	t.Helper()
	return newSpendGateway(t, client, nil, false)
}

func newSpendGateway(t *testing.T, client llm.Client, appConfig map[string]any, withBindings bool) *spendGateway {
	t.Helper()
	ledger := platstore.TestDB(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	price := pricing.Store(&pricing.ModelPrice{Input: mustAmount(t, "3"), Output: mustAmount(t, "15")})
	var bound platstore.ModelConfiguration
	for _, alias := range []string{"general-bound", generalModelAlias} {
		mc := platstore.ModelConfiguration{
			Alias: alias, Provider: "openai", ModelName: alias, ModelAuthType: "apikey",
			ModelAuthConfig: `{}`, ModelParams: `{}`, CreatedBy: "test", UpdatedBy: "test",
			PriceInput: price.Input, PriceOutput: price.Output,
		}
		if err := platstore.NewModelConfigRepo(ledger).Create(t.Context(), &mc); err != nil {
			t.Fatalf("create model %s: %v", alias, err)
		}
		if bound.ID == "" {
			bound = mc
		}
	}
	spendtest.StartGovernor(t, brk, "test-ns", ledger)

	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatalf("load flags: %v", err)
	}

	adapter := NewHTTPSSEAdapter()
	cfg := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service":       map[string]any{"type": "sql", "database_url": testTemplate.Clone(t)},
		"scheduler_service":     map[string]any{"enabled": true},
	}
	maps.Copy(cfg, appConfig)
	core := entrypoint.NewGatewayCore(adapter, cfg)
	defaultLLM := llm.NewReconfigurableClient(client)
	gated := &gatedSpendBroker{DevBroker: brk, topic: a2a.SpendEventTopic("test-ns")}
	opts := []runtime.Option{
		runtime.WithLogger(slog.Default()),
		runtime.WithDefaultLLM(defaultLLM),
		runtime.WithFeatureRegistry(flags),
	}
	if withBindings {
		opts = append(opts, runtime.WithBindings(bindings.New(
			bindings.WithClientBuilder(func(context.Context, map[string]any, *slog.Logger) (llm.Client, error) { return client, nil }),
			bindings.WithFetcher(func(_ context.Context, alias string) (map[string]any, error) {
				if alias != generalModelAlias {
					return nil, nil
				}
				return map[string]any{"model": bound.ModelName, samapp.ModelConfigIDKey: bound.ID}, nil
			}),
		)))
	}
	rt := runtime.NewRuntime(gated, nil, opts...)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Store().Close() })
	adapter.core = core
	adapter.logger = slog.Default()
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	return &spendGateway{
		core: core, sessRepo: store.NewSessionRepo(core.Store()), ledger: ledger, boundID: bound.ID,
		defaultLLM: defaultLLM, broker: gated,
	}
}

type gatedSpendBroker struct {
	*broker.DevBroker
	topic     string
	held      atomic.Pointer[chan struct{}]
	published atomic.Int64
}

func (b *gatedSpendBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if topic == b.topic {
		if gate := b.held.Load(); gate != nil {
			select {
			case <-*gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := b.DevBroker.PublishGuaranteed(ctx, topic, msg); err != nil {
		return err
	}
	if topic == b.topic {
		b.published.Add(1)
	}
	return nil
}

func (b *gatedSpendBroker) hold() (release func()) {
	gate := make(chan struct{})
	b.held.Store(&gate)
	return sync.OnceFunc(func() { close(gate) })
}

func mustAmount(t *testing.T, s string) pricing.Amount {
	t.Helper()
	a, err := pricing.ParseAmount(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type gatewaySpendRow struct {
	UserID         *string `db:"user_id"`
	UserSub        *string `db:"user_sub"`
	EntrypointName *string `db:"entrypoint_name"`
	Channel        *string `db:"channel"`
	TriggerKind    *string `db:"trigger_kind"`
	Surface        string  `db:"surface"`
	ModelConfigID  *string `db:"model_config_id"`
	UncachedInput  int64   `db:"uncached_input_tokens"`
	Output         int64   `db:"output_tokens"`
	OutputCost     *int64  `db:"output_cost"`
}

func (g *spendGateway) filedRows(t *testing.T, surface string, want int) []gatewaySpendRow {
	t.Helper()
	return spendtest.FiledRows[gatewaySpendRow](t, g.ledger, surface, want,
		`SELECT user_id, user_sub, entrypoint_name, channel, trigger_kind, surface, model_config_id,
			uncached_input_tokens, output_tokens, output_cost
		 FROM token_spend_ledger WHERE surface = ?`, surface)
}

type gatewaySpendDetails struct {
	SessionID     *string `db:"session_id"`
	ModelKey      *string `db:"model_key"`
	SentModelName *string `db:"sent_model_name"`
}

func (g *spendGateway) filedDetails(t *testing.T, surface string, want int) []gatewaySpendDetails {
	t.Helper()
	return spendtest.FiledRows[gatewaySpendDetails](t, g.ledger, surface+" details", want,
		`SELECT d.session_id, d.model_key, d.sent_model_name FROM token_spend_ledger_details d
		 JOIN token_spend_ledger l ON l.id = d.id WHERE l.surface = ?`, surface)
}

func (g *spendGateway) assertBookedToRequester(t *testing.T, row gatewaySpendRow, wantUserID, wantUserSub string) {
	t.Helper()
	if !holdsOrNil(row.UserID, wantUserID) || !holdsOrNil(row.UserSub, wantUserSub) {
		t.Errorf("%s booked to user %v/%v, want %q/%q", row.Surface, row.UserID, row.UserSub, wantUserID, wantUserSub)
	}
	if !holdsOrNil(row.EntrypointName, "test-gw") || !holdsOrNil(row.Channel, httputil.SurfaceWebUI) {
		t.Errorf("%s entered through %v on %v, want test-gw on %s", row.Surface, row.EntrypointName, row.Channel, httputil.SurfaceWebUI)
	}
	if !holdsOrNil(row.TriggerKind, string(spend.TriggerRequest)) {
		t.Errorf("%s has trigger kind %v, want %s: a user's direct action", row.Surface, row.TriggerKind, spend.TriggerRequest)
	}
	if !holdsOrNil(row.ModelConfigID, g.boundID) || row.OutputCost == nil {
		t.Errorf("%s priced by model %v at %v, want the bound configuration %s", row.Surface, row.ModelConfigID, row.OutputCost, g.boundID)
	}
	if row.UncachedInput != 120 || row.Output != 30 {
		t.Errorf("%s tokens = %d/%d, want 120/30", row.Surface, row.UncachedInput, row.Output)
	}
}

func holdsOrNil(got *string, want string) bool {
	if want == "" {
		return got == nil
	}
	return got != nil && *got == want
}

func webUIRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceWebUI)
	return r
}

func TestGatewayAssistantsBookTheirSpendToTheRequestingUser(t *testing.T) {
	g := startSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: `{"message":"done","confidence":0.9}`}, served: servedModel}, nil)

	taskBuilder := newTaskBuilderHandler(g.core, slog.Default())
	taskBuilder.handleChat(httptest.NewRecorder(),
		webUIRequest("POST", "/api/v1/scheduledTasks/builder/chat", `{"message":"a daily report at 9"}`))
	taskBuilder.handleValidateConflict(httptest.NewRecorder(),
		webUIRequest("POST", "/api/v1/scheduledTasks/builder/validate-conflict",
			`{"instructions":"run the report","scheduleType":"cron","scheduleExpression":"0 9 * * *"}`))
	for _, row := range g.filedRows(t, "task_builder", 2) {
		g.assertBookedToRequester(t, row, "", "sam_dev_user")
	}
	for _, d := range g.filedDetails(t, "task_builder", 2) {
		if !holdsOrNil(d.SentModelName, servedModel) {
			t.Errorf("task_builder model = %v, want the model the client served the call with, %s", d.SentModelName, servedModel)
		}
	}

	promptBuilder := &promptBuilderHandler{
		core: g.core, promptRepo: store.NewPromptRepo(g.core.Store()), logger: slog.Default(),
	}
	platformUserID := idgen.New()
	r := webUIRequest("POST", "/api/v1/prompts/chat", `{"message":"a greeting template"}`)
	r = r.WithContext(auth.ContextWithSubject(r.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "sam_dev_user", PlatformUserID: platformUserID})))
	promptBuilder.handleChat(httptest.NewRecorder(), r)
	g.assertBookedToRequester(t, g.filedRows(t, "prompt_builder", 1)[0], platformUserID, "")
}

func TestSessionTitleBooksItsSpendToTheSessionOwner(t *testing.T) {
	g := startSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: "Ottawa Weather"}}, nil)
	sessionID := idgen.New()
	if err := g.sessRepo.Create(t.Context(), &store.Session{ID: sessionID, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	h := &sessionHandler{core: g.core, sessRepo: g.sessRepo, logger: slog.Default()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", h.handleGenerateTitle)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, webUIRequest("POST", "/api/v1/sessions/"+sessionID+"/generateTitle",
		`{"userMessage":"What is the weather in Ottawa?","agentResponse":"Sunny."}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	row := g.filedRows(t, "session_title", 1)[0]
	g.assertBookedToRequester(t, row, "", "sam_dev_user")
	filedSession := g.filedDetails(t, "session_title", 1)[0].SessionID
	if !holdsOrNil(filedSession, sessionID) {
		t.Errorf("title spend filed under session %v, want %s", filedSession, sessionID)
	}
}

func TestATitleGeneratedWhenAChatIsSavedBooksItsSpendToTheSessionOwner(t *testing.T) {
	g := startSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: "Weather Discussion"}}, nil)
	sessionID := idgen.New()
	if err := g.sessRepo.Create(t.Context(), &store.Session{ID: sessionID, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	chatRepo, sseRepo := store.NewChatTaskRepo(g.core.Store()), store.NewSSEEventRepo(g.core.Store())
	sessH := &sessionHandler{core: g.core, sessRepo: g.sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, logger: slog.Default()}
	chatH := &chatTaskHandler{core: g.core, sessRepo: g.sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, sessH: sessH, logger: slog.Default()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/chatTasks", chatH.handleSave)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, webUIRequest("POST", "/api/v1/sessions/"+sessionID+"/chatTasks", saveChatTaskBody(t, true)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	g.assertBookedToRequester(t, g.filedRows(t, "session_title", 1)[0], "", "sam_dev_user")
}

func TestATitleFromADedicatedTitleModelIsFiledUnpricedUnderThatModel(t *testing.T) {
	const titleModel = "title-model"
	g := startSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: "Ottawa Weather"}},
		map[string]any{"model": map[string]any{"llm_service_title_model_name": titleModel}})
	sessionID := idgen.New()
	if err := g.sessRepo.Create(t.Context(), &store.Session{ID: sessionID, UserID: "sam_dev_user"}); err != nil {
		t.Fatal(err)
	}
	h := &sessionHandler{core: g.core, sessRepo: g.sessRepo, logger: slog.Default()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sessions/{id}/generateTitle", h.handleGenerateTitle)

	mux.ServeHTTP(httptest.NewRecorder(), webUIRequest("POST", "/api/v1/sessions/"+sessionID+"/generateTitle",
		`{"userMessage":"What is the weather in Ottawa?","agentResponse":"Sunny."}`))

	row := g.filedRows(t, "session_title", 1)[0]
	if row.ModelConfigID != nil || row.OutputCost != nil {
		t.Errorf("dedicated title model row priced by %v at %v, want unpriced", row.ModelConfigID, row.OutputCost)
	}
	if d := g.filedDetails(t, "session_title", 1)[0]; !holdsOrNil(d.SentModelName, "openai/"+titleModel) {
		t.Errorf("dedicated title model row model = %v, want openai/%s", d.SentModelName, titleModel)
	}
}

func (g *spendGateway) taskBuilderChat(t *testing.T) {
	t.Helper()
	newTaskBuilderHandler(g.core, slog.Default()).handleChat(httptest.NewRecorder(),
		webUIRequest("POST", "/api/v1/scheduledTasks/builder/chat", `{"message":"a daily report at 9"}`))
}

func TestAStandaloneGatewayPricesItsDefaultModelByTheConfigurationItWasSeededFrom(t *testing.T) {
	client := &spendingLLM{mockLLMClient: mockLLMClient{response: `{"message":"done","confidence":0.9}`}}
	g := startStandaloneSpendGateway(t, client)
	g.defaultLLM.ReconfigureFrom(client, g.boundID)

	g.taskBuilderChat(t)

	g.assertBookedToRequester(t, g.filedRows(t, "task_builder", 1)[0], "", "sam_dev_user")
	if d := g.filedDetails(t, "task_builder", 1)[0]; !holdsOrNil(d.ModelKey, generalModelAlias) {
		t.Errorf("default model filed under key %v, want %s", d.ModelKey, generalModelAlias)
	}
}

func TestAStandaloneGatewayFilesAnEnvironmentDefaultModelUnpriced(t *testing.T) {
	g := startStandaloneSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: `{"message":"done","confidence":0.9}`}, served: servedModel})

	g.taskBuilderChat(t)

	row := g.filedRows(t, "task_builder", 1)[0]
	if row.ModelConfigID != nil || row.OutputCost != nil {
		t.Errorf("environment default priced by %v at %v, want unpriced: it came from no configuration", row.ModelConfigID, row.OutputCost)
	}
	if d := g.filedDetails(t, "task_builder", 1)[0]; d.ModelKey != nil {
		t.Errorf("environment default filed under key %q, want none: the configuration named general is not its source", *d.ModelKey)
	}
}

func TestStoppingTheGatewayFlushesTheSpendItRecorded(t *testing.T) {
	g := startSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: `{"message":"done","confidence":0.9}`}}, nil)
	release := g.broker.hold()
	t.Cleanup(release)

	g.taskBuilderChat(t)
	time.AfterFunc(50*time.Millisecond, release)
	if err := g.core.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if got := g.broker.published.Load(); got != 1 {
		t.Fatalf("%d spend events published when Stop returned, want the one recorded before it", got)
	}
	g.filedRows(t, "task_builder", 1)
}

func TestAStoppedGatewayHandsOutNoSpendPublisherAndFilesNothingAfterTheStop(t *testing.T) {
	g := startSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: `{"message":"done"}`}}, nil)
	client := resolveGeneralModel(t.Context(), g.core).client
	if g.core.SpendPublisher() == nil {
		t.Fatal("a running gateway has no spend publisher")
	}

	if err := g.core.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if g.core.SpendPublisher() != nil {
		t.Error("a stopped gateway handed out a spend publisher")
	}
	attribution := g.core.RequestSpend(nil, "sam_dev_user", httputil.SurfaceWebUI,
		spend.SurfaceTaskBuilder, generalModelAlias, "", "")
	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: attribution}); err != nil {
		t.Fatalf("call after stop: %v", err)
	}

	g.filedRows(t, "task_builder", 0)
}

func TestARestartedGatewayRecordsSpendAgain(t *testing.T) {
	g := startSpendGateway(t, &spendingLLM{mockLLMClient: mockLLMClient{response: `{"message":"done","confidence":0.9}`}}, nil)
	if err := g.core.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	g.taskBuilderChat(t)
	if err := g.core.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if err := g.core.Start(t.Context()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(func() { _ = g.core.Stop(context.Background()) })
	g.taskBuilderChat(t)

	for _, row := range g.filedRows(t, "task_builder", 2) {
		g.assertBookedToRequester(t, row, "", "sam_dev_user")
	}
}
