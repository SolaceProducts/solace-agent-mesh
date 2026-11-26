// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendtest"
)

const modelSpendUserSub = "admin-sub"

type modelSpendRow struct {
	UserID        *string `db:"user_id"`
	UserSub       *string `db:"user_sub"`
	Channel       *string `db:"channel"`
	TriggerKind   *string `db:"trigger_kind"`
	TaskID        *string `db:"task_id"`
	ModelConfigID *string `db:"model_config_id"`
	ModelName     *string `db:"model_name"`
	OutputCost    *int64  `db:"output_cost"`
}

type modelSpendAPI struct {
	*entrypointAPI
	db *store.DB
}

func newModelSpendAPI(t *testing.T, caller *auth.Subject) *modelSpendAPI {
	t.Helper()
	core, _ := newTestCore(t)
	spendtest.StartGovernor(t, core.Services().Broker(), "test-ns", core.DB())
	checked := checkedAgainstSpec(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		core.Handler().ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), caller)))
	}))
	fromTheWebUI := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceWebUI)
		checked.ServeHTTP(w, r)
	})
	return &modelSpendAPI{entrypointAPI: &entrypointAPI{apiClient: &apiClient{t: t, handler: fromTheWebUI, core: core}}, db: core.DB()}
}

func modelSpendCaller(platformUserID string) *auth.Subject {
	return auth.NewTestSubject(auth.SubjectFields{UserID: modelSpendUserSub, PlatformUserID: platformUserID, Authenticated: true, Scopes: []string{"*"}})
}

func (a *modelSpendAPI) modelSpendRows(want int) []modelSpendRow {
	a.t.Helper()
	return spendtest.FiledRows[modelSpendRow](a.t, a.db, string(spend.SurfaceModelTest), want,
		`SELECT user_id, user_sub, channel, trigger_kind, task_id, model_config_id, model_name, output_cost
		 FROM token_spend_ledger WHERE surface = ?`, string(spend.SurfaceModelTest))
}

func (a *modelSpendAPI) modelSpendTest(body map[string]any) ModelTestConnectionResult {
	a.t.Helper()
	return probeResult(a.t, a.call(http.MethodPut, "/models/testConnection", body))
}

func modelSpendHolds(got *string, want string) bool {
	if want == "" {
		return got == nil
	}
	return got != nil && *got == want
}

func modelSpendExpectDirectAction(t *testing.T, row modelSpendRow, wantUserID, wantUserSub string) {
	t.Helper()
	if !modelSpendHolds(row.UserID, wantUserID) || !modelSpendHolds(row.UserSub, wantUserSub) {
		t.Errorf("booked to user %v/%v, want %q/%q", row.UserID, row.UserSub, wantUserID, wantUserSub)
	}
	if !modelSpendHolds(row.TriggerKind, string(spend.TriggerRequest)) || row.TaskID != nil {
		t.Errorf("trigger %v and task %v, want %s and none: a user's direct action", row.TriggerKind, row.TaskID, spend.TriggerRequest)
	}
	if !modelSpendHolds(row.Channel, httputil.SurfaceWebUI) {
		t.Errorf("channel %v, want the client surface %s", row.Channel, httputil.SurfaceWebUI)
	}
}

func modelSpendChatProvider(t *testing.T, content string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion", "created": 1, "model": "gpt-4o",
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 4, "total_tokens": 14},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fakeProvider(t, answerWith(string(body)))
}

func modelSpendDecisionProvider(t *testing.T) string {
	t.Helper()
	return fakeProvider(t, answerWith(`{"model":"jev-1.13.0","answers":{"connection_test":{"type":"noul","noul":0.9}},"usage":{"input_tokens":12,"output_tokens":3}}`))
}

func modelSpendUnsavedChat(apiBase string) map[string]any {
	return map[string]any{"provider": "openai", "modelName": "gpt-4o", "apiBase": apiBase, "authType": "apikey", "authConfig": keyAuth("sk")}
}

func modelSpendUnsavedDecision(apiBase string) map[string]any {
	return map[string]any{"provider": "typesafe", "modelType": "decision", "modelName": "jev-1.13.0", "apiBase": apiBase, "authType": "apikey", "authConfig": keyAuth("ts")}
}

func TestModels_ATestConnectionOfASavedPricedModelIsBookedToTheCallerAndPricedByIt(t *testing.T) {
	platformUserID := idgen.New()
	api := newModelSpendAPI(t, modelSpendCaller(platformUserID))
	saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "priced", "provider": "openai", "modelName": "gpt-4o", "apiBase": modelSpendChatProvider(t, "It is 3pm."),
		"authConfig": keyAuth("sk"), "pricing": map[string]any{"input": "3", "output": "15"},
	}), "models")

	if result := api.modelSpendTest(map[string]any{"modelId": saved.ID}); !result.Success {
		t.Fatalf("connection test %+v, want success", result)
	}
	row := api.modelSpendRows(1)[0]
	modelSpendExpectDirectAction(t, row, platformUserID, "")
	if !modelSpendHolds(row.ModelConfigID, saved.ID) || row.OutputCost == nil {
		t.Errorf("priced by %v at %v, want the saved model %s that served it", row.ModelConfigID, row.OutputCost, saved.ID)
	}
}

