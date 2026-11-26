// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/llmtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

var upstreamLeaks = []string{"401", "Incorrect API key", "api.openai.com", "invalid_request_error", "llm error"}

const upstreamUnauthorizedBody = `{"error":{"message":"Incorrect API key provided: sk-definitely-invalid. Visit api.openai.com for your keys","type":"invalid_request_error","code":"invalid_api_key"}}`

func fakeProvider(t *testing.T, handle http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handle)
	t.Cleanup(srv.Close)
	return srv.URL
}

func deadProvider(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

func refuseWith(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, body, status)
	}
}

const chatCompletionBody = `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o",
	"choices":[{"index":0,"message":{"role":"assistant","content":"It is 3pm."},"finish_reason":"stop"}]}`

const emptyCompletionBody = `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-4o",
	"choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`

func answerWith(body string) http.HandlerFunc {
	return mockllm.CompletionHandler(body)
}

func acceptOnlyKey(key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			refuseWith(http.StatusUnauthorized, upstreamUnauthorizedBody)(w, r)
			return
		}
		answerWith(chatCompletionBody)(w, r)
	}
}

func createModelAt(api *entrypointAPI, alias, apiBase string, authConfig map[string]any) Model {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": alias, "provider": "openai", "modelName": "gpt-4o",
		"apiBase": apiBase, "authConfig": authConfig,
	})
	return expectCreated[Model](api.t, rec, "models")
}

func probeResult(t *testing.T, rec *httptest.ResponseRecorder) ModelTestConnectionResult {
	t.Helper()
	expectStatus(t, rec, http.StatusOK)
	return decodeData[ModelTestConnectionResult](t, rec)
}

func TestModels_TestConnectionFailureIs200WithNoUpstreamText(t *testing.T) {
	api, _ := newModelAPI(t)
	base := fakeProvider(t, refuseWith(http.StatusUnauthorized, upstreamUnauthorizedBody))

	result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": base,
		"authType":   "apikey",
		"authConfig": map[string]any{"type": "apikey", "api_key": "sk-definitely-invalid"},
	}))

	if result.Success {
		t.Fatal("success = true for an invalid key")
	}
	if result.ErrorClass == nil {
		t.Fatal("errorClass is null on a failure; it must name the kind")
	}
	if *result.ErrorClass != classAuthCredentialsInvalid {
		t.Errorf("errorClass = %q, want %q", *result.ErrorClass, classAuthCredentialsInvalid)
	}
	if result.Message == nil || *result.Message == "" {
		t.Fatal("message is null on a failure; the form renders it")
	}
	for _, leak := range upstreamLeaks {
		if strings.Contains(*result.Message, leak) {
			t.Errorf("message carried upstream text %q: %s", leak, *result.Message)
		}
	}
	if strings.Contains(*result.Message, "sk-definitely-invalid") {
		t.Errorf("message echoed the credential: %s", *result.Message)
	}
}

func TestModels_TestConnectionRestoresTheRedactedSecret(t *testing.T) {
	api, _ := newModelAPI(t)
	base := fakeProvider(t, acceptOnlyKey(modelSecret))
	created := createModelAt(api, "round-trip", base, apiKeyAuth())

	result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"modelId":    created.ID,
		"authConfig": map[string]any{"type": "apikey", "api_key": redacted},
	}))

	if !result.Success {
		t.Fatalf("the sentinel reached the provider instead of the stored secret: class=%v message=%v",
			derefOrEmpty(result.ErrorClass), derefOrEmpty(result.Message))
	}
	if result.Message != nil {
		t.Errorf("message = %q, want null on a probe that passed", *result.Message)
	}
	if result.ErrorClass != nil {
		t.Errorf("errorClass = %q, want null on a probe that passed", *result.ErrorClass)
	}
}

func TestModels_TestConnectionRestoresASecretOfAnUnrecognisedAuthType(t *testing.T) {
	api, svc := newModelAPI(t)
	base := fakeProvider(t, acceptOnlyKey(modelSecret))
	created := seedUnregisteredTypeModel(t, svc, "custom-round-trip", base, map[string]any{"type": unknownAuthType, "api_key": modelSecret})

	got := api.call(http.MethodGet, "/models/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)

	result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"modelId":    created.ID,
		"authConfig": decodeData[Model](t, got).AuthConfig,
	}))

	if !result.Success {
		t.Fatalf("the sentinel reached the provider instead of the stored secret: class=%v message=%v",
			derefOrEmpty(result.ErrorClass), derefOrEmpty(result.Message))
	}
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestModels_TestConnectionClassifiesWhatTheProviderAnswers(t *testing.T) {
	shared, _ := newModelAPI(t)
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"unauthorized", refuseWith(http.StatusUnauthorized, upstreamUnauthorizedBody), classAuthCredentialsInvalid},
		{"forbidden", refuseWith(http.StatusForbidden, `{"error":{"message":"You do not have access to this model"}}`), classAuthPermissionDenied},
		{"notFound", refuseWith(http.StatusNotFound, `{"error":{"message":"The model does not exist","code":"model_not_found"}}`), classModelNotFound},
		{"badRequest", refuseWith(http.StatusBadRequest, `{"error":{"message":"Unsupported parameter: tools"}}`), classRequestRejected},
		{"rateLimited", refuseWith(http.StatusTooManyRequests, `{"error":{"message":"Rate limit reached"}}`), classRateLimited},
		{"serverError", refuseWith(http.StatusInternalServerError, `{"error":{"message":"The server had an error"}}`), classProviderError},
		{"emptyAnswer", answerWith(emptyCompletionBody), classNoResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
				"provider": "openai", "modelName": "gpt-4o", "apiBase": fakeProvider(t, tc.handler),
				"authConfig": map[string]any{"type": "apikey", "api_key": "sk-test"},
			}))
			if result.Success {
				t.Fatal("success = true for a refused probe")
			}
			if result.ErrorClass == nil || *result.ErrorClass != tc.want {
				t.Errorf("errorClass = %v, want %q", derefOrEmpty(result.ErrorClass), tc.want)
			}
			for _, leak := range upstreamLeaks {
				if result.Message != nil && strings.Contains(*result.Message, leak) {
					t.Errorf("message carried upstream text %q: %s", leak, *result.Message)
				}
			}
		})
	}
}

