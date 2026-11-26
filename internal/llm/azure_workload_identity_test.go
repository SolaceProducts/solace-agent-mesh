// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/maximhq/bifrost/core/providers/azure"
)

type fakeAzureCredential struct {
	token  string
	err    error
	calls  atomic.Int32
	scopes atomic.Value
}

func (f *fakeAzureCredential) GetToken(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f.calls.Add(1)
	f.scopes.Store(opts.Scopes)
	if f.err != nil {
		return azcore.AccessToken{}, f.err
	}
	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func useAzureCredential(t *testing.T, id AzureWorkloadIdentity, cred azcore.TokenCredential) {
	t.Helper()
	azureCredentials.mu.Lock()
	azureCredentials.byID[id] = cred
	azureCredentials.mu.Unlock()
	t.Cleanup(func() {
		azureCredentials.mu.Lock()
		delete(azureCredentials.byID, id)
		azureCredentials.mu.Unlock()
	})
}

func TestAzureWorkloadIdentity_SendsEntraTokenInsteadOfAmbientKey(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "ambient-azure-key")
	id := AzureWorkloadIdentity{ClientID: "client-sends", TenantID: "tenant-sends"}
	cred := &fakeAzureCredential{token: "entra-access-token"}
	useAzureCredential(t, id, cred)

	type headers struct{ authorization, apiKey string }
	seen := make(chan headers, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- headers{r.Header.Get("Authorization"), r.Header.Get("api-key")}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","model":"m",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},
			"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:              "azure",
		Model:                 "gpt-4.1-mini",
		BaseURL:               srv.URL,
		MaxRetries:            new(0),
		AzureWorkloadIdentity: &id,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	if _, err := client.ChatCompletion(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}, MaxTokens: 16}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	got := <-seen
	if got.authorization != "Bearer entra-access-token" {
		t.Errorf("Authorization = %q, want the Entra access token", got.authorization)
	}
	if got.apiKey != "" {
		t.Errorf("api-key = %q, want none", got.apiKey)
	}
	if scopes, _ := cred.scopes.Load().([]string); !slices.Equal(scopes, []string{azure.DefaultAzureScope}) {
		t.Errorf("token scopes = %v, want %v", scopes, []string{azure.DefaultAzureScope})
	}
}

func TestAzureWorkloadIdentity_SignInFailureFailsWithoutRetryOrRequest(t *testing.T) {
	id := AzureWorkloadIdentity{ClientID: "client-fails", TenantID: "tenant-fails"}
	cred := &fakeAzureCredential{err: errors.New("AADSTS70021: No matching federated identity record found")}
	useAzureCredential(t, id, cred)

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:              "azure",
		Model:                 "gpt-4.1-mini",
		BaseURL:               srv.URL,
		MaxRetries:            new(2),
		AzureWorkloadIdentity: &id,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	_, err = client.ChatCompletion(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}, MaxTokens: 16})
	if err == nil || !strings.Contains(err.Error(), "AADSTS70021") {
		t.Fatalf("error = %v, want the Entra sign-in failure", err)
	}
	if n := cred.calls.Load(); n != 1 {
		t.Errorf("sign-in attempts = %d, want 1: a refused sign-in is not transient", n)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("requests to Azure OpenAI = %d, want 0", n)
	}
}