func TestModels_AFailedTestConnectionTheProviderBilledIsRecorded(t *testing.T) {
	api := newModelSpendAPI(t, modelSpendCaller(""))
	if result := api.modelSpendTest(modelSpendUnsavedChat(modelSpendChatProvider(t, ""))); result.Success {
		t.Fatal("a probe the model answered with nothing succeeded")
	}
	modelSpendExpectDirectAction(t, api.modelSpendRows(1)[0], "", modelSpendUserSub)
}

func TestModels_ATestConnectionOfAnUnsavedModelIsBookedUnpricedUnderItsModel(t *testing.T) {
	api := newModelSpendAPI(t, modelSpendCaller(""))
	if result := api.modelSpendTest(modelSpendUnsavedChat(modelSpendChatProvider(t, "It is 3pm."))); !result.Success {
		t.Fatalf("connection test %+v, want success", result)
	}
	row := api.modelSpendRows(1)[0]
	modelSpendExpectDirectAction(t, row, "", modelSpendUserSub)
	if row.ModelConfigID != nil || row.ModelName == nil || row.OutputCost != nil {
		t.Errorf("model %v/%v cost %v, want the provider model the probe was sent to, unpriced", row.ModelConfigID, row.ModelName, row.OutputCost)
	}
}

func TestModels_ATestConnectionThatEditsASavedModelsNameIsNotPricedByIt(t *testing.T) {
	api := newModelSpendAPI(t, modelSpendCaller(""))
	saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "saved", "provider": "openai", "modelName": "gpt-4o", "apiBase": modelSpendChatProvider(t, "It is 3pm."),
		"authConfig": keyAuth("sk"), "pricing": map[string]any{"input": "3", "output": "15"},
	}), "models")

	if result := api.modelSpendTest(map[string]any{"modelId": saved.ID, "modelName": "gpt-4o-large"}); !result.Success {
		t.Fatalf("connection test %+v, want success", result)
	}
	if row := api.modelSpendRows(1)[0]; row.ModelConfigID != nil {
		t.Errorf("priced by %v, want unpriced: the probe ran a model the saved price is not for", *row.ModelConfigID)
	}
}

func TestModels_ADecisionModelsTestConnectionIsPricedByItsSavedModelOnlyWhenItRunsIt(t *testing.T) {
	for name, tc := range map[string]struct {
		body        func(savedID, apiBase string) map[string]any
		wantSavedID bool
	}{
		"the saved model as saved": {func(savedID, _ string) map[string]any {
			return map[string]any{"modelId": savedID}
		}, true},
		"the saved model with another model name": {func(savedID, _ string) map[string]any {
			return map[string]any{"modelId": savedID, "modelName": "jev-1.14.0"}
		}, false},
		"an unsaved model": {func(_, apiBase string) map[string]any {
			return modelSpendUnsavedDecision(apiBase)
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			api := newModelSpendAPI(t, modelSpendCaller(""))
			provider := modelSpendDecisionProvider(t)
			saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
				"alias": "jev", "provider": "typesafe", "modelType": "decision", "modelName": "jev-1.13.0",
				"apiBase": provider, "authConfig": keyAuth("ts"),
			}), "models")

			if result := api.modelSpendTest(tc.body(saved.ID, provider)); !result.Success {
				t.Fatalf("connection test %+v, want success", result)
			}
			row := api.modelSpendRows(1)[0]
			modelSpendExpectDirectAction(t, row, "", modelSpendUserSub)
			if tc.wantSavedID && !modelSpendHolds(row.ModelConfigID, saved.ID) {
				t.Errorf("model %v, want the saved decision model %s", row.ModelConfigID, saved.ID)
			}
			if !tc.wantSavedID && row.ModelConfigID != nil {
				t.Errorf("model %v, want none: the probe ran a model the saved price is not for", *row.ModelConfigID)
			}
		})
	}
}

func TestModels_ATestConnectionWithoutAnIdentityRecordsNothing(t *testing.T) {
	for name, body := range map[string]func(t *testing.T) map[string]any{
		"chat": func(t *testing.T) map[string]any {
			return modelSpendUnsavedChat(modelSpendChatProvider(t, "It is 3pm."))
		},
		"decision": func(t *testing.T) map[string]any { return modelSpendUnsavedDecision(modelSpendDecisionProvider(t)) },
	} {
		t.Run(name, func(t *testing.T) {
			api := newModelSpendAPI(t, auth.NewTestSubject(auth.SubjectFields{Authenticated: true, Scopes: []string{"*"}}))
			if result := api.modelSpendTest(body(t)); !result.Success {
				t.Fatalf("connection test %+v, want success", result)
			}
			api.modelSpendRows(0)
		})
	}
}
