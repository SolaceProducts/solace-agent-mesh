// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/smithy-go"
	"golang.org/x/oauth2"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

func TestModelListService_OpenAI(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("missing or wrong auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "openai",
		APIBase:  server.URL,
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2", len(models))
	}
	if models[0].ID != "gpt-4" {
		t.Errorf("models[0].ID = %q, want gpt-4", models[0].ID)
	}
}

func TestModelListService_StripsTrailingV1(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"openai", "custom"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					t.Errorf("unexpected path: %s, want /v1/models", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"}]}`))
			}))
			defer server.Close()

			svc := NewModelListService(nil, slog.Default())
			models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
				Provider: provider,
				APIBase:  server.URL + "/v1",
				APIKey:   "sk-test",
			})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(models) != 1 || models[0].ID != "gpt-4" {
				t.Fatalf("models = %+v, want one gpt-4", models)
			}
		})
	}
}

func TestModelListService_Custom(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"minimax/minimax-m3"}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "custom",
		APIBase:  server.URL,
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("len = %d, want 1", len(models))
	}
	if models[0].ID != "minimax/minimax-m3" {
		t.Errorf("models[0].ID = %q, want minimax/minimax-m3", models[0].ID)
	}
	if models[0].Provider != "custom" {
		t.Errorf("models[0].Provider = %q, want custom", models[0].Provider)
	}
}

func TestModelListService_Anthropic(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant" {
			t.Errorf("missing x-api-key")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("missing anthropic-version")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-3-opus","type":"model"},{"id":"something","type":"other"}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "anthropic",
		APIBase:  server.URL,
		APIKey:   "sk-ant",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("len = %d, want 1 (only type=model)", len(models))
	}
	if models[0].ID != "claude-3-opus" {
		t.Errorf("models[0].ID = %q, want claude-3-opus", models[0].ID)
	}
}

func TestModelListService_AnthropicStripsTrailingV1(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-3-opus","type":"model"}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "anthropic",
		APIBase:  server.URL + "/v1",
		APIKey:   "sk-ant",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 1 || models[0].ID != "claude-3-opus" {
		t.Fatalf("models = %+v, want one claude-3-opus", models)
	}
}

func TestModelListService_GoogleAIStudio(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "my-key" {
			t.Errorf("missing key param")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-pro"},{"name":"models/gemini-1.5-flash"}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "google_ai_studio",
		APIBase:  server.URL,
		APIKey:   "my-key",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2", len(models))
	}
	if models[0].ID != "gemini-pro" {
		t.Errorf("models[0].ID = %q, want gemini-pro (models/ prefix stripped)", models[0].ID)
	}
}

func TestModelListService_Ollama(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3:latest"},{"name":"codellama:7b"}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "ollama",
		APIBase:  server.URL,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len = %d, want 2", len(models))
	}
	if models[0].ID != "llama3" {
		t.Errorf("models[0].ID = %q, want llama3 (tag stripped)", models[0].ID)
	}
	if models[1].ID != "codellama" {
		t.Errorf("models[1].ID = %q, want codellama (tag stripped)", models[1].ID)
	}
}

type fakeBedrockClient struct {
	foundation    []bedrocktypes.FoundationModelSummary
	foundationErr error
	profiles      [][]bedrocktypes.InferenceProfileSummary
	profileErr    error
	callIdx       int
}

func (f *fakeBedrockClient) ListFoundationModels(_ context.Context, _ *bedrock.ListFoundationModelsInput, _ ...func(*bedrock.Options)) (*bedrock.ListFoundationModelsOutput, error) {
	if f.foundationErr != nil {
		return nil, f.foundationErr
	}
	return &bedrock.ListFoundationModelsOutput{ModelSummaries: f.foundation}, nil
}

func (f *fakeBedrockClient) ListInferenceProfiles(_ context.Context, _ *bedrock.ListInferenceProfilesInput, _ ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error) {
	if f.profileErr != nil {
		return nil, f.profileErr
	}
	if f.callIdx >= len(f.profiles) {
		return &bedrock.ListInferenceProfilesOutput{}, nil
	}
	page := f.profiles[f.callIdx]
	f.callIdx++
	var next *string
	if f.callIdx < len(f.profiles) {
		next = aws.String(fmt.Sprintf("token-%d", f.callIdx))
	}
	return &bedrock.ListInferenceProfilesOutput{
		InferenceProfileSummaries: page,
		NextToken:                 next,
	}, nil
}

