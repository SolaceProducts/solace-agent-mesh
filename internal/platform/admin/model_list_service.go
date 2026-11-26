// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/smithy-go"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/gemini"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/openai"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

type bedrockClient interface {
	ListFoundationModels(ctx context.Context, params *bedrock.ListFoundationModelsInput, optFns ...func(*bedrock.Options)) (*bedrock.ListFoundationModelsOutput, error)
	ListInferenceProfiles(ctx context.Context, params *bedrock.ListInferenceProfilesInput, optFns ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error)
}

type gcpTokenSource func(ctx context.Context, saJSON string) (oauth2.TokenSource, error)

type ModelListService struct {
	httpClient           *http.Client
	logger               *slog.Logger
	newBedrockClient     func(ctx context.Context, req *SupportedModelsRequest) (bedrockClient, error)
	newGCPTokenSource    gcpTokenSource
	vertexAIPlatformHost func(location string) string
}

func NewModelListService(httpClient *http.Client, logger *slog.Logger) *ModelListService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ModelListService{
		httpClient:           httpClient,
		logger:               logger,
		newBedrockClient:     defaultBedrockClient,
		newGCPTokenSource:    defaultGCPTokenSource,
		vertexAIPlatformHost: defaultVertexAIPlatformHost,
	}
}

func (s *ModelListService) ListModels(ctx context.Context, req *SupportedModelsRequest) ([]SupportedModel, error) {
	provider := strings.ToLower(req.Provider)
	if strings.TrimSpace(req.APIBase) == "" {
		req.APIBase = platpkg.DefaultAPIBase(provider, nil)
	}
	if req.ModelType == platpkg.ModelTypeDecision {
		switch provider {
		case platpkg.ProviderOpenAI:
			return openAIDecisionModels(), nil
		case platpkg.ProviderGoogleAIStudio:
			return geminiDecisionModels(), nil
		}
	}
	switch provider {
	case "openai", "custom":
		return s.listOpenAI(ctx, req)
	case "anthropic":
		return s.listAnthropic(ctx, req)
	case "google_ai_studio":
		return s.listGoogleAIStudio(ctx, req)
	case "ollama":
		return s.listOllama(ctx, req)
	case "azure_openai":
		return s.listAzureOpenAI(ctx, req)
	case "bedrock":
		return s.listBedrock(ctx, req)
	case "vertex_ai":
		return s.listVertexAI(ctx, req)
	case platpkg.ProviderTypeSafe:
		return typeSafeModels(req.APIBase), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

var commonOpenAIParams = []string{
	"frequency_penalty", "max_tokens", "presence_penalty", "seed",
	"stop", "stream", "temperature", "tool_choice", "tools", "top_p",
}

var providerExtraParams = map[string][]string{
	"openai": {
		"logit_bias", "logprobs", "max_completion_tokens", "parallel_tool_calls",
		"response_format", "top_logprobs", "user",
	},
	"anthropic": {
		"max_completion_tokens", "response_format", "top_k", "user",
	},
	"google_ai_studio": {
		"max_completion_tokens", "response_format", "top_k",
	},
	"vertex_ai": {
		"max_completion_tokens", "response_format", "top_k",
	},
	"azure_openai": {
		"logit_bias", "logprobs", "max_completion_tokens", "parallel_tool_calls",
		"response_format", "top_logprobs", "user",
	},
	"bedrock": {
		"max_completion_tokens", "top_k",
	},
	"ollama": {
		"response_format", "top_k",
	},
}

func (s *ModelListService) ValidateProvider(provider string) error {
	provider = strings.ToLower(provider)
	_, hasExtras := providerExtraParams[provider]
	if hasExtras || provider == "custom" || provider == platpkg.ProviderTypeSafe {
		return nil
	}
	return &apierr.NotFoundError{Resource: "provider", ID: provider}
}

func (s *ModelListService) GetSupportedParams(_ context.Context, provider, _ string) []string {
	provider = strings.ToLower(provider)

	params := make([]string, len(commonOpenAIParams))
	copy(params, commonOpenAIParams)

	if extras, ok := providerExtraParams[provider]; ok {
		params = append(params, extras...)
	} else if provider == "custom" {
		if extras, ok := providerExtraParams["openai"]; ok {
			params = append(params, extras...)
		}
	}

	slices.Sort(params)
	return slices.Compact(params)
}

func (s *ModelListService) listOpenAI(ctx context.Context, req *SupportedModelsRequest) ([]SupportedModel, error) {
	if strings.TrimSpace(req.APIBase) == "" {
		return nil, fmt.Errorf("openai requires api_base")
	}
	url := llm.OpenAIModelsURL(req.APIBase)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if req.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai list models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai list models: status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai parse response: %w", err)
	}

	models := make([]SupportedModel, len(result.Data))
	for i, m := range result.Data {
		models[i] = SupportedModel{ID: m.ID, Label: m.ID, Provider: req.Provider}
	}
	return models, nil
}

func (s *ModelListService) listAnthropic(ctx context.Context, req *SupportedModelsRequest) ([]SupportedModel, error) {
	if strings.TrimSpace(req.APIBase) == "" {
		return nil, fmt.Errorf("anthropic requires api_base")
	}
	url := strings.TrimRight(llm.TrimTrailingV1(req.APIBase), "/") + "/v1/models"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if req.APIKey != "" {
		httpReq.Header.Set("x-api-key", req.APIKey)
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic list models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic list models: status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("anthropic parse response: %w", err)
	}

	var models []SupportedModel
	for _, m := range result.Data {
		if m.Type == "model" {
			models = append(models, SupportedModel{ID: m.ID, Label: m.ID, Provider: req.Provider})
		}
	}
	return models, nil
}

func (s *ModelListService) listGoogleAIStudio(ctx context.Context, req *SupportedModelsRequest) ([]SupportedModel, error) {
	if strings.TrimSpace(req.APIBase) == "" {
		return nil, fmt.Errorf("google_ai_studio requires api_base")
	}
	url := strings.TrimRight(req.APIBase, "/") + "/models"
	if req.APIKey != "" {
		url += "?key=" + req.APIKey
	}

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, logging.UnwrapURLError(err)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("google ai studio list models: %w", logging.UnwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google ai studio list models: status %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("google ai studio parse response: %w", err)
	}

	models := make([]SupportedModel, len(result.Models))
	for i, m := range result.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		models[i] = SupportedModel{ID: id, Label: id, Provider: req.Provider}
	}
	return models, nil
}