func TestModels_TestConnectionClassifiesADecisionModelByTheStatusTheProviderAnswers(t *testing.T) {
	const outOfQuotaBody = `{"error":{"message":"no credits","type":"insufficient_quota","code":"credit_balance_exhausted"}}`
	for _, tc := range []struct {
		name     string
		provider string
		model    string
		handler  http.HandlerFunc
		want     string
	}{
		{"typesafe 500 is the provider's own fault", "typesafe", "jev-1.13.0", refuseWith(http.StatusInternalServerError, `{}`), classProviderError},
		{"typesafe 404 names no such model", "typesafe", "jev-1.13.0", refuseWith(http.StatusNotFound, `{}`), classModelNotFound},
		{"typesafe 422 rejects the request", "typesafe", "jev-1.13.0", refuseWith(http.StatusUnprocessableEntity, `{}`), classRequestRejected},
		{"typesafe 403 lacks permission", "typesafe", "jev-1.13.0", refuseWith(http.StatusForbidden, `{}`), classAuthPermissionDenied},
		{"typesafe 402 is out of credit", "typesafe", "jev-1.13.0", refuseWith(http.StatusPaymentRequired, `{}`), classAuthPermissionDenied},
		{"typesafe 401 rejects the credentials", "typesafe", "jev-1.13.0", refuseWith(http.StatusUnauthorized, `{}`), classAuthCredentialsInvalid},
		{"typesafe 529 is a 5xx the provider answered", "typesafe", "jev-1.13.0", refuseWith(529, `{}`), classProviderError},
		{"typesafe 429 is rate limited", "typesafe", "jev-1.13.0", refuseWith(http.StatusTooManyRequests, `{}`), classRateLimited},
		{"typesafe 413 has no status class and keeps its kind", "typesafe", "jev-1.13.0", refuseWith(http.StatusRequestEntityTooLarge, `{}`), classRequestRejected},
		{"typesafe 409 has no status class and keeps its kind", "typesafe", "jev-1.13.0", refuseWith(http.StatusConflict, `{}`), classConfigIncomplete},
		{"openai 400 rejects the request", "openai", "gpt-6-luna", refuseWith(http.StatusBadRequest, `{}`), classRequestRejected},
		{"openai out of quota is out of credit", "openai", "gpt-6-luna", refuseWith(http.StatusTooManyRequests, outOfQuotaBody), classAuthPermissionDenied},
		{"openai plain 429 is rate limited", "openai", "gpt-6-luna", refuseWith(http.StatusTooManyRequests, `{}`), classRateLimited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newModelAPI(t)
			result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
				"provider": tc.provider, "modelName": tc.model, "modelType": "decision", "apiBase": fakeProvider(t, tc.handler),
				"authType": "apikey", "authConfig": keyAuth("k"),
			}))
			if result.Success {
				t.Fatal("success = true for a refused probe")
			}
			if result.ErrorClass == nil || *result.ErrorClass != tc.want {
				t.Errorf("errorClass = %v, want %q", derefOrEmpty(result.ErrorClass), tc.want)
			}
		})
	}
}

func TestModels_TestConnectionClassifiesAGeminiDecisionModelByTheStatusTheProviderAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"403 lacks permission":       {http.StatusForbidden, `{}`, classAuthPermissionDenied},
		"404 names no such model":    {http.StatusNotFound, `{}`, classModelNotFound},
		"503 is the provider's own":  {http.StatusServiceUnavailable, `{}`, classProviderError},
		"504 is a timeout":           {http.StatusGatewayTimeout, `{}`, classTimeout},
		"400 rejects the request":    {http.StatusBadRequest, `{}`, classRequestRejected},
		"400 invalid key is a login": {http.StatusBadRequest, `{"error":{"details":[{"reason":"API_KEY_INVALID"}]}}`, classAuthCredentialsInvalid},
		"400 safety block":           {http.StatusBadRequest, `{"error":{"code":"safety"}}`, classInvalidResponse},
	} {
		t.Run(name, func(t *testing.T) {
			api, _ := newModelAPI(t)
			result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
				"provider": "google_ai_studio", "modelName": "gemini-decision-1", "modelType": "decision",
				"apiBase": fakeProvider(t, refuseWith(tc.status, tc.body)), "authType": "apikey", "authConfig": keyAuth("k"),
			}))
			if result.ErrorClass == nil || *result.ErrorClass != tc.want {
				t.Errorf("errorClass = %v, want %q", derefOrEmpty(result.ErrorClass), tc.want)
			}
		})
	}
}

func TestModels_TestConnectionReportsADecisionEndpointThatCannotBeReachedAsUnreachable(t *testing.T) {
	api, _ := newModelAPI(t)
	result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "typesafe", "modelName": "jev-1.13.0", "modelType": "decision", "apiBase": deadProvider(t),
		"authType": "apikey", "authConfig": keyAuth("k"),
	}))
	if result.ErrorClass == nil || *result.ErrorClass != classNetworkUnreachable {
		t.Errorf("errorClass = %v, want %q", derefOrEmpty(result.ErrorClass), classNetworkUnreachable)
	}
}

func TestModels_TestConnectionUnreachableEndpoint(t *testing.T) {
	api, _ := newModelAPI(t)
	result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": deadProvider(t),
		"authConfig": map[string]any{"type": "apikey", "api_key": "sk-test"},
	}))
	if result.ErrorClass == nil || *result.ErrorClass != classNetworkUnreachable {
		t.Errorf("errorClass = %v, want %q", derefOrEmpty(result.ErrorClass), classNetworkUnreachable)
	}
}

func TestModels_TestConnectionReportsWorkloadIdentityNotEnabled(t *testing.T) {
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "false")
	api, _ := newModelAPI(t)

	result := probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "azure_openai", "modelName": "gpt-4o", "apiBase": "https://example.openai.azure.com/",
		"authType": platpkg.AuthTypeAzureWorkloadIdentity,
		"authConfig": map[string]any{
			"type":      platpkg.AuthTypeAzureWorkloadIdentity,
			"client_id": "00000000-0000-0000-0000-000000000001", "tenant_id": "00000000-0000-0000-0000-000000000002",
		},
	}))

	if result.Success {
		t.Fatal("success = true for a deployment that has not enabled workload identity")
	}
	if result.ErrorClass == nil || *result.ErrorClass != classConfigIncomplete {
		t.Errorf("errorClass = %v, want %q", derefOrEmpty(result.ErrorClass), classConfigIncomplete)
	}
	if got := derefOrEmpty(result.Message); !strings.Contains(got, "is not enabled on this deployment") {
		t.Errorf("message = %q, want it to say workload identity is not enabled on this deployment", got)
	}
}