func TestModelListService_Bedrock(t *testing.T) {
	t.Parallel()

	fake := &fakeBedrockClient{
		foundation: []bedrocktypes.FoundationModelSummary{
			{
				ModelId:                 aws.String("anthropic.claude-3-haiku-20240307-v1:0"),
				InferenceTypesSupported: []bedrocktypes.InferenceType{bedrocktypes.InferenceTypeOnDemand},
			},
			{
				ModelId:                 aws.String("amazon.titan-text-express-v1"),
				InferenceTypesSupported: []bedrocktypes.InferenceType{bedrocktypes.InferenceTypeOnDemand},
			},
			{
				ModelId:                 aws.String("some.provisioned-only:0"),
				InferenceTypesSupported: []bedrocktypes.InferenceType{"PROVISIONED"},
			},
		},
		profiles: [][]bedrocktypes.InferenceProfileSummary{
			{
				{InferenceProfileId: aws.String("us.anthropic.claude-3-haiku-20240307-v1:0")},
			},
		},
	}

	svc := NewModelListService(nil, slog.Default())
	svc.newBedrockClient = func(context.Context, *SupportedModelsRequest) (bedrockClient, error) {
		return fake, nil
	}

	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:           "bedrock",
		AWSAccessKeyID:     "AKIA",
		AWSSecretAccessKey: "secret",
		AWSRegionName:      "us-east-1",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}

	want := []string{
		"us.anthropic.claude-3-haiku-20240307-v1:0",
		"amazon.titan-text-express-v1",
	}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestModelListService_Bedrock_ProfilesUnsupportedFallsBack(t *testing.T) {
	t.Parallel()

	fake := &fakeBedrockClient{
		foundation: []bedrocktypes.FoundationModelSummary{
			{
				ModelId:                 aws.String("amazon.titan-text-lite-v1"),
				InferenceTypesSupported: []bedrocktypes.InferenceType{bedrocktypes.InferenceTypeOnDemand},
			},
		},
		profileErr: &smithy.GenericAPIError{Code: "UnknownOperationException", Message: "not supported here"},
	}

	svc := NewModelListService(nil, slog.Default())
	svc.newBedrockClient = func(context.Context, *SupportedModelsRequest) (bedrockClient, error) {
		return fake, nil
	}

	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{Provider: "bedrock"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 1 || models[0].ID != "amazon.titan-text-lite-v1" {
		t.Fatalf("models = %+v, want [amazon.titan-text-lite-v1]", models)
	}
}

func TestModelListService_Bedrock_ProfilesErrorsPropagate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"AccessDenied", &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "denied"}},
		{"Throttling", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow down"}},
		{"TooManyRequests", &smithy.GenericAPIError{Code: "TooManyRequestsException", Message: "slow down"}},
		{"InternalServer", &smithy.GenericAPIError{Code: "InternalServerException", Message: "boom"}},
		{"ServiceUnavailable", &smithy.GenericAPIError{Code: "ServiceUnavailableException", Message: "down"}},
		{"non-APIError (network/transport)", errors.New("dial tcp: no such host")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeBedrockClient{
				foundation: []bedrocktypes.FoundationModelSummary{
					{
						ModelId:                 aws.String("amazon.titan-text-lite-v1"),
						InferenceTypesSupported: []bedrocktypes.InferenceType{bedrocktypes.InferenceTypeOnDemand},
					},
				},
				profileErr: tc.err,
			}
			svc := NewModelListService(nil, slog.Default())
			svc.newBedrockClient = func(context.Context, *SupportedModelsRequest) (bedrockClient, error) {
				return fake, nil
			}
			_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{Provider: "bedrock"})
			if err == nil {
				t.Fatalf("%s: expected error to propagate, got nil", tc.name)
			}
		})
	}
}