func (s *ModelListService) listOllama(ctx context.Context, req *SupportedModelsRequest) ([]SupportedModel, error) {
	apiBase := req.APIBase
	if apiBase == "" {
		apiBase = "http://localhost:11434"
	}
	url := strings.TrimRight(apiBase, "/") + "/api/tags"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama list models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama list models: status %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama parse response: %w", err)
	}

	models := make([]SupportedModel, len(result.Models))
	for i, m := range result.Models {
		name := m.Name
		if idx := strings.LastIndex(name, ":"); idx > 0 {
			name = name[:idx]
		}
		models[i] = SupportedModel{ID: name, Label: name, Provider: req.Provider}
	}
	return models, nil
}

func (s *ModelListService) listAzureOpenAI(_ context.Context, _ *SupportedModelsRequest) ([]SupportedModel, error) {
	return []SupportedModel{}, nil
}

const defaultBedrockRegion = "us-east-1"

var hostSafeAWSRegionRE = regexp.MustCompile(`^[a-z0-9-]+$`)

func (s *ModelListService) listBedrock(ctx context.Context, req *SupportedModelsRequest) ([]SupportedModel, error) {
	region := strings.TrimSpace(req.AWSRegionName)
	if region == "" {
		region = defaultBedrockRegion
	}
	if !hostSafeAWSRegionRE.MatchString(region) {
		return nil, fmt.Errorf("bedrock invalid region %q", region)
	}

	client, err := s.newBedrockClient(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("bedrock list models: %w", err)
	}

	fmOut, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
	if err != nil {
		return nil, fmt.Errorf("bedrock list foundation models: %w", err)
	}
	onDemand := map[string]struct{}{}
	for _, m := range fmOut.ModelSummaries {
		if m.ModelId == nil {
			continue
		}
		if !slices.Contains(m.InferenceTypesSupported, bedrocktypes.InferenceTypeOnDemand) {
			continue
		}
		onDemand[*m.ModelId] = struct{}{}
	}

	var profileIDs []string
	profileBaseIDs := map[string]struct{}{}
	var nextToken *string
	for {
		profiles, err := client.ListInferenceProfiles(ctx, &bedrock.ListInferenceProfilesInput{
			TypeEquals: bedrocktypes.InferenceProfileTypeSystemDefined,
			NextToken:  nextToken,
		})
		if err != nil {
			if isBedrockProfilesFatal(err) {
				return nil, fmt.Errorf("bedrock list inference profiles: %w", err)
			}
			s.logger.Warn("bedrock list inference profiles unavailable; using foundation models only", "error", err)
			break
		}
		for _, p := range profiles.InferenceProfileSummaries {
			if p.InferenceProfileId == nil {
				continue
			}
			id := *p.InferenceProfileId
			profileIDs = append(profileIDs, id)
			if idx := strings.Index(id, "."); idx > 0 {
				profileBaseIDs[id[idx+1:]] = struct{}{}
			}
		}
		if profiles.NextToken == nil || *profiles.NextToken == "" {
			break
		}
		nextToken = profiles.NextToken
	}

	onDemandIDs := make([]string, 0, len(onDemand))
	for id := range onDemand {
		onDemandIDs = append(onDemandIDs, id)
	}
	slices.Sort(onDemandIDs)
	slices.Sort(profileIDs)

	merged := make([]string, 0, len(profileIDs)+len(onDemandIDs))
	merged = append(merged, profileIDs...)
	for _, id := range onDemandIDs {
		if _, covered := profileBaseIDs[id]; covered {
			continue
		}
		merged = append(merged, id)
	}

	models := make([]SupportedModel, len(merged))
	for i, id := range merged {
		models[i] = SupportedModel{ID: id, Label: id, Provider: req.Provider}
	}
	return models, nil
}