func TestModels_TestConnectionRejectsAnIncompleteRequest(t *testing.T) {
	api, _ := newModelAPI(t)
	for name, tc := range map[string]struct {
		body  map[string]any
		field string
	}{
		"no provider":  {map[string]any{"modelName": "gpt-4o", "authConfig": map[string]any{"type": "apikey", "api_key": "sk-test"}}, "provider"},
		"no modelName": {map[string]any{"provider": "openai", "authConfig": map[string]any{"type": "apikey", "api_key": "sk-test"}}, "modelName"},
		"no authType":  {map[string]any{"provider": "openai", "modelName": "gpt-4o", "authConfig": map[string]any{"api_key": "sk-test"}}, "authConfig.type"},
	} {
		t.Run(name, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodPut, "/models/testConnection", tc.body), http.StatusUnprocessableEntity, tc.field)
		})
	}
}

func TestModels_TestConnectionNamesEveryMissingField(t *testing.T) {
	api, _ := newModelAPI(t)
	rec := api.call(http.MethodPut, "/models/testConnection", map[string]any{})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	body := decodeError(t, rec)
	for _, field := range []string{"provider", "modelName", "authConfig.type"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("validationDetails has nothing for %q: %s", field, rec.Body.String())
		}
	}
	if body.Message == "Unable to test the model connection" {
		t.Errorf("message = %q, which reads as a server fault rather than a rejected request", body.Message)
	}
}

func TestModels_TestConnectionUnknownModelIs404(t *testing.T) {
	api, _ := newModelAPI(t)
	rec := api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"modelId":    missingID,
		"authConfig": map[string]any{"type": "apikey", "api_key": "sk-test"},
	})
	expectStatus(t, rec, http.StatusNotFound)
}

func TestModels_TestConnectionTakesEitherWriteScope(t *testing.T) {
	api, _ := newModelAPI(t)
	base := fakeProvider(t, answerWith(chatCompletionBody))
	body := map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": base,
		"authConfig": map[string]any{"type": "apikey", "api_key": "sk-test"},
	}
	for _, scope := range []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate} {
		if rec := api.call(http.MethodPut, "/models/testConnection", body, scope); rec.Code != http.StatusOK {
			t.Errorf("with %s = %d, want 200; body %s", scope, rec.Code, rec.Body.String())
		}
	}
	if rec := api.call(http.MethodPut, "/models/testConnection", body, rbac.ScopeModelConfigDelete); rec.Code != http.StatusForbidden {
		t.Errorf("with only the delete scope = %d, want 403", rec.Code)
	}
}

func TestModels_SecretsDiff(t *testing.T) {
	api, _ := newModelAPI(t)
	created := createModel(api, "diffable", apiKeyAuth())
	path := "/models/" + created.ID + "/secretsDiff"

	for name, tc := range map[string]struct {
		authConfig map[string]any
		want       bool
	}{
		"the stored value":   {map[string]any{"api_key": modelSecret}, true},
		"the sentinel":       {map[string]any{"api_key": redacted}, true},
		"the field omitted":  {map[string]any{"type": "apikey"}, true},
		"a different value":  {map[string]any{"api_key": "sk-rotated"}, false},
		"an empty api_key":   {map[string]any{"api_key": ""}, true},
		"a non-string value": {map[string]any{"api_key": 7}, false},
	} {
		t.Run(name, func(t *testing.T) {
			rec := api.call(http.MethodPut, path, map[string]any{"authConfig": tc.authConfig})
			expectStatus(t, rec, http.StatusOK)
			if strings.Contains(rec.Body.String(), modelSecret) {
				t.Errorf("secretsDiff returned the stored credential: %s", rec.Body.String())
			}
			got := decodeData[ModelSecretsDiffResult](t, rec)
			match, present := got.Secrets["api_key"]
			if !present {
				t.Fatalf("no entry for api_key: %s", rec.Body.String())
			}
			if match.Matches != tc.want {
				t.Errorf("matches = %v, want %v", match.Matches, tc.want)
			}
		})
	}
}

func TestModels_SecretsDiffUnknownModelIs404(t *testing.T) {
	api, _ := newModelAPI(t)
	rec := api.call(http.MethodPut, "/models/"+missingID+"/secretsDiff", map[string]any{"authConfig": map[string]any{}})
	expectStatus(t, rec, http.StatusNotFound)
}