func TestModelListService_Bedrock_ProfilesPagination(t *testing.T) {
	t.Parallel()

	fake := &fakeBedrockClient{
		profiles: [][]bedrocktypes.InferenceProfileSummary{
			{{InferenceProfileId: aws.String("us.model.a")}},
			{{InferenceProfileId: aws.String("us.model.b")}},
		},
	}

	svc := NewModelListService(nil, slog.Default())
	svc.newBedrockClient = func(context.Context, *SupportedModelsRequest) (bedrockClient, error) {
		return fake, nil
	}

	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{Provider: "bedrock"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 2 || models[0].ID != "us.model.a" || models[1].ID != "us.model.b" {
		t.Fatalf("models = %+v, want [us.model.a us.model.b]", models)
	}
}

func TestModelListService_ListErrorsOmitProviderBody(t *testing.T) {
	t.Parallel()
	const sentinel = "sk-abc123-SENTINEL-BODY"

	cases := []struct {
		name     string
		req      func(apiBase string) *SupportedModelsRequest
		handler  http.HandlerFunc
		setupSvc func(svc *ModelListService, serverURL string)
	}{
		{
			name: "openai",
			req: func(apiBase string) *SupportedModelsRequest {
				return &SupportedModelsRequest{Provider: "openai", APIBase: apiBase, APIKey: "key"}
			},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"` + sentinel + `"}`))
			},
		},
		{
			name: "anthropic",
			req: func(apiBase string) *SupportedModelsRequest {
				return &SupportedModelsRequest{Provider: "anthropic", APIBase: apiBase, APIKey: "key"}
			},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"` + sentinel + `"}`))
			},
		},
		{
			name: "ollama",
			req: func(apiBase string) *SupportedModelsRequest {
				return &SupportedModelsRequest{Provider: "ollama", APIBase: apiBase}
			},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(sentinel))
			},
		},
		{
			name: "google_ai_studio",
			req: func(apiBase string) *SupportedModelsRequest {
				return &SupportedModelsRequest{Provider: "google_ai_studio", APIBase: apiBase, APIKey: "key"}
			},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(sentinel))
			},
		},
		{
			name: "vertex_ai",
			req: func(_ string) *SupportedModelsRequest {
				return &SupportedModelsRequest{
					Provider:          "vertex_ai",
					VertexCredentials: `{"type":"service_account"}`,
				}
			},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(sentinel))
			},
			setupSvc: func(svc *ModelListService, serverURL string) {
				svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
					return staticTokenSource{token: "tok"}, nil
				}
				svc.vertexAIPlatformHost = func(string) string { return strings.TrimPrefix(serverURL, "http://") }
				svc.httpClient.Transport = rewriteToHTTPTransport{fallback: http.DefaultTransport}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			svc := NewModelListService(nil, slog.Default())
			if tc.setupSvc != nil {
				tc.setupSvc(svc, server.URL)
			}
			_, err := svc.ListModels(context.Background(), tc.req(server.URL))
			if err == nil {
				t.Fatalf("expected error from %s list on non-200", tc.name)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Errorf("%s err = %q, must not echo the provider's response body", tc.name, err.Error())
			}
		})
	}
}