func isBedrockProfilesFatal(err error) bool {
	if err == nil {
		return false
	}
	apiErr, ok := errors.AsType[smithy.APIError](err)
	if !ok {
		return true
	}
	switch apiErr.ErrorCode() {
	case "AccessDeniedException",
		"ThrottlingException", "TooManyRequestsException",
		"InternalServerException", "ServiceUnavailableException":
		return true
	}
	return false
}

var vertexPublishers = []string{"google", "anthropic", "mistralai"}

type vertexHTTPError struct {
	publisher string
	status    int
}

func (e *vertexHTTPError) Error() string {
	return fmt.Sprintf("vertex_ai list publisher %s: status %d", e.publisher, e.status)
}

const (
	vertexDefaultLocation = "us-central1"
	vertexPageSize        = 100
)

var vertexLocationRE = regexp.MustCompile(`^[a-z0-9-]+$`)

func (s *ModelListService) listVertexAI(ctx context.Context, req *SupportedModelsRequest) ([]SupportedModel, error) {
	saJSON := strings.TrimSpace(req.VertexCredentials)
	if saJSON == "" {
		return nil, fmt.Errorf("vertex_ai requires vertex_credentials")
	}

	tokenSource, err := s.newGCPTokenSource(ctx, saJSON)
	if err != nil {
		return nil, fmt.Errorf("vertex_ai token source: %w", err)
	}

	location := strings.TrimSpace(req.VertexLocation)
	if location == "" {
		location = vertexDefaultLocation
	}
	if !vertexLocationRE.MatchString(location) {
		return nil, fmt.Errorf("vertex_ai invalid location %q", location)
	}
	host := s.vertexAIPlatformHost(location)

	var ids []string
	for _, publisher := range vertexPublishers {
		publisherIDs, err := s.fetchVertexPublisherModels(ctx, host, publisher, tokenSource)
		if err != nil {
			if publisher == "google" {
				return nil, err
			}
			if httpErr, ok := errors.AsType[*vertexHTTPError](err); ok &&
				(httpErr.status == http.StatusForbidden || httpErr.status == http.StatusNotFound) {
				s.logger.Warn("vertex_ai publisher not available in region; skipping",
					"publisher", publisher, "location", location, "httpStatus", httpErr.status)
				continue
			}
			return nil, err
		}
		ids = append(ids, publisherIDs...)
	}

	models := make([]SupportedModel, len(ids))
	for i, id := range ids {
		models[i] = SupportedModel{ID: id, Label: id, Provider: req.Provider}
	}
	return models, nil
}