func TestModels_SecretsDiffNeedsTheUpdateScope(t *testing.T) {
	api, _ := newModelAPI(t)
	created := createModel(api, "gated-diff", apiKeyAuth())
	path := "/models/" + created.ID + "/secretsDiff"
	body := map[string]any{"authConfig": map[string]any{"api_key": redacted}}

	if rec := api.call(http.MethodPut, path, body, rbac.ScopeModelConfigUpdate); rec.Code != http.StatusOK {
		t.Errorf("with the update scope = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if rec := api.call(http.MethodPut, path, body, rbac.ScopeModelConfigCreate); rec.Code != http.StatusForbidden {
		t.Errorf("with only the create scope = %d, want 403", rec.Code)
	}
}

var errUpstreamSentinel = errors.New("UPSTREAM-SENTINEL-must-not-leak")

func TestClassifyProbeError(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"no response":               {llmtest.ErrNoResponse, classNoResponse},
		"probe deadline":            {context.DeadlineExceeded, classTimeout},
		"no model configuration":    {llmtest.ErrNoConfig, classConfigIncomplete},
		"an unusable configuration": {llmtest.ErrInvalidConfig, classConfigIncomplete},
		"client would not build":    {fmt.Errorf("%w: %w", llmtest.ErrClientInit, errUpstreamSentinel), classConfigIncomplete},
		"bifrost timeout":           {&llm.Error{Type: "request_timed_out", StatusCode: http.StatusGatewayTimeout, Message: errUpstreamSentinel.Error()}, classTimeout},
		"request timeout":           {&llm.Error{StatusCode: http.StatusRequestTimeout, Message: errUpstreamSentinel.Error()}, classTimeout},
		"connection failed":         {&llm.Error{Type: "provider_connection_failed", StatusCode: http.StatusBadGateway, Message: errUpstreamSentinel.Error()}, classNetworkUnreachable},
		"no key for the model":      {&llm.Error{Type: "no_key_supports_model", StatusCode: http.StatusBadRequest, Message: errUpstreamSentinel.Error()}, classConfigIncomplete},
		"unauthorized":              {&llm.Error{StatusCode: http.StatusUnauthorized, Message: errUpstreamSentinel.Error()}, classAuthCredentialsInvalid},
		"forbidden":                 {&llm.Error{StatusCode: http.StatusForbidden, Message: errUpstreamSentinel.Error()}, classAuthPermissionDenied},
		"payment required":          {&llm.Error{StatusCode: http.StatusPaymentRequired, Message: errUpstreamSentinel.Error()}, classAuthPermissionDenied},
		"not found":                 {&llm.Error{StatusCode: http.StatusNotFound, Message: errUpstreamSentinel.Error()}, classModelNotFound},
		"unprocessable":             {&llm.Error{StatusCode: http.StatusUnprocessableEntity, Message: errUpstreamSentinel.Error()}, classRequestRejected},
		"too many requests":         {&llm.Error{StatusCode: http.StatusTooManyRequests, Message: errUpstreamSentinel.Error()}, classRateLimited},
		"bad gateway":               {&llm.Error{StatusCode: http.StatusBadGateway, Message: errUpstreamSentinel.Error()}, classProviderError},
		"teapot":                    {&llm.Error{StatusCode: http.StatusTeapot, Message: errUpstreamSentinel.Error()}, classUnknown},
		"no status at all":          {&llm.Error{Message: errUpstreamSentinel.Error()}, classUnknown},
		"decision unauthorized":     {&decision.Error{Kind: decision.FailureUnauthorized, Err: errUpstreamSentinel}, classAuthCredentialsInvalid},
		"decision rate limited":     {&decision.Error{Kind: decision.FailureRateLimited, Err: errUpstreamSentinel}, classRateLimited},
		"decision timeout":          {&decision.Error{Kind: decision.FailureTimeout, Err: errUpstreamSentinel}, classTimeout},
		"decision unreachable":      {&decision.Error{Kind: decision.FailureUnreachable, Err: errUpstreamSentinel}, classNetworkUnreachable},
		"decision config":           {&decision.Error{Kind: decision.FailureConfig, Err: errUpstreamSentinel}, classConfigIncomplete},
		"decision too large":        {&decision.Error{Kind: decision.FailureInputTooLarge, Err: errUpstreamSentinel}, classRequestRejected},
		"decision invalid":          {&decision.Error{Kind: decision.FailureInvalidResponse, Err: errUpstreamSentinel}, classInvalidResponse},
		"decision refused":          {&decision.Error{Kind: decision.FailureRefused, Err: errUpstreamSentinel}, classInvalidResponse},
		"a bare error":              {errUpstreamSentinel, classUnknown},
		"azure tenant not found":    {&llm.Error{Type: llm.ErrorTypeAzureSignIn, Code: "AADSTS90002", Message: errUpstreamSentinel.Error()}, classAuthCredentialsInvalid},
		"azure sign-in refused":     {&llm.Error{Type: llm.ErrorTypeAzureSignIn, Code: "AADSTS9999999", Message: errUpstreamSentinel.Error()}, classAuthCredentialsInvalid},
		"azure entra unreachable":   {&llm.Error{Type: llm.ErrorTypeAzureSignIn, Code: llm.SignInEntraUnreachable, Message: errUpstreamSentinel.Error()}, classNetworkUnreachable},
		"azure token unreadable":    {&llm.Error{Type: llm.ErrorTypeAzureSignIn, Code: llm.SignInTokenFileUnreadable, Message: errUpstreamSentinel.Error()}, classConfigIncomplete},
	} {
		t.Run(name, func(t *testing.T) {
			class, message := classifyProbeError(tc.err)
			if class != tc.want {
				t.Errorf("class = %q, want %q", class, tc.want)
			}
			if message == "" {
				t.Error("message is empty; the form renders it")
			}
			if strings.Contains(message, tc.err.Error()) {
				t.Errorf("message carried the error's own text: %s", message)
			}
		})
	}
}

func TestClassifyProbeErrorNamesTheAzureSetupProblem(t *testing.T) {
	const entraDescription = "AADSTS90002: Tenant 'not-a-tenant' not found."
	err := &llm.Error{Type: llm.ErrorTypeAzureSignIn, Code: "AADSTS90002", Message: entraDescription}

	class, message := classifyProbeError(err)

	if class != classAuthCredentialsInvalid {
		t.Errorf("class = %q, want %q", class, classAuthCredentialsInvalid)
	}
	if !strings.Contains(message, "tenant ID") || !strings.Contains(message, "AADSTS90002") {
		t.Errorf("message = %q, want it to name the tenant ID and the AADSTS code", message)
	}
	if strings.Contains(message, "not-a-tenant") {
		t.Errorf("message carried Entra's description: %s", message)
	}
}

func TestProbeErrorClasses(t *testing.T) {
	want := []string{
		classAuthCredentialsInvalid, classAuthPermissionDenied, classConfigIncomplete,
		classInvalidResponse, classModelNotFound, classNetworkUnreachable, classNoResponse,
		classProviderError, classRateLimited, classRequestRejected, classTimeout, classUnknown,
	}
	got := slices.Sorted(maps.Keys(probeMessages))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("published classes = %v, want %v", got, want)
	}
	for _, class := range got {
		if !lowerSnakeCaseClass(class) {
			t.Errorf("class %q is not lowercase snake_case", class)
		}
	}
}

func lowerSnakeCaseClass(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return s != ""
}

const modelListBody = `{"object":"list","data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`

type listingProvider struct {
	url string

	mu    sync.Mutex
	calls int
	auths []string
}

func newListingProvider(t *testing.T, respond http.HandlerFunc) *listingProvider {
	t.Helper()
	p := &listingProvider{}
	p.url = fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.calls++
		p.auths = append(p.auths, r.Header.Get("Authorization"))
		p.mu.Unlock()
		respond(w, r)
	})
	return p
}

func (p *listingProvider) requests() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, slices.Clone(p.auths)
}

func keyAuth(key string) map[string]any {
	return map[string]any{"type": "apikey", "api_key": key}
}