func TestNewModelListService_NilLoggerFallsBackToDefault(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, nil)
	if svc.logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestDefaultBedrockClient(t *testing.T) {
	t.Parallel()

	client, err := defaultBedrockClient(t.Context(), &SupportedModelsRequest{
		AWSAccessKeyID:     "AKIA",
		AWSSecretAccessKey: "secret",
		AWSRegionName:      "us-east-1",
	})
	if err != nil {
		t.Fatalf("with creds: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	client, err = defaultBedrockClient(t.Context(), &SupportedModelsRequest{})
	if err != nil {
		t.Fatalf("without creds (ambient chain): %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client from ambient chain")
	}
}

func TestDefaultBedrockClient_KeepsLegacyRetryer(t *testing.T) {
	t.Parallel()

	client, err := defaultBedrockClient(t.Context(), &SupportedModelsRequest{
		AWSAccessKeyID:     "AKIA",
		AWSSecretAccessKey: "secret",
		AWSRegionName:      "us-east-1",
	})
	if err != nil {
		t.Fatalf("defaultBedrockClient: %v", err)
	}
	bc, ok := client.(*bedrock.Client)
	if !ok {
		t.Fatalf("client is %T, want *bedrock.Client", client)
	}
	if !awsretry.IsLegacy(bc.Options().Retryer) {
		t.Error("bedrock client does not use the legacy retryer")
	}
}

func TestDefaultGCPTokenSource_MalformedJSON(t *testing.T) {
	t.Parallel()
	if _, err := defaultGCPTokenSource(t.Context(), "not-a-json-object"); err == nil {
		t.Fatal("expected error for malformed service account JSON")
	}
}

func TestModelListService_Bedrock_FoundationErrorPropagates(t *testing.T) {
	t.Parallel()
	fake := &fakeBedrockClient{
		foundationErr: errors.New("aws api down"),
	}
	svc := NewModelListService(nil, slog.Default())
	svc.newBedrockClient = func(context.Context, *SupportedModelsRequest) (bedrockClient, error) {
		return fake, nil
	}
	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{Provider: "bedrock"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestModelListService_Bedrock_ClientFactoryErrorPropagates(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, slog.Default())
	svc.newBedrockClient = func(context.Context, *SupportedModelsRequest) (bedrockClient, error) {
		return nil, errors.New("failed to load AWS config")
	}
	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{Provider: "bedrock"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestModelListService_Bedrock_RejectsInvalidRegion(t *testing.T) {
	t.Parallel()
	cases := []string{"attacker.com/", "us-east-1/../evil", "us-east-1:8080", "US-EAST-1"}
	for _, region := range cases {
		t.Run(region, func(t *testing.T) {
			t.Parallel()
			svc := NewModelListService(nil, slog.Default())
			svc.newBedrockClient = func(context.Context, *SupportedModelsRequest) (bedrockClient, error) {
				t.Fatal("newBedrockClient must not be called for an invalid region")
				return nil, nil
			}
			_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
				Provider:      "bedrock",
				AWSRegionName: region,
			})
			if err == nil {
				t.Fatalf("expected error for region %q", region)
			}
		})
	}
}

type staticTokenSource struct{ token string }

func (s staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token}, nil
}

type erroringTokenSource struct{ err error }

func (s erroringTokenSource) Token() (*oauth2.Token, error) { return nil, s.err }

func TestModelListService_VertexAI(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer test-access-token" {
			t.Errorf("Authorization = %q, want Bearer test-access-token", got)
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1beta1/publishers/google/models"):
			if r.URL.Query().Get("pageToken") == "" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"publisherModels":[{"name":"publishers/google/models/gemini-2.0-flash"}],"nextPageToken":"page2"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"publisherModels":[{"name":"publishers/google/models/gemini-1.5-pro"}]}`))
		case strings.HasPrefix(r.URL.Path, "/v1beta1/publishers/anthropic/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"publisherModels":[{"name":"publishers/anthropic/models/claude-3-5-sonnet-v2@20241022"}]}`))
		case strings.HasPrefix(r.URL.Path, "/v1beta1/publishers/mistralai/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"publisherModels":[{"name":"publishers/mistralai/models/mistral-large"}]}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
		return staticTokenSource{token: "test-access-token"}, nil
	}
	svc.vertexAIPlatformHost = func(string) string {
		return strings.TrimPrefix(server.URL, "http://")
	}
	svc.httpClient = server.Client()
	svc.httpClient.Transport = rewriteToHTTPTransport{fallback: http.DefaultTransport}

	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:          "vertex_ai",
		VertexCredentials: `{"type":"service_account"}`,
		VertexLocation:    "us-central1",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	got := make([]string, len(models))
	for i, m := range models {
		got[i] = m.ID
	}
	want := []string{
		"gemini-2.0-flash",
		"gemini-1.5-pro",
		"claude-3-5-sonnet-v2@20241022",
		"mistral-large",
	}
	if len(got) != len(want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestModelListService_VertexAI_SkipsUnavailablePublishers(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1beta1/publishers/google/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"publisherModels":[{"name":"publishers/google/models/gemini-1.5-flash"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"publisher not available in this region"}`))
		}
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
		return staticTokenSource{token: "tok"}, nil
	}
	svc.vertexAIPlatformHost = func(string) string { return strings.TrimPrefix(server.URL, "http://") }
	svc.httpClient = server.Client()
	svc.httpClient.Transport = rewriteToHTTPTransport{fallback: http.DefaultTransport}

	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:          "vertex_ai",
		VertexCredentials: `{"type":"service_account"}`,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gemini-1.5-flash" {
		t.Fatalf("models = %+v, want [gemini-1.5-flash]", models)
	}
}

func TestModelListService_VertexAI_GoogleErrorFails(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"permission denied"}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
		return staticTokenSource{token: "tok"}, nil
	}
	svc.vertexAIPlatformHost = func(string) string { return strings.TrimPrefix(server.URL, "http://") }
	svc.httpClient = server.Client()
	svc.httpClient.Transport = rewriteToHTTPTransport{fallback: http.DefaultTransport}

	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:          "vertex_ai",
		VertexCredentials: `{"type":"service_account"}`,
	})
	if err == nil {
		t.Fatal("expected error when google publisher returns 403")
	}
}

func TestModelListService_VertexAI_RequiresCredentials(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, slog.Default())
	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{Provider: "vertex_ai"})
	if err == nil {
		t.Fatal("expected error when vertex_credentials is missing")
	}
}

func TestModelListService_VertexAI_TokenSourceErrorPropagates(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, slog.Default())
	svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
		return nil, errors.New("service account key malformed")
	}
	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:          "vertex_ai",
		VertexCredentials: `{"type":"service_account"}`,
	})
	if err == nil {
		t.Fatal("expected error when token source fails")
	}
}

func TestModelListService_VertexAI_TokenFetchErrorPropagates(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, slog.Default())
	svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
		return erroringTokenSource{err: errors.New("token endpoint unreachable")}, nil
	}
	svc.vertexAIPlatformHost = func(string) string { return "vertex.example" }
	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:          "vertex_ai",
		VertexCredentials: `{"type":"service_account"}`,
	})
	if err == nil {
		t.Fatal("expected error when Token() fails")
	}
}

func TestModelListService_VertexAI_NonGoogle5xxPropagates(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1beta1/publishers/google/models"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"publisherModels":[{"name":"publishers/google/models/gemini-pro"}]}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"backend transiently unavailable"}`))
		}
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
		return staticTokenSource{token: "tok"}, nil
	}
	svc.vertexAIPlatformHost = func(string) string { return strings.TrimPrefix(server.URL, "http://") }
	svc.httpClient = server.Client()
	svc.httpClient.Transport = rewriteToHTTPTransport{fallback: http.DefaultTransport}

	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:          "vertex_ai",
		VertexCredentials: `{"type":"service_account"}`,
	})
	if err == nil {
		t.Fatal("expected 5xx from non-Google publisher to propagate, not be silently skipped")
	}
}