func (s *ModelListService) fetchVertexPublisherModels(ctx context.Context, host, publisher string, tokenSource oauth2.TokenSource) ([]string, error) {
	var ids []string
	pageToken := ""
	for {
		pageIDs, next, err := s.fetchVertexPublisherPage(ctx, host, publisher, tokenSource, pageToken)
		if err != nil {
			return nil, err
		}
		ids = append(ids, pageIDs...)
		if next == "" {
			return ids, nil
		}
		pageToken = next
	}
}

func (s *ModelListService) fetchVertexPublisherPage(ctx context.Context, host, publisher string, tokenSource oauth2.TokenSource, pageToken string) ([]string, string, error) {
	u := fmt.Sprintf("https://%s/v1beta1/publishers/%s/models?pageSize=%d", host, publisher, vertexPageSize)
	if pageToken != "" {
		u += "&pageToken=" + url.QueryEscape(pageToken)
	}

	token, err := tokenSource.Token()
	if err != nil {
		return nil, "", fmt.Errorf("vertex_ai token: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("vertex_ai list publisher %s: %w", publisher, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", &vertexHTTPError{publisher: publisher, status: resp.StatusCode}
	}

	var parsed struct {
		PublisherModels []struct {
			Name string `json:"name"`
		} `json:"publisherModels"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", fmt.Errorf("vertex_ai parse publisher %s: %w", publisher, err)
	}

	ids := make([]string, 0, len(parsed.PublisherModels))
	for _, m := range parsed.PublisherModels {
		if m.Name == "" {
			continue
		}
		if idx := strings.LastIndex(m.Name, "/"); idx >= 0 {
			ids = append(ids, m.Name[idx+1:])
		} else {
			ids = append(ids, m.Name)
		}
	}
	return ids, parsed.NextPageToken, nil
}

func defaultBedrockClient(ctx context.Context, req *SupportedModelsRequest) (bedrockClient, error) {
	region := strings.TrimSpace(req.AWSRegionName)
	if region == "" {
		region = defaultBedrockRegion
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if req.AWSAccessKeyID != "" && req.AWSSecretAccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(req.AWSAccessKeyID, req.AWSSecretAccessKey, req.AWSSessionToken),
		))
	}
	cfg, err := awsretry.Load(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return bedrock.NewFromConfig(cfg), nil
}

func defaultGCPTokenSource(ctx context.Context, saJSON string) (oauth2.TokenSource, error) {
	creds, err := google.CredentialsFromJSONWithTypeAndParams(ctx, []byte(saJSON), google.ServiceAccount, google.CredentialsParams{
		Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
	})
	if err != nil {
		return nil, fmt.Errorf("parse service account: %w", err)
	}
	return creds.TokenSource, nil
}

func defaultVertexAIPlatformHost(location string) string {
	if location == "global" {
		return "aiplatform.googleapis.com"
	}
	return location + "-aiplatform.googleapis.com"
}

func openAIDecisionModels() []SupportedModel {
	return []SupportedModel{{ID: openai.DefaultModel, Label: openai.DefaultModel, Provider: platpkg.ProviderOpenAI}}
}

func geminiDecisionModels() []SupportedModel {
	return []SupportedModel{{ID: gemini.DefaultModel, Label: gemini.DefaultModel, Provider: platpkg.ProviderGoogleAIStudio}}
}

func typeSafeModels(apiBase string) []SupportedModel {
	ids := []string{"jev-1.13.0", "jev-latest"}
	if strings.Contains(apiBase, "openrouter.ai") {
		ids = []string{"typesafe/jev-1.13", "~typesafe/jev-latest"}
	}
	models := make([]SupportedModel, len(ids))
	for i, id := range ids {
		models[i] = SupportedModel{ID: id, Label: id, Provider: platpkg.ProviderTypeSafe}
	}
	return models
}