func TestSupportedModels_TakesNestedAuthConfig(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))

	rec := api.call(http.MethodPut, "/supportedModels", map[string]any{
		"provider": "openai", "apiBase": provider.url, "authConfig": keyAuth("sk-nested"),
	})
	expectStatus(t, rec, http.StatusOK)

	body := decodeList[map[string]any](t, rec)
	if len(body.Data) != 2 {
		t.Fatalf("%d models, want the provider's 2: %s", len(body.Data), rec.Body.String())
	}
	first := body.Data[0]
	if first["type"] != "supportedModel" || first["key"] != "gpt-4o" || first["provider"] != "openai" {
		t.Errorf("first model = %v, want type supportedModel, key gpt-4o, provider openai", first)
	}
	if _, hasID := first["id"]; hasID {
		t.Errorf("a model carries id, but no route fetches one, so its identifier is key: %v", first)
	}
	if _, auths := provider.requests(); !slices.Equal(auths, []string{"Bearer sk-nested"}) {
		t.Errorf("provider saw credentials %v, want the nested api_key", auths)
	}
}

func TestSupportedModels_RejectsTheFlatCredentialShape(t *testing.T) {
	api, _ := newModelAPI(t)
	for _, field := range []string{
		"apiKey", "clientId", "clientSecret", "tokenUrl", "awsAccessKeyId", "awsSecretAccessKey",
		"awsSessionToken", "awsRegionName", "vertexCredentials", "vertexLocation",
	} {
		rec := api.call(http.MethodPut, "/supportedModels", map[string]any{"provider": "openai", field: "x"})
		expectStatus(t, rec, http.StatusBadRequest)
		if details := decodeError(t, rec).ValidationDetails; len(details[field]) == 0 {
			t.Errorf("%s: validationDetails %v do not name the field", field, details)
		}
	}
}

func TestProviderModels_UnknownProviderIs404WithoutAnOutboundCall(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))
	creds := map[string]any{"apiBase": provider.url, "authConfig": keyAuth("sk-x")}

	for name, call := range map[string]func() *httptest.ResponseRecorder{
		"providers/{provider}/models": func() *httptest.ResponseRecorder {
			return api.call(http.MethodPut, "/providers/not_a_provider/models", creds)
		},
		"providers/{provider}/params": func() *httptest.ResponseRecorder {
			return api.call(http.MethodPut, "/providers/not_a_provider/params", nil)
		},
		"supportedModels": func() *httptest.ResponseRecorder {
			return api.call(http.MethodPut, "/supportedModels", map[string]any{
				"provider": "not_a_provider", "apiBase": provider.url, "authConfig": keyAuth("sk-x"),
			})
		},
	} {
		rec := call()
		expectStatus(t, rec, http.StatusNotFound)
		if got := decodeError(t, rec).Message; got != msgUnknownProvider {
			t.Errorf("%s: message = %q, want %q", name, got, msgUnknownProvider)
		}
		if calls, _ := provider.requests(); calls != 0 {
			t.Errorf("%s: an unknown provider reached the outbound call %d time(s)", name, calls)
		}
	}
}

func TestProviderModels_ListsWithCreatingModeCredentials(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))

	rec := api.call(http.MethodPut, "/providers/openai/models", map[string]any{
		"apiBase": provider.url, "authConfig": keyAuth("sk-creating"),
	})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeList[SupportedModel](t, rec).Data; len(got) != 2 || got[0].Key != "gpt-4o" || got[0].Type != "supportedModel" {
		t.Errorf("models = %+v, want gpt-4o and gpt-4o-mini typed supportedModel", got)
	}
	if _, auths := provider.requests(); !slices.Equal(auths, []string{"Bearer sk-creating"}) {
		t.Errorf("provider saw credentials %v, want the request's api_key", auths)
	}
}

func TestProviderModels_EditingModeRestoresTheStoredSecret(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))
	saved := createModelAt(api, "listing", provider.url, apiKeyAuth())

	rec := api.call(http.MethodPut, "/providers/openai/models", map[string]any{
		"modelId": saved.ID, "authConfig": keyAuth(redacted),
	})
	expectStatus(t, rec, http.StatusOK)
	if _, auths := provider.requests(); !slices.Equal(auths, []string{"Bearer " + modelSecret}) {
		t.Errorf("provider saw credentials %v, want the stored key", auths)
	}
	if strings.Contains(rec.Body.String(), modelSecret) {
		t.Errorf("the stored credential reached the response: %s", rec.Body.String())
	}
}

func TestProviderModels_EditingModeRestoresASecretOfAnUnrecognisedAuthType(t *testing.T) {
	api, svc := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))
	saved := seedUnregisteredTypeModel(t, svc, "custom-listing", provider.url, map[string]any{"type": unknownAuthType, "api_key": modelSecret})

	got := api.call(http.MethodGet, "/models/"+saved.ID, nil)
	expectStatus(t, got, http.StatusOK)

	rec := api.call(http.MethodPut, "/providers/openai/models", map[string]any{
		"modelId": saved.ID, "authConfig": decodeData[Model](t, got).AuthConfig,
	})
	expectStatus(t, rec, http.StatusOK)
	if _, auths := provider.requests(); !slices.Equal(auths, []string{"Bearer " + modelSecret}) {
		t.Errorf("provider saw credentials %v, want the stored key", auths)
	}
}

func TestProviderModels_CreatingModeDoesNotSendTheSentinelUpstream(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))

	api.call(http.MethodPut, "/providers/openai/models", map[string]any{
		"authConfig": keyAuth(redacted), "apiBase": provider.url,
	})

	_, auths := provider.requests()
	for _, auth := range auths {
		if strings.Contains(auth, redacted) {
			t.Errorf("provider saw credentials %q: the redaction sentinel went upstream as a key", auth)
		}
	}
}

func TestProviderModels_CreatingModeNeedsAnAuthType(t *testing.T) {
	api, _ := newModelAPI(t)
	for name, body := range map[string]map[string]any{
		"nothing":        {},
		"no auth type":   {"authConfig": map[string]any{"api_key": "sk-x"}},
		"empty authType": {"authConfig": map[string]any{"type": "", "api_key": "sk-x"}},
	} {
		rec := api.call(http.MethodPut, "/providers/openai/models", body)
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if details := decodeError(t, rec).ValidationDetails; len(details["authConfig.type"]) == 0 {
			t.Errorf("%s: validationDetails %v do not name authConfig.type", name, details)
		}
	}
}