func TestAzureCredentialFor_OneCredentialPerIdentity(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "azure-identity-token")
	if err := os.WriteFile(tokenFile, []byte("k8s-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AzureFederatedTokenFileEnv, tokenFile)
	t.Setenv(AzureWorkloadIdentityEnabledEnv, "true")
	first := AzureWorkloadIdentity{ClientID: "11111111-2222-3333-4444-555555555555", TenantID: "66666666-7777-8888-9999-000000000000"}
	second := AzureWorkloadIdentity{ClientID: "aaaaaaaa-2222-3333-4444-555555555555", TenantID: first.TenantID}
	t.Cleanup(func() {
		azureCredentials.mu.Lock()
		delete(azureCredentials.byID, first)
		delete(azureCredentials.byID, second)
		azureCredentials.mu.Unlock()
	})

	a, err := azureCredentialFor(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := azureCredentialFor(first)
	if err != nil {
		t.Fatal(err)
	}
	other, err := azureCredentialFor(second)
	if err != nil {
		t.Fatal(err)
	}
	if a != again {
		t.Error("the same identity got a second credential, so its cached token is not shared")
	}
	if a == other {
		t.Error("two identities share one credential")
	}
}

func TestAzureWorkloadIdentityAvailable(t *testing.T) {
	cases := []struct {
		name, enabled, tokenFile string
		want                     bool
	}{
		{name: "enabled with a token file", enabled: "true", tokenFile: "/var/run/secrets/sam/azure/token", want: true},
		{name: "token file without the switch, as on a Public datacenter with Blob storage", tokenFile: "/var/run/secrets/azure/tokens/azure-identity-token"},
		{name: "switched off", enabled: "false", tokenFile: "/var/run/secrets/sam/azure/token"},
		{name: "enabled without a token file", enabled: "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AzureWorkloadIdentityEnabledEnv, tc.enabled)
			t.Setenv(AzureFederatedTokenFileEnv, tc.tokenFile)
			if got := AzureWorkloadIdentityAvailable(); got != tc.want {
				t.Errorf("AzureWorkloadIdentityAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAzureCredentialFor_RefusedWhereUnavailable(t *testing.T) {
	t.Setenv(AzureWorkloadIdentityEnabledEnv, "")
	t.Setenv(AzureFederatedTokenFileEnv, "/var/run/secrets/azure/tokens/azure-identity-token")
	_, err := azureCredentialFor(AzureWorkloadIdentity{ClientID: "11111111-2222-3333-4444-555555555555", TenantID: "66666666-7777-8888-9999-000000000000"})
	if err == nil || !strings.Contains(err.Error(), AzureWorkloadIdentityEnabledEnv) {
		t.Fatalf("error = %v, want a refusal naming %s", err, AzureWorkloadIdentityEnabledEnv)
	}
}

func writeServiceAccountToken(t *testing.T, claims map[string]any) {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	token := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AzureFederatedTokenFileEnv, path)
}

func TestReadAzureWorkloadIdentitySubject(t *testing.T) {
	const issuer = "https://oidc.eks.ca-central-1.amazonaws.com/id/EXAMPLE"
	cases := []struct {
		name    string
		claims  map[string]any
		want    AzureWorkloadIdentitySubject
		wantErr bool
	}{
		{
			name:   "projected token",
			claims: map[string]any{"iss": issuer, "sub": "system:serviceaccount:sam:solace-agent-mesh", "aud": "api://AzureADTokenExchange"},
			want:   AzureWorkloadIdentitySubject{Issuer: issuer, Namespace: "sam", ServiceAccount: "solace-agent-mesh", Audiences: []string{"api://AzureADTokenExchange"}},
		},
		{
			name:   "default token with several audiences",
			claims: map[string]any{"iss": issuer, "sub": "system:serviceaccount:sam:sam-sa", "aud": []string{"https://kubernetes.default.svc", "sts.amazonaws.com"}},
			want:   AzureWorkloadIdentitySubject{Issuer: issuer, Namespace: "sam", ServiceAccount: "sam-sa", Audiences: []string{"https://kubernetes.default.svc", "sts.amazonaws.com"}},
		},
		{name: "not a service account", claims: map[string]any{"iss": issuer, "sub": "alice", "aud": "x"}, wantErr: true},
		{name: "no service account name", claims: map[string]any{"iss": issuer, "sub": "system:serviceaccount:sam", "aud": "x"}, wantErr: true},
		{name: "no audience", claims: map[string]any{"iss": issuer, "sub": "system:serviceaccount:sam:sa"}, wantErr: true},
		{name: "no issuer", claims: map[string]any{"sub": "system:serviceaccount:sam:sa", "aud": "x"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeServiceAccountToken(t, tc.claims)
			got, err := ReadAzureWorkloadIdentitySubject()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Issuer != tc.want.Issuer || got.Namespace != tc.want.Namespace || got.ServiceAccount != tc.want.ServiceAccount || !slices.Equal(got.Audiences, tc.want.Audiences) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReadAzureWorkloadIdentitySubject_UnreadableToken(t *testing.T) {
	t.Setenv(AzureFederatedTokenFileEnv, filepath.Join(t.TempDir(), "missing"))
	if _, err := ReadAzureWorkloadIdentitySubject(); err == nil {
		t.Fatal("want an error for a missing token file")
	}
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("not-a-jwt"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AzureFederatedTokenFileEnv, path)
	if _, err := ReadAzureWorkloadIdentitySubject(); err == nil {
		t.Fatal("want an error for a malformed token")
	}
}

func azureChatError(t *testing.T, cred azcore.TokenCredential, respond http.HandlerFunc) error {
	t.Helper()
	id := AzureWorkloadIdentity{ClientID: "client-" + t.Name(), TenantID: "tenant-" + t.Name()}
	useAzureCredential(t, id, cred)
	srv := httptest.NewServer(respond)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:              "azure",
		Model:                 "gpt-4.1-mini",
		BaseURL:               srv.URL,
		MaxRetries:            new(0),
		AzureWorkloadIdentity: &id,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	_, err = client.ChatCompletion(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}, MaxTokens: 16})
	if err == nil {
		t.Fatal("ChatCompletion succeeded, want a failure")
	}
	return err
}

func entraRefusal(body string) error {
	return &azidentity.AuthenticationFailedError{RawResponse: &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Body:       io.NopCloser(strings.NewReader(body)),
	}}
}

func TestAzureWorkloadIdentity_SignInFailureNamesTheSetupProblem(t *testing.T) {
	for _, tc := range []struct {
		name          string
		signInErr     error
		tokenReadable bool
		wantCode      string
		wantMessage   string
		wantDetail    string
		wantCategory  string
	}{
		{
			name: "no matching federated credential",
			signInErr: entraRefusal(`{"error":"invalid_request","error_codes":[70021],
				"error_description":"AADSTS70021: No matching federated identity record found for presented assertion. Assertion Issuer: 'https://issuer'.\r\nTrace ID: t-1\r\nCorrelation ID: c-1"}`),
			wantCode:    "AADSTS70021",
			wantMessage: OtherErrorMessagePrefix + "No federated credential on the Azure identity matches this deployment (AADSTS70021). " + azureAdminHint,
			wantDetail:  "AADSTS70021: No matching federated identity record found for presented assertion. Assertion Issuer: 'https://issuer'.",
		},
		{
			name: "unknown client",
			signInErr: entraRefusal(`{"error":"unauthorized_client","error_codes":[700016],
				"error_description":"AADSTS700016: Application with identifier 'c' was not found in the directory 't'. Trace ID: t-2"}`),
			wantCode:    "AADSTS700016",
			wantMessage: OtherErrorMessagePrefix + "Azure has no application with this model's client ID in its tenant (AADSTS700016). " + azureAdminHint,
			wantDetail:  "AADSTS700016: Application with identifier 'c' was not found in the directory 't'.",
		},
		{
			name: "code outside the catalog",
			signInErr: entraRefusal(`{"error":"invalid_client","error_codes":[7000999],
				"error_description":"AADSTS7000999: Something new."}`),
			wantCode:    "AADSTS7000999",
			wantMessage: OtherErrorMessagePrefix + "Unable to sign in to Azure with the model's workload identity. " + azureAdminHint,
			wantDetail:  "AADSTS7000999: Something new.",
		},
		{
			name:        "token file unreadable",
			signInErr:   errors.New("WorkloadIdentityCredential: open /var/run/secrets/azure/tokens/token: no such file or directory"),
			wantCode:    SignInTokenFileUnreadable,
			wantMessage: OtherErrorMessagePrefix + "Unable to read this deployment's service account token. " + azureAdminHint,
			wantDetail:  "WorkloadIdentityCredential: open /var/run/secrets/azure/tokens/token: no such file or directory",
		},
		{
			name:          "Entra unreachable",
			signInErr:     errors.New("WorkloadIdentityCredential: dial tcp: lookup login.microsoftonline.com: no such host"),
			tokenReadable: true,
			wantCode:      SignInEntraUnreachable,
			wantMessage:   OtherErrorMessagePrefix + "Unable to reach Microsoft Entra to sign in. " + azureAdminHint,
			wantDetail:    "WorkloadIdentityCredential: dial tcp: lookup login.microsoftonline.com: no such host",
			wantCategory:  ErrorCategoryUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "token")
			if tc.tokenReadable {
				if err := os.WriteFile(tokenFile, []byte("sa-token"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(AzureFederatedTokenFileEnv, tokenFile)
			wantCategory := tc.wantCategory
			if wantCategory == "" {
				wantCategory = ErrorCategoryAuthFailure
			}
			err := azureChatError(t, &fakeAzureCredential{err: tc.signInErr}, func(w http.ResponseWriter, _ *http.Request) {
				t.Error("request reached Azure OpenAI after a failed sign-in")
			})

			llmErr, ok := errors.AsType[*Error](err)
			if !ok {
				t.Fatalf("error = %T, want *Error", err)
			}
			if llmErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", llmErr.Code, tc.wantCode)
			}
			if llmErr.Message != tc.wantDetail {
				t.Errorf("Message = %q, want %q", llmErr.Message, tc.wantDetail)
			}
			if got := UserFacingErrorMessage(err); got != tc.wantMessage {
				t.Errorf("UserFacingErrorMessage = %q, want %q", got, tc.wantMessage)
			}
			if got := llmErr.Category(); got != wantCategory {
				t.Errorf("Category = %q, want %q", got, wantCategory)
			}
		})
	}
}

func TestAzureOpenAIRefusal_NamesTheSetupProblem(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			name:        "deployment removed",
			status:      http.StatusNotFound,
			body:        `{"error":{"code":"DeploymentNotFound","message":"The API deployment for this resource does not exist."}}`,
			wantMessage: BadRequestMessagePrefix + "Azure OpenAI has no deployment with this model name at this endpoint (DeploymentNotFound). " + azureAdminHint,
		},
		{
			name:        "role missing",
			status:      http.StatusUnauthorized,
			body:        `{"error":{"code":"PermissionDenied","message":"The principal lacks the required data action."}}`,
			wantMessage: BadRequestMessagePrefix + "The Azure identity is not allowed to call this Azure resource (PermissionDenied). " + azureAdminHint,
		},
		{
			name:        "code outside the catalog",
			status:      http.StatusBadRequest,
			body:        `{"error":{"code":"InvalidPayload","message":"The request is invalid."}}`,
			wantMessage: BadRequestMessagePrefix + "The request is invalid.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := azureChatError(t, &fakeAzureCredential{token: "entra-access-token"}, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			if got := UserFacingErrorMessage(err); got != tc.wantMessage {
				t.Errorf("UserFacingErrorMessage = %q, want %q", got, tc.wantMessage)
			}
		})
	}
}

func TestAzureWorkloadIdentity_StreamSendsEntraToken(t *testing.T) {
	id := AzureWorkloadIdentity{ClientID: "client-stream", TenantID: "tenant-stream"}
	useAzureCredential(t, id, &fakeAzureCredential{token: "entra-stream-token"})

	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get("Authorization"):
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"1","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:              "azure",
		Model:                 "gpt-4.1-mini",
		BaseURL:               srv.URL,
		MaxRetries:            new(0),
		AzureWorkloadIdentity: &id,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	stream, err := client.ChatCompletionStream(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}, MaxTokens: 16})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error: %v", chunk.Err)
		}
	}
	if got := <-seen; got != "Bearer entra-stream-token" {
		t.Errorf("Authorization = %q, want the Entra access token", got)
	}
}

