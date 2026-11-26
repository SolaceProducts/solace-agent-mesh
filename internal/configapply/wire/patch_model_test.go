// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var testModelKind = generic.Kind{
	Name:             "model",
	CreateProto:      CreateModelRequest{},
	UpdateProto:      UpdateModelRequest{},
	ResponseProto:    ModelResponse{},
	NameFromCreate:   func(v any) string { return v.(CreateModelRequest).Alias },
	NameFromResponse: func(v any) string { return v.(ModelResponse).Alias },
	IDFromResponse:   func(v any) string { return v.(ModelResponse).ID },
}

const (
	openAIBase   = "https://api.openai.com/v1"
	customBase   = "https://llm.example.com/v1"
	firstAPIKey  = "sk-first-key"
	rotatedKey   = "sk-rotated-key"
	clientSecret = "oauth-client-secret"

	workloadClientID = "0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"
	workloadTenantID = "0190f5ea-5e6f-7a8b-9c0d-1e2f3a4b5c6d"
)

type modelPlatform struct {
	t      *testing.T
	srv    *httptest.Server
	models *entplatform.ModelConfigService
	bodies []string
}

func newModelPlatform(t *testing.T) *modelPlatform {
	t.Helper()
	t.Setenv("SAM_DATA_DIR", t.TempDir())
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatalf("load feature flags: %v", err)
	}
	deps := &entplatform.RouteDeps{FeatureRegistry: reg, IsRBACAuthority: true}
	core := platform.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": testTemplate.Clone(t)},
		platform.ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, c *platform.PlatformCore) {
				entplatform.RegisterRoutesWithDeps(mux, c, deps)
				apiv2.RegisterRoutes(mux, deps, c, c.Logger())
			},
		},
	)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	srv := httptest.NewServer(auth.DevMiddleware()(core.Handler()))
	t.Cleanup(srv.Close)
	p := &modelPlatform{t: t, srv: srv, models: deps.ModelConfigSvc}
	t.Cleanup(p.assertNoSentinelSent)
	return p
}

func (p *modelPlatform) call(method, path string, body any) (int, []byte) {
	p.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			p.t.Fatalf("encode %s %s: %v", method, path, err)
		}
		p.bodies = append(p.bodies, string(raw))
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(p.t.Context(), method, p.srv.URL+"/api/v2/platform"+path, reader)
	if err != nil {
		p.t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.srv.Client().Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, out
}