func TestSupportedModels_UpstreamFailureIs502WithNoUpstreamText(t *testing.T) {
	logs := captureLogs(t)
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, refuseWith(http.StatusUnauthorized, upstreamUnauthorizedBody))

	saved := createModelAt(api, "failing-provider", provider.url, apiKeyAuth())
	for _, tc := range []struct {
		name string
		path string
		body map[string]any
	}{
		{"supportedModels", "/supportedModels", map[string]any{"provider": "openai", "apiBase": provider.url, "authConfig": keyAuth("sk-definitely-invalid")}},
		{"creating mode", "/providers/openai/models", map[string]any{"apiBase": provider.url, "authConfig": keyAuth("sk-definitely-invalid")}},
		{"editing mode", "/providers/openai/models", map[string]any{"modelId": saved.ID, "authConfig": keyAuth(redacted)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := api.call(http.MethodPut, tc.path, tc.body)
			expectStatus(t, rec, http.StatusBadGateway)
			for _, leak := range upstreamForbidden {
				if leak != "401" && strings.Contains(rec.Body.String(), leak) {
					t.Errorf("the response body carried upstream text %q: %s", leak, rec.Body.String())
				}
			}
			errBody := decodeError(t, rec)
			if errBody.Message != "Unable to list models from the provider" {
				t.Errorf("message = %q, want the fixed sentence", errBody.Message)
			}
			entry, ok := errorLogEntry(logs.String(), errBody.ErrorID)
			if !ok {
				t.Errorf("no log line has errorId %s, so the 502 joins to nothing", errBody.ErrorID)
				return
			}
			if entry.HTTPStatus != http.StatusBadGateway {
				t.Errorf("logged httpStatus = %d, want 502", entry.HTTPStatus)
			}
			if !strings.Contains(entry.Error, "openai") {
				t.Errorf("logged cause %q does not name the provider", entry.Error)
			}
		})
	}

	for _, leak := range upstreamForbidden {
		if leak == "401" {
			continue
		}
		if strings.Contains(logs.String(), leak) {
			t.Errorf("the platform log carried upstream text %q", leak)
		}
	}
}

var upstreamForbidden = append(slices.Clone(upstreamLeaks), "sk-definitely-invalid", modelSecret, "status 401")

type loggedError struct {
	ErrorID    string `json:"errorId"`
	HTTPStatus int    `json:"httpStatus"`
	Error      string `json:"error"`
}

func errorLogEntry(logs, errorID string) (loggedError, bool) {
	for line := range strings.Lines(logs) {
		var entry loggedError
		if json.Unmarshal([]byte(line), &entry) == nil && entry.ErrorID == errorID {
			return entry, true
		}
	}
	return loggedError{}, false
}

func TestProviderParams_UsesTheListEnvelope(t *testing.T) {
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodPut, "/providers/openai/params", nil)
	expectStatus(t, rec, http.StatusOK)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, bespoke := raw["supportedParams"]; bespoke {
		t.Error("the bespoke supportedParams key is still on the wire")
	}
	if _, ok := raw["data"]; !ok {
		t.Errorf("no data key in %s", rec.Body.String())
	}
	body := decodeList[ProviderParam](t, rec)
	if len(body.Data) == 0 {
		t.Fatal("no parameters returned for openai")
	}
	if !slices.Contains(body.Data, ProviderParam{Type: "providerParam", Key: "temperature"}) {
		t.Errorf("temperature is not in %+v", body.Data)
	}
	pg := body.Meta.Pagination
	if pg.Count != len(body.Data) || pg.PageNumber != 1 || pg.PageSize != 20 || pg.TotalPages != 1 || pg.NextPage != nil {
		t.Errorf("pagination = %+v, want page 1 of size 20 over %d items", pg, len(body.Data))
	}
}

func TestProviderParams_AnOpenAIOnlyParameterIsNotAnAnthropicOne(t *testing.T) {
	api, _ := newModelAPI(t)
	keys := func(provider string) []string {
		rec := api.call(http.MethodPut, "/providers/"+provider+"/params", nil)
		expectStatus(t, rec, http.StatusOK)
		var out []string
		for _, p := range decodeList[ProviderParam](t, rec).Data {
			out = append(out, p.Key)
		}
		return out
	}
	if !slices.Contains(keys("openai"), "logit_bias") || slices.Contains(keys("anthropic"), "logit_bias") {
		t.Error("the list is not per provider")
	}
}

func TestProviderParams_HonoursThePageQuery(t *testing.T) {
	api, _ := newModelAPI(t)
	rec := api.call(http.MethodPut, "/providers/openai/params?pageNumber=2&pageSize=5", nil)
	expectStatus(t, rec, http.StatusOK)
	body := decodeList[ProviderParam](t, rec)
	pg := body.Meta.Pagination
	if len(body.Data) != 5 || pg.PageNumber != 2 || pg.PageSize != 5 || pg.Count <= 10 {
		t.Errorf("page = %d items, pagination %+v, want the second page of 5", len(body.Data), pg)
	}
}

func TestModelLookups_TheProviderIsLowercase(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))

	rec := api.call(http.MethodPut, "/supportedModels", map[string]any{
		"provider": "OpenAI", "apiBase": provider.url, "authConfig": keyAuth("sk-x"),
	})
	expectStatus(t, rec, http.StatusOK)
	for _, m := range decodeList[SupportedModel](t, rec).Data {
		if m.Provider != "openai" {
			t.Errorf("provider = %q, want openai", m.Provider)
		}
	}
	models := api.call(http.MethodPut, "/providers/OpenAI/models", map[string]any{
		"apiBase": provider.url, "authConfig": keyAuth("sk-x"),
	})
	expectStatus(t, models, http.StatusOK)
	expectStatus(t, api.call(http.MethodPut, "/providers/OpenAI/params", nil), http.StatusOK)
	expectDetail(t, api.call(http.MethodPut, "/providers/open-ai/params", nil), http.StatusBadRequest, "provider")
}

func TestSupportedModels_UnknownSavedModelIs404InSAMsWords(t *testing.T) {
	api, _ := newModelAPI(t)
	for path, body := range map[string]map[string]any{
		"/supportedModels":         {"provider": "openai", "modelId": missingID},
		"/providers/openai/models": {"modelId": missingID},
	} {
		rec := api.call(http.MethodPut, path, body)
		expectStatus(t, rec, http.StatusNotFound)
		if got := decodeError(t, rec).Message; got != failReadModel {
			t.Errorf("%s: message = %q, want %q", path, got, failReadModel)
		}
	}
}