func TestAzureWorkloadIdentity_SignInFailureLogsErrorCode(t *testing.T) {
	id := AzureWorkloadIdentity{ClientID: "client-log", TenantID: "tenant-log"}
	useAzureCredential(t, id, &fakeAzureCredential{err: entraRefusal(`{"error":"invalid_request","error_codes":[70021],
		"error_description":"AADSTS70021: No matching federated identity record found."}`)})
	var logs bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:              "azure",
		Model:                 "gpt-4.1-mini",
		BaseURL:               "https://sam.openai.azure.com/",
		MaxRetries:            new(0),
		AzureWorkloadIdentity: &id,
		Logger:                slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)

	if _, err := client.ChatCompletion(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}, MaxTokens: 16}); err == nil {
		t.Fatal("ChatCompletion succeeded, want the sign-in failure")
	}
	if !strings.Contains(logs.String(), "errorCode=AADSTS70021") {
		t.Errorf("attempt log lacks errorCode=AADSTS70021: %s", logs.String())
	}
}

func TestOverrideCacheKey_DifferentAzureWorkloadIdentities(t *testing.T) {
	a := &BifrostConfig{Model: "gpt-4.1", AzureWorkloadIdentity: &AzureWorkloadIdentity{ClientID: "a", TenantID: "t"}}
	b := &BifrostConfig{Model: "gpt-4.1", AzureWorkloadIdentity: &AzureWorkloadIdentity{ClientID: "b", TenantID: "t"}}
	if cacheKey(t, a) == cacheKey(t, b) {
		t.Error("two Azure identities must not share an override sub-client")
	}
	if cacheKey(t, a) == cacheKey(t, &BifrostConfig{Model: "gpt-4.1"}) {
		t.Error("a model signing in with an identity must not share a sub-client with one that does not")
	}
}