func TestModelListService_VertexAI_RejectsInvalidLocation(t *testing.T) {
	t.Parallel()

	cases := []string{
		"attacker.com/",
		"us-central1/../evil",
		"us-central1#fragment",
		"us-central1?query",
		"us-central1:8080",
		"us-central1@evil.com",
		"US-CENTRAL1",
	}
	for _, loc := range cases {
		t.Run(loc, func(t *testing.T) {
			t.Parallel()
			svc := NewModelListService(nil, slog.Default())
			svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
				return staticTokenSource{token: "tok"}, nil
			}
			svc.vertexAIPlatformHost = func(string) string {
				t.Fatal("vertexAIPlatformHost must not be called for an invalid location")
				return ""
			}
			_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
				Provider:          "vertex_ai",
				VertexCredentials: `{"type":"service_account"}`,
				VertexLocation:    loc,
			})
			if err == nil {
				t.Fatalf("expected error for location %q", loc)
			}
		})
	}
}

func TestDefaultVertexAIPlatformHost(t *testing.T) {
	t.Parallel()
	cases := []struct{ location, want string }{
		{"global", "aiplatform.googleapis.com"},
		{"us-central1", "us-central1-aiplatform.googleapis.com"},
		{"europe-west4", "europe-west4-aiplatform.googleapis.com"},
	}
	for _, tc := range cases {
		if got := defaultVertexAIPlatformHost(tc.location); got != tc.want {
			t.Errorf("defaultVertexAIPlatformHost(%q) = %q, want %q", tc.location, got, tc.want)
		}
	}
}