func TestModelLookups_TakeEitherWriteScope(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))
	for path, body := range map[string]map[string]any{
		"/supportedModels":         {"provider": "openai", "apiBase": provider.url, "authConfig": keyAuth("sk-x")},
		"/providers/openai/models": {"apiBase": provider.url, "authConfig": keyAuth("sk-x")},
		"/providers/openai/params": nil,
	} {
		for _, scope := range []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate} {
			if rec := api.call(http.MethodPut, path, body, scope); rec.Code != http.StatusOK {
				t.Errorf("%s with %s = %d, want 200; body %s", path, scope, rec.Code, rec.Body.String())
			}
		}
		if rec := api.call(http.MethodPut, path, body, rbac.ScopeModelConfigDelete); rec.Code != http.StatusForbidden {
			t.Errorf("%s with only the delete scope = %d, want 403", path, rec.Code)
		}
	}
}

type tokenEndpointError struct{ body string }

func (e *tokenEndpointError) Error() string { return e.body }

func TestLogProbeFailure_NamesAPlainErrorsCauseWithoutItsText(t *testing.T) {
	var logs bytes.Buffer
	h := &modelLookupHandler{logger: slog.New(slog.NewTextHandler(&logs, nil))}
	err := fmt.Errorf("oauth2 token: %w", &tokenEndpointError{body: "invalid client secret " + modelSecret})

	h.logProbeFailure(t.Context(), entplatform.ModelProbe{Provider: "openai", ModelType: "chat"}, classUnknown, err)

	out := logs.String()
	if !strings.Contains(out, "tokenEndpointError") {
		t.Errorf("log names no cause type, so an operator learns nothing: %s", out)
	}
	if strings.Contains(out, modelSecret) || strings.Contains(out, "invalid client secret") {
		t.Errorf("the error's text reached the log: %s", out)
	}
}

func TestModels_TestConnectionLogNamesTheStableSubject(t *testing.T) {
	logs := captureLogs(t)
	api, _ := newModelAPI(t)
	base := fakeProvider(t, refuseWith(http.StatusUnauthorized, upstreamUnauthorizedBody))
	identity := auth.DevIdentity()

	probeResult(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": base,
		"authType":   "apikey",
		"authConfig": map[string]any{"type": "apikey", "api_key": "sk-definitely-invalid"},
	}))

	if strings.Contains(logs.String(), identity.Email()) {
		t.Errorf("the log carried the caller's email %q", identity.Email())
	}
	if !strings.Contains(logs.String(), `"userID":"`+identity.UserID()+`"`) {
		t.Errorf("the failed-probe log does not carry userID %q", identity.UserID())
	}
}

func TestSupportedModels_LocalLookupFailureIsNotAProviderFailure(t *testing.T) {
	logs := captureLogs(t)
	dbURL := testTemplate.Clone(t)
	core, _ := newTestCoreWithConfig(t, map[string]any{"database_url": dbURL})
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	provider := newListingProvider(t, answerWith(modelListBody))
	saved := createModelAt(api, "doomed", provider.url, apiKeyAuth())

	db, err := store.Open(dbURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE model_configurations RENAME TO model_configurations_gone"); err != nil {
		t.Fatal(err)
	}

	for path, body := range map[string]map[string]any{
		"/supportedModels":         {"provider": "openai", "modelId": saved.ID, "authConfig": keyAuth(redacted)},
		"/providers/openai/models": {"modelId": saved.ID, "authConfig": keyAuth(redacted)},
	} {
		rec := api.call(http.MethodPut, path, body)
		expectStatus(t, rec, http.StatusInternalServerError)
		entry, ok := errorLogEntry(logs.String(), decodeError(t, rec).ErrorID)
		if !ok {
			t.Errorf("%s: no log line joins to the 500", path)
			continue
		}
		if strings.Contains(entry.Error, "withheld") || !strings.Contains(entry.Error, "model") {
			t.Errorf("%s: logged cause %q is withheld or does not name the local lookup", path, entry.Error)
		}
	}
}