func TestMergeOverrideConfig_TakesTheOverridesAzureIdentity(t *testing.T) {
	parentID := &AzureWorkloadIdentity{ClientID: "parent", TenantID: "t"}
	parent := &BifrostConfig{Provider: "azure", Model: "gpt-4.1", AzureWorkloadIdentity: parentID}

	if got := mergeOverrideConfig(parent, &BifrostConfig{Model: "gpt-4o"}); got.AzureWorkloadIdentity != nil {
		t.Errorf("override without an identity kept the parent's: %+v", got.AzureWorkloadIdentity)
	}
	ovID := &AzureWorkloadIdentity{ClientID: "override", TenantID: "t"}
	if got := mergeOverrideConfig(parent, &BifrostConfig{Model: "gpt-4o", AzureWorkloadIdentity: ovID}); got.AzureWorkloadIdentity != ovID {
		t.Errorf("AzureWorkloadIdentity = %+v, want the override's", got.AzureWorkloadIdentity)
	}
}

func TestAzureWorkloadIdentity_SignsInForTheScopeOfTheEndpointHost(t *testing.T) {
	for _, tc := range []struct {
		endpoint  string
		wantScope string
	}{
		{"https://sam.openai.azure.com/", azure.DefaultAzureScope},
		{"https://sam.cognitiveservices.azure.com/", azure.DefaultAzureScope},
		{"https://sam.services.ai.azure.com/", "https://ai.azure.com/.default"},
		{"https://sam.services.ai.azure.com.attacker.example/", azure.DefaultAzureScope},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			cred := &fakeAzureCredential{token: "entra-access-token"}
			client := &BifrostClient{cfg: BifrostConfig{Provider: "azure", BaseURL: tc.endpoint}, azureCredential: cred}

			if _, bfErr := client.bifrostContext(context.Background()); bfErr != nil {
				t.Fatalf("bifrostContext: %v", bfErr.Error)
			}
			if scopes, _ := cred.scopes.Load().([]string); !slices.Equal(scopes, []string{tc.wantScope}) {
				t.Errorf("token scopes = %v, want [%s]", scopes, tc.wantScope)
			}
		})
	}
}