func TestModelListService_VertexAI_BareModelName(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta1/publishers/google/models") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"publisherModels":[{"name":"bare-model-name"},{"name":"publishers/google/models/gemini-pro"},{"name":""}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	svc.newGCPTokenSource = func(context.Context, string) (oauth2.TokenSource, error) {
		return staticTokenSource{token: "tok"}, nil
	}
	svc.vertexAIPlatformHost = func(string) string { return strings.TrimPrefix(server.URL, "http://") }
	svc.httpClient = server.Client()
	svc.httpClient.Transport = rewriteToHTTPTransport{fallback: http.DefaultTransport}

	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider:          "vertex_ai",
		VertexCredentials: `{"type":"service_account"}`,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	want := []string{"bare-model-name", "gemini-pro"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

type rewriteToHTTPTransport struct{ fallback http.RoundTripper }

func (t rewriteToHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req = req.Clone(req.Context())
		req.URL.Scheme = "http"
	}
	return t.fallback.RoundTrip(req)
}

func TestModelListService_UnsupportedProvider(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, slog.Default())
	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "unknown_provider",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestModelListService_RequiresAPIBase(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, slog.Default())
	_, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "custom",
		APIBase:  "",
	})
	if err == nil {
		t.Fatal("custom with empty apiBase must fail")
	}
}

func TestModelListService_AzureReturnsEmpty(t *testing.T) {
	t.Parallel()
	svc := NewModelListService(nil, slog.Default())
	models, err := svc.ListModels(context.Background(), &SupportedModelsRequest{
		Provider: "azure_openai",
		APIBase:  "https://example.openai.azure.com/",
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected empty model list for azure_openai, got %d models", len(models))
	}
}

func TestModelListService_ValidateProvider(t *testing.T) {
	svc := NewModelListService(nil, nil)

	for _, provider := range []string{
		"openai", "custom", "anthropic", "google_ai_studio", "ollama",
		"azure_openai", "bedrock", "vertex_ai", platpkg.ProviderTypeSafe, "OpenAI",
	} {
		if err := svc.ValidateProvider(provider); err != nil {
			t.Errorf("ValidateProvider(%q) = %v, want nil", provider, err)
		}
	}

	for _, provider := range []string{"not_a_provider", ""} {
		err := svc.ValidateProvider(provider)
		if _, ok := errors.AsType[*apierr.NotFoundError](err); !ok {
			t.Errorf("ValidateProvider(%q) = %T, want *apierr.NotFoundError", provider, err)
		}
	}
}

func TestModelListService_ModelsRequestLineForExistingBaseURLs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, suffix, wantPath, wantQuery string
	}{
		{"bare host", "", "/v1/models", ""},
		{"trailing slash", "/", "/v1/models", ""},
		{"trailing v1", "/v1", "/v1/models", ""},
		{"trailing v1 slash", "/v1/", "/v1/models", ""},
		{"gateway prefix", "/openai", "/openai/v1/models", ""},
		{"other version prefix", "/api/v3", "/api/v3/v1/models", ""},
		{"query on the base", "/api/v3?k=v", "/api/v3", "k=v/v1/models"},
		{"chat path inside the base", "/chat/completions/proxy", "/chat/completions/proxy/v1/models", ""},
		{"v1 after a chat path", "/chat/completions/v1", "/chat/completions/v1/models", ""},
	} {
		for _, provider := range []string{"openai", "custom"} {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				gotPath, gotQuery := listModelsRequestLine(t, provider, tc.suffix)
				if gotPath != tc.wantPath || gotQuery != tc.wantQuery {
					t.Errorf("models request line = %q?%q, want %q?%q", gotPath, gotQuery, tc.wantPath, tc.wantQuery)
				}
			})
		}
	}
}

func TestModelListService_CompleteEndpointListsItsSibling(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"openai", "custom"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			if path, query := listModelsRequestLine(t, provider, "/api/v3/chat/completions/"); path != "/api/v3/models" || query != "" {
				t.Errorf("models request line = %q?%q, want /api/v3/models", path, query)
			}
			if path, query := listModelsRequestLine(t, provider, "/api/v3/chat/completions?k=v"); path != "/api/v3/models" || query != "k=v" {
				t.Errorf("models request line = %q?%q, want /api/v3/models?k=v", path, query)
			}
		})
	}
}

func listModelsRequestLine(t *testing.T, provider, baseSuffix string) (path, query string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer server.Close()

	svc := NewModelListService(nil, slog.Default())
	if _, err := svc.ListModels(t.Context(), &SupportedModelsRequest{
		Provider: provider,
		APIBase:  server.URL + baseSuffix,
		APIKey:   "sk-test",
	}); err != nil {
		t.Fatalf("list: %v", err)
	}
	return path, query
}