func TestErrorTypeChain(t *testing.T) {
	cause := errors.New("sk-text-that-must-not-appear")
	deep := errors.New("deep")
	for i := 0; i < 2*maxCauseChain; i++ {
		deep = fmt.Errorf("layer: %w", deep)
	}
	for name, tc := range map[string]struct {
		err      error
		want     string
		maxTypes int
	}{
		"nil": {err: nil, want: ""},
		"single wrap": {
			err:  fmt.Errorf("outer: %w", cause),
			want: "*fmt.wrapError > *errors.errorString",
		},
		"joined error follows every branch": {
			err:  fmt.Errorf("%w: %w", llmtest.ErrClientInit, &llm.Error{Message: cause.Error()}),
			want: "*fmt.wrapErrors > *errors.errorString > *llm.Error",
		},
		"branches of a branch": {
			err:  errors.Join(fmt.Errorf("a: %w", cause), &decision.Error{Err: cause}),
			want: "*errors.joinError > *fmt.wrapError > *errors.errorString > *decision.Error > *errors.errorString",
		},
		"a long chain is bounded": {err: deep, maxTypes: maxCauseChain},
		"a wide join is bounded": {
			err:      errors.Join(cause, cause, cause, cause, cause, cause, cause, cause, cause, cause),
			maxTypes: maxCauseChain,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := errorTypeChain(tc.err)
			if strings.Contains(got, cause.Error()) {
				t.Errorf("chain %q carries error text", got)
			}
			if tc.maxTypes > 0 {
				if n := len(strings.Split(got, " > ")); n != tc.maxTypes {
					t.Errorf("chain has %d types, want exactly %d: %q", n, tc.maxTypes, got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("chain = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestModelLookups_UnparseableConfigLogsOnceByTypeOnly(t *testing.T) {
	const secretPath = "/nonexistent/request-supplied-secret-path"
	var logs bytes.Buffer
	h := &modelLookupHandler{svc: &entplatform.ModelConfigService{}, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	probe := entplatform.ModelProbe{
		Provider: "openai", ModelType: "chat",
		Raw: map[string]any{"model": "openai/gpt-4o", "api_key_file": secretPath},
	}

	result := h.result(t.Context(), probe, h.runProbe(t.Context(), probe, ""))

	if result.ErrorClass == nil || *result.ErrorClass != classConfigIncomplete {
		t.Fatalf("errorClass = %v, want %q", result.ErrorClass, classConfigIncomplete)
	}
	out := logs.String()
	if lines := strings.Count(out, "level=WARN"); lines != 1 {
		t.Errorf("WARN lines = %d, want 1 for one event: %s", lines, out)
	}
	if strings.Contains(out, secretPath) {
		t.Errorf("the request-supplied path reached the log: %s", out)
	}
	if !strings.Contains(out, "os.PathError") && !strings.Contains(out, "fs.PathError") {
		t.Errorf("log names no cause type: %s", out)
	}
}

func TestModelLookups_AProbeIsBookedToTheCallerAndTheSavedModel(t *testing.T) {
	h := &modelLookupHandler{logger: slog.Default()}
	probe := entplatform.ModelProbe{ConfigID: "0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"}

	t.Run("a signed-in caller's probe is attributed", func(t *testing.T) {
		ctx := auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "someone", Authenticated: true}))
		rec := h.probeSpend(ctx, probe, "cli")

		if rec == nil || rec.Attribution == nil {
			t.Fatalf("probeSpend = %+v, want an attribution", rec)
		}
		if rec.Attribution.ModelConfigID != probe.ConfigID {
			t.Errorf("modelConfigID = %q, want the probed model %q", rec.Attribution.ModelConfigID, probe.ConfigID)
		}
		if rec.Attribution.Channel != "cli" {
			t.Errorf("channel = %q, want the client surface", rec.Attribution.Channel)
		}
	})

	t.Run("a probe with no identity records nothing", func(t *testing.T) {
		if rec := h.probeSpend(t.Context(), probe, "ui"); rec.Attribution != nil {
			t.Errorf("attribution = %+v, want none without an identity", rec.Attribution)
		}
	})
}

func TestProviderModels_ListsASavedModelStoredWithAnUppercaseProvider(t *testing.T) {
	api, _ := newModelAPI(t)
	provider := newListingProvider(t, answerWith(modelListBody))
	saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "uppercase", "provider": "OpenAI", "modelName": "gpt-4o",
		"apiBase": provider.url, "authConfig": apiKeyAuth(),
	}), "models")

	for _, path := range []string{"/providers/OpenAI/models", "/providers/openai/models"} {
		rec := api.call(http.MethodPut, path, map[string]any{"modelId": saved.ID, "authConfig": keyAuth(redacted)})
		expectStatus(t, rec, http.StatusOK)
		for _, m := range decodeList[SupportedModel](t, rec).Data {
			if m.Provider != "openai" {
				t.Errorf("%s: provider = %q, want openai", path, m.Provider)
			}
		}
	}
	if _, auths := provider.requests(); !slices.Equal(auths, []string{"Bearer " + modelSecret, "Bearer " + modelSecret}) {
		t.Errorf("provider saw credentials %v, want the stored key both times", auths)
	}
}

func TestModels_TestConnectionIsRefusedWhileTheBudgetIsSpent(t *testing.T) {
	resetsAt := time.Now().Add(time.Hour)
	core, _ := newGatedTestCore(t, true, budgettest.BlockedGate(t, resetsAt), nil)
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	var calls atomic.Int32
	base := fakeProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.NotFound(w, nil)
	})

	rec := api.call(http.MethodPut, "/models/testConnection", map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": base,
		"authType": "apikey", "authConfig": keyAuth("k"),
	})

	expectStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on a budget refusal")
	}
	if !strings.Contains(rec.Body.String(), entrypoint.BudgetExhaustedText(budgettest.MeshExhausted(resetsAt))) {
		t.Errorf("body = %s, want the budget refusal text", rec.Body.String())
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("provider called %d times while the budget was spent", n)
	}
}

func azureIdentityToken(t *testing.T, claims map[string]any) {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	token := enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
	file := filepath.Join(t.TempDir(), "azure-identity-token")
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(llm.AzureFederatedTokenFileEnv, file)
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "true")
}

const azureIdentityPath = "/models/azureWorkloadIdentity"

func TestAzureWorkloadIdentityRoute_ReturnsTheSubjectInTheEnvelope(t *testing.T) {
	azureIdentityToken(t, map[string]any{
		"iss": "https://oidc.example.com/issuer", "sub": "system:serviceaccount:sam-ns:sam-runtime",
		"aud": []string{"api://AzureADTokenExchange"},
	})
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodGet, azureIdentityPath, nil)

	expectStatus(t, rec, http.StatusOK)
	var raw map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	want := map[string]any{
		"type": "azureWorkloadIdentity", "issuer": "https://oidc.example.com/issuer",
		"namespace": "sam-ns", "serviceAccount": "sam-runtime",
		"audiences": []any{"api://AzureADTokenExchange"},
	}
	if !reflect.DeepEqual(raw["data"], want) {
		t.Errorf("data = %v, want %v", raw["data"], want)
	}
}

func TestAzureWorkloadIdentityRoute_IsNotFoundWhenTheDeploymentHasNoIdentity(t *testing.T) {
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "")
	t.Setenv(llm.AzureFederatedTokenFileEnv, "")
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodGet, azureIdentityPath, nil)

	expectStatus(t, rec, http.StatusNotFound)
	if got := decodeError(t, rec).Message; got != msgAzureWorkloadIdentityOff {
		t.Errorf("message = %q, want %q", got, msgAzureWorkloadIdentityOff)
	}
}

func TestAzureWorkloadIdentityRoute_KeepsTheReadFailureOffTheWire(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-token")
	t.Setenv(llm.AzureFederatedTokenFileEnv, missing)
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "true")
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodGet, azureIdentityPath, nil)

	expectStatus(t, rec, http.StatusInternalServerError)
	if strings.Contains(rec.Body.String(), missing) || strings.Contains(rec.Body.String(), "no such file") {
		t.Errorf("body carries the read failure: %s", rec.Body.String())
	}
}

func TestAzureWorkloadIdentityRoute_TakesEitherWriteScope(t *testing.T) {
	azureIdentityToken(t, map[string]any{
		"iss": "https://oidc.example.com/issuer", "sub": "system:serviceaccount:sam-ns:sam-runtime",
		"aud": []string{"api://AzureADTokenExchange"},
	})
	api, _ := newModelAPI(t)
	for _, scope := range []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate} {
		if rec := api.call(http.MethodGet, azureIdentityPath, nil, scope); rec.Code != http.StatusOK {
			t.Errorf("with %s = %d, want 200; body %s", scope, rec.Code, rec.Body.String())
		}
	}
	if rec := api.call(http.MethodGet, azureIdentityPath, nil, rbac.ScopeModelConfigDelete); rec.Code != http.StatusForbidden {
		t.Errorf("with only the delete scope = %d, want 403", rec.Code)
	}
}