func (p *modelPlatform) decode(status int, raw []byte, want int) ModelResponse {
	p.t.Helper()
	if status != want {
		p.t.Fatalf("status %d, want %d: %s", status, want, raw)
	}
	var env struct {
		Data ModelResponse `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		p.t.Fatalf("decode model: %v: %s", err, raw)
	}
	return env.Data
}

func (p *modelPlatform) create(req CreateModelRequest) ModelResponse {
	p.t.Helper()
	status, raw := p.call(http.MethodPost, "/models", req)
	return p.decode(status, raw, http.StatusCreated)
}

func (p *modelPlatform) get(id string) ModelResponse {
	p.t.Helper()
	status, raw := p.call(http.MethodGet, "/models/"+id, nil)
	return p.decode(status, raw, http.StatusOK)
}

func (p *modelPlatform) apply(id string, desired CreateModelRequest) string {
	p.t.Helper()
	plan := p.plan(id, desired)
	if len(plan.Updates) != 1 {
		p.t.Fatalf("plan has %d updates and %d unchanged, want one update", len(plan.Updates), len(plan.Unchanged))
	}
	return p.patch(id, plan.Updates[0].Patch.Interface())
}

func (p *modelPlatform) patch(id string, body any) string {
	p.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		p.t.Fatalf("encode patch: %v", err)
	}
	status, out := p.call(http.MethodPatch, "/models/"+id, body)
	p.decode(status, out, http.StatusOK)
	return string(raw)
}

func (p *modelPlatform) plan(id string, desired CreateModelRequest) *generic.KindPlan {
	p.t.Helper()
	actual := p.get(id)
	plan, err := generic.Plan(
		[]generic.DesiredEntry{{Name: desired.Alias, Create: desired}},
		[]reflect.Value{reflect.ValueOf(actual)},
		testModelKind,
	)
	if err != nil {
		p.t.Fatalf("plan: %v", err)
	}
	return plan
}

func (p *modelPlatform) assertConverged(id string, desired CreateModelRequest) {
	p.t.Helper()
	plan := p.plan(id, desired)
	if len(plan.Unchanged) != 1 {
		body := ""
		if len(plan.Updates) == 1 {
			raw, _ := json.Marshal(plan.Updates[0].Patch.Interface())
			body = string(raw)
		}
		p.t.Errorf("the plan after the update still wants to change the model: %s", body)
	}
}

func (p *modelPlatform) storedAuth(id string) map[string]any {
	p.t.Helper()
	_, stored, err := p.models.GetAuthConfigCleartext(p.t.Context(), id)
	if err != nil {
		p.t.Fatalf("read stored auth config: %v", err)
	}
	return stored
}

func (p *modelPlatform) assertNoSentinelSent() {
	for _, body := range p.bodies {
		if strings.Contains(body, generic.RedactedSentinel) {
			p.t.Errorf("a request carried %s: %s", generic.RedactedSentinel, body)
		}
	}
}

func baseModel() CreateModelRequest {
	return CreateModelRequest{
		Alias:          "support",
		Provider:       "openai",
		ModelName:      "gpt-4o",
		APIBase:        customBase,
		AuthConfig:     map[string]any{"type": "apikey", "api_key": firstAPIKey},
		ModelParams:    map[string]any{"temperature": 0.2, "top_p": 0.9},
		Description:    "Fast model for support replies",
		MaxInputTokens: new(128000),
	}
}

func assertBody(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("body is not JSON: %s", got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %s", want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("PATCH body\n got: %s\nwant: %s", got, want)
	}
}

func TestModelPatch_SendsOnlyTheChangeAndKeepsTheRest(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.Description = "Model for support replies"
	desired.ModelType = "decision"
	body := p.apply(model.ID, desired)

	assertBody(t, body, `{"description":"Model for support replies","authConfig":{"api_key":"`+firstAPIKey+`"}}`)
	got := p.get(model.ID)
	if got.ModelType != "chat" {
		t.Errorf("modelType = %q; it is fixed at create and must never be sent", got.ModelType)
	}
	if got.APIBase == nil || *got.APIBase != customBase || got.MaxInputTokens == nil || *got.MaxInputTokens != 128000 {
		t.Errorf("fields left out of the body changed: apiBase %v, maxInputTokens %v", got.APIBase, got.MaxInputTokens)
	}
	if stored := p.storedAuth(model.ID); stored["api_key"] != firstAPIKey {
		t.Errorf("stored api_key = %v, want it kept", stored["api_key"])
	}
	desired.ModelType = ""
	p.assertConverged(model.ID, desired)
}

func TestModelPatch_ClearsRemovedFields(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.Description = ""
	desired.MaxInputTokens = nil
	body := p.apply(model.ID, desired)

	assertBody(t, body, `{"description":null,"maxInputTokens":null,"authConfig":{"api_key":"`+firstAPIKey+`"}}`)
	got := p.get(model.ID)
	if got.Description != nil || got.MaxInputTokens != nil {
		t.Errorf("description %v, maxInputTokens %v; want both cleared", got.Description, got.MaxInputTokens)
	}
	p.assertConverged(model.ID, desired)
}

func TestModelPatch_ResetsAPIBaseWithAnEmptyString(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.APIBase = ""
	body := p.apply(model.ID, desired)

	assertBody(t, body, `{"apiBase":"","authConfig":{"api_key":"`+firstAPIKey+`"}}`)
	if got := p.get(model.ID); got.APIBase == nil || *got.APIBase != openAIBase {
		t.Errorf("apiBase = %v, want the provider default %s", got.APIBase, openAIBase)
	}
}

func TestModelPatch_ReplacesModelParamsWhole(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.ModelParams = map[string]any{"temperature": 0.5}
	body := p.apply(model.ID, desired)

	assertBody(t, body, `{"modelParams":{"temperature":0.5},"authConfig":{"api_key":"`+firstAPIKey+`"}}`)
	if got := p.get(model.ID); !reflect.DeepEqual(got.ModelParams, map[string]any{"temperature": 0.5}) {
		t.Errorf("modelParams = %v, want top_p gone", got.ModelParams)
	}
	p.assertConverged(model.ID, desired)
}

func TestModelPatch_ClearsModelParamsWithAnEmptyObject(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.ModelParams = nil
	body := p.apply(model.ID, desired)

	assertBody(t, body, `{"modelParams":{},"authConfig":{"api_key":"`+firstAPIKey+`"}}`)
	if got := p.get(model.ID); len(got.ModelParams) != 0 {
		t.Errorf("modelParams = %v, want them cleared", got.ModelParams)
	}
	p.assertConverged(model.ID, desired)
}

func TestModelPatch_RefusesToClearTheAuthType(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.AuthConfig = map[string]any{"api_key": firstAPIKey}
	_, err := generic.Plan(
		[]generic.DesiredEntry{{Name: desired.Alias, Create: desired}},
		[]reflect.Value{reflect.ValueOf(p.get(model.ID))},
		testModelKind,
	)
	var notClearable *generic.NotClearableError
	if !errors.As(err, &notClearable) || notClearable.Field != "authConfig.type" {
		t.Fatalf("err = %v, want a NotClearableError for authConfig.type", err)
	}
}

func TestModelPatch_WorkloadIdentitySwitchKeepsUnchangedMembers(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "azure-token")
	if err := os.WriteFile(tokenFile, []byte("federated-token"), 0o600); err != nil {
		t.Fatalf("write federated token file: %v", err)
	}
	t.Setenv("SAM_AZURE_WORKLOAD_IDENTITY_ENABLED", "true")
	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", tokenFile)
	p := newModelPlatform(t)
	oauth := baseModel()
	oauth.AuthConfig = map[string]any{
		"type": "oauth2", "client_id": workloadClientID, "client_secret": clientSecret,
		"token_url": "https://login.example.com/token",
	}
	model := p.create(oauth)

	desired := baseModel()
	desired.Provider = "azure_openai"
	desired.APIBase = "https://support.openai.azure.com/"
	desired.AuthConfig = map[string]any{"type": "azure_workload_identity", "client_id": workloadClientID, "tenant_id": workloadTenantID}
	body := p.apply(model.ID, desired)

	assertBody(t, body, `{"provider":"azure_openai","apiBase":"https://support.openai.azure.com/","authConfig":{"type":"azure_workload_identity","client_id":"`+workloadClientID+`","tenant_id":"`+workloadTenantID+`","token_url":null}}`)
	stored := p.storedAuth(model.ID)
	if stored["client_id"] != workloadClientID || stored["tenant_id"] != workloadTenantID {
		t.Errorf("stored authConfig = %v, want client_id and tenant_id kept", stored)
	}
	if _, kept := stored["client_secret"]; kept {
		t.Errorf("stored authConfig keeps the oauth2 client_secret: %v", stored)
	}
	p.assertConverged(model.ID, desired)
}

func TestModelPatch_AuthTypeSwitchLeavesNoOldCredential(t *testing.T) {
	p := newModelPlatform(t)
	oauth := baseModel()
	oauth.AuthConfig = map[string]any{
		"type": "oauth2", "client_id": "support-app", "client_secret": clientSecret,
		"token_url": "https://login.example.com/token",
	}
	model := p.create(oauth)

	desired := baseModel()
	body := p.apply(model.ID, desired)

	assertBody(t, body, `{"authConfig":{"type":"apikey","api_key":"`+firstAPIKey+`","client_id":null,"token_url":null}}`)
	stored := p.storedAuth(model.ID)
	for key, value := range stored {
		if key != "type" && key != "api_key" && value != nil {
			t.Errorf("stored authConfig keeps %s from the oauth2 config: %v", key, stored)
		}
	}
	if stored["api_key"] != firstAPIKey {
		t.Errorf("stored api_key = %v, want %s", stored["api_key"], firstAPIKey)
	}
	p.assertConverged(model.ID, desired)
}

func TestModelPatch_RotatedSecretReachesTheServer(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.AuthConfig = map[string]any{"type": "apikey", "api_key": rotatedKey}
	if plan := p.plan(model.ID, desired); len(plan.Unchanged) != 1 {
		t.Fatalf("a rotated secret should hash as unchanged; the secrets diff promotes it")
	}
	patch, err := generic.ToPatch(desired, p.get(model.ID), UpdateModelRequest{})
	if err != nil {
		t.Fatalf("ToPatch: %v", err)
	}
	body := p.patch(model.ID, patch)

	assertBody(t, body, `{"authConfig":{"api_key":"`+rotatedKey+`"}}`)
	if stored := p.storedAuth(model.ID); stored["api_key"] != rotatedKey {
		t.Errorf("stored api_key = %v, want the rotated key", stored["api_key"])
	}
}

func TestModelPatch_RefusesToRemoveAFieldTheServerCannotClear(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	desired := baseModel()
	desired.ModelName = ""
	_, err := generic.Plan(
		[]generic.DesiredEntry{{Name: desired.Alias, Create: desired}},
		[]reflect.Value{reflect.ValueOf(p.get(model.ID))},
		testModelKind,
	)
	var notClearable *generic.NotClearableError
	if !errors.As(err, &notClearable) || notClearable.Field != "modelName" {
		t.Fatalf("err = %v, want a NotClearableError for modelName", err)
	}
}

func TestModelPatch_ClearableTagsMatchWhatTheServerAccepts(t *testing.T) {
	p := newModelPlatform(t)
	model := p.create(baseModel())

	for sf := range reflect.TypeFor[UpdateModelRequest]().Fields() {
		if strings.Contains(sf.Tag.Get("cfgapply"), "read-only") {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		clearable := sf.Tag.Get("clearable") == "true"
		t.Run(name, func(t *testing.T) {
			status, raw := p.call(http.MethodPatch, "/models/"+model.ID, json.RawMessage(`{"`+name+`":null}`))
			accepted := status == http.StatusOK
			if accepted != clearable {
				t.Errorf("null for %s answered %d with clearable=%s: %s", name, status, strconv.FormatBool(clearable), raw)
			}
		})
	}
}
