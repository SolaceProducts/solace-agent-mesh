// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func TestSanitizeSkills_Valid(t *testing.T) {
	t.Parallel()
	skills := []SkillSuggestion{
		{Name: "Analyze Sales Trends", Description: "Analyze quarterly sales data and identify emerging trends"},
		{Name: "Generate Reports", Description: "Create monthly summary reports from database records"},
	}
	result := sanitizeSkills(skills)
	if len(result) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result))
	}
	if result[0].Name != "Analyze Sales Trends" {
		t.Errorf("unexpected name: %s", result[0].Name)
	}
}

func TestSanitizeSkills_Deduplication(t *testing.T) {
	t.Parallel()
	skills := []SkillSuggestion{
		{Name: "Query Database", Description: "Execute database queries for reporting"},
		{Name: "query database", Description: "Run SQL queries against the data warehouse"},
	}
	result := sanitizeSkills(skills)
	if len(result) != 1 {
		t.Fatalf("expected 1 skill after dedup, got %d", len(result))
	}
}

func TestSanitizeSkills_Truncation(t *testing.T) {
	t.Parallel()
	skills := []SkillSuggestion{
		{Name: strings.Repeat("A", 50), Description: strings.Repeat("B", 300)},
	}
	result := sanitizeSkills(skills)
	if len(result) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result))
	}
	if len(result[0].Name) != 40 {
		t.Errorf("name should be truncated to 40, got %d", len(result[0].Name))
	}
	if len(result[0].Description) != 250 {
		t.Errorf("description should be truncated to 250, got %d", len(result[0].Description))
	}
}

func TestSanitizeSkills_FilterInvalid(t *testing.T) {
	t.Parallel()
	skills := []SkillSuggestion{
		{Name: "OK", Description: "Too short"},
		{Name: "Valid Name", Description: "short"},
		{Name: "Good Skill", Description: "This is a valid description for the skill"},
		{Name: "  ", Description: "This is a valid description"},
	}
	result := sanitizeSkills(skills)
	if len(result) != 1 {
		t.Fatalf("expected 1 valid skill, got %d", len(result))
	}
	if result[0].Name != "Good Skill" {
		t.Errorf("unexpected name: %s", result[0].Name)
	}
}

func TestSanitizeSkills_MaxCap(t *testing.T) {
	t.Parallel()
	skills := make([]SkillSuggestion, 20)
	for i := range skills {
		skills[i] = SkillSuggestion{
			Name:        strings.Repeat("S", 5) + string(rune('A'+i)),
			Description: "Valid description for this skill entry",
		}
	}
	result := sanitizeSkills(skills)
	if len(result) != maxSkillsPerResponse {
		t.Fatalf("expected %d skills (max), got %d", maxSkillsPerResponse, len(result))
	}
}

func TestValidateAndDefaultToolsets_FiltersDeprecated(t *testing.T) {
	t.Parallel()
	result := validateAndDefaultToolsets([]string{"artifact_management", "builtin_web_request_tools"}, builtinToolsets)
	for _, id := range result {
		if id == "artifact_management" {
			t.Error("deprecated toolset artifact_management should have been filtered")
		}
	}
	if !slices.Contains(result, "builtin_web_request_tools") {
		t.Error("expected builtin_web_request_tools to be preserved")
	}
	if !slices.Contains(result, "builtin_artifact_tools") {
		t.Error("expected default builtin_artifact_tools to be added")
	}
}

func TestEnsureUniqueName(t *testing.T) {
	t.Parallel()
	existing := []string{"Sales Agent", "Marketing Agent"}

	if name := ensureUniqueName("Data Agent", existing); name != "Data Agent" {
		t.Errorf("expected 'Data Agent', got %q", name)
	}

	name := ensureUniqueName("Sales Agent", existing)
	if name != "Sales Agent 2" {
		t.Errorf("expected 'Sales Agent 2', got %q", name)
	}
}

func TestAIAssistantService_NoGeneralConfigReturnsValidationError(t *testing.T) {
	t.Parallel()

	repo := store.NewModelConfigRepo(testOpenDB(t))
	svc := NewModelConfigService(repo, slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	aiSvc := NewAIAssistantService(svc, nil, nil, slog.Default(), 32000)

	_, _, release, err := aiSvc.resolveLLM(context.Background())
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("expected ValidationError when 'general' alias is unconfigured")
	}
	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(vErr.Message, "general") || !strings.Contains(vErr.Message, "Models UI") {
		t.Errorf("ValidationError message should name the alias and point operators at the Models UI; got %q", vErr.Message)
	}
}

func TestPreviewString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"abc", 5, "abc"},
		{"abcde", 5, "abcde"},
		{"abcdef", 5, "abcde…"},
		{strings.Repeat("x", 10), 3, "xxx…"},
	}
	for _, c := range cases {
		if got := previewString(c.in, c.n); got != c.want {
			t.Errorf("previewString(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func fifoResolver(client llm.Client) llmClientResolver {
	return func(_ context.Context) (llm.Client, string, func(), error) {
		return client, "", noopRelease, nil
	}
}

func newAIAssistantWithEmptyRepos(t *testing.T, resolver llmClientResolver) *AIAssistantService {
	t.Helper()
	db := testOpenDB(t)
	return newAIAssistantServiceWithResolver(resolver,
		store.NewAgentRepo(db),
		store.NewConnectorRepo(db),
		slog.Default())
}

func TestGenerateAgent_InvalidJSONFromLLM(t *testing.T) {
	t.Parallel()
	mockLLM := mockllm.NewFIFOClient([]*llm.ChatResponse{
		{Content: "not actually json", FinishReason: "stop"},
	})
	svc := newAIAssistantWithEmptyRepos(t, fifoResolver(mockLLM))

	_, err := svc.GenerateAgent(context.Background(),
		GenerateAgentRequest{Description: "a test agent that does things"}, "tester", "")
	if err == nil {
		t.Fatal("expected parse error from invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse LLM response") {
		t.Errorf("error should wrap with 'parse LLM response'; got %q", err.Error())
	}
}

func TestGenerateAgent_ProviderErrorBodyNotInMessage(t *testing.T) {
	t.Parallel()
	body := "<html><body>gateway error for request {\"prompt\":\"customer-record-123\"}</body></html>"
	mockLLM := mockllm.NewFIFOClientWithEntries([]mockllm.FIFOEntry{
		{Err: &llm.Error{Message: body, StatusCode: 502}},
	})
	svc := newAIAssistantWithEmptyRepos(t, fifoResolver(mockLLM))

	_, err := svc.GenerateAgent(context.Background(),
		GenerateAgentRequest{Description: "a test agent that does things"}, "tester", "")
	extErr, ok := errors.AsType[*ExternalServiceError](err)
	if !ok {
		t.Fatalf("expected ExternalServiceError, got %v", err)
	}
	if !strings.HasPrefix(extErr.Message, "agent generation failed: ") {
		t.Errorf("message prefix: %q", extErr.Message)
	}
	if strings.Contains(extErr.Message, "customer-record-123") {
		t.Errorf("message must not carry the provider body: %q", extErr.Message)
	}
}

func TestGenerateAgent_EmptyResultLogs(t *testing.T) {
	t.Parallel()
	mockLLM := mockllm.NewFIFOClient([]*llm.ChatResponse{
		{Content: `{}`, FinishReason: "stop"},
	})
	svc := newAIAssistantWithEmptyRepos(t, fifoResolver(mockLLM))

	res, err := svc.GenerateAgent(context.Background(),
		GenerateAgentRequest{Description: "a test agent that does things"}, "tester", "")
	if err != nil {
		t.Fatalf("unexpected error on empty JSON object: %v", err)
	}
	if res.Name != "" {
		t.Errorf("expected empty Name when LLM returned {}, got %q", res.Name)
	}
	if !slices.Equal(res.ModelProvider, []string{"general"}) {
		t.Errorf("expected modelProvider=[general] default, got %v", res.ModelProvider)
	}
}

func TestGenerateAgent_DefaultsModelProvider(t *testing.T) {
	t.Parallel()
	mockLLM := mockllm.NewFIFOClient([]*llm.ChatResponse{
		{Content: `{"name": "Sales Agent", "description": "Helps with sales questions", "systemPrompt": "You are a sales agent."}`, FinishReason: "stop"},
	})
	svc := newAIAssistantWithEmptyRepos(t, fifoResolver(mockLLM))

	res, err := svc.GenerateAgent(context.Background(),
		GenerateAgentRequest{Description: "a test agent that does things"}, "tester", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(res.ModelProvider, []string{"general"}) {
		t.Errorf("expected modelProvider=[general] default, got %v", res.ModelProvider)
	}
}

func TestChooseModelProvider(t *testing.T) {
	t.Parallel()
	configured := []configuredModelHint{
		{ID: "id-general", Alias: "general"},
		{ID: "id-coding", Alias: "coding"},
	}
	cases := []struct {
		name       string
		picked     []string
		configured []configuredModelHint
		want       []string
	}{
		{"translates alias to id", []string{"coding"}, configured, []string{"id-coding"}},
		{"keeps verbatim id", []string{"id-coding"}, configured, []string{"id-coding"}},
		{"drops hallucinated, falls back to general id", []string{"made_up"}, configured, []string{"id-general"}},
		{"empty pick falls back to general id", nil, configured, []string{"id-general"}},
		{"no general — falls back to first configured", nil, []configuredModelHint{{ID: "id-coding", Alias: "coding"}}, []string{"id-coding"}},
		{"empty configured emits general alias", []string{"made_up"}, nil, []string{"general"}},
		{"empty configured + empty pick still emits general alias", nil, nil, []string{"general"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := chooseModelProvider(c.picked, c.configured)
			if !slices.Equal(got, c.want) {
				t.Errorf("chooseModelProvider(%v, %v) = %v, want %v", c.picked, c.configured, got, c.want)
			}
		})
	}
}

func TestBuildAIAssistantSystemPrompt_RendersConfiguredModels(t *testing.T) {
	t.Parallel()
	models := []configuredModelHint{
		{Alias: "general", Description: "General-purpose chat"},
		{Alias: "coding"},
	}
	out := buildAIAssistantSystemPrompt(nil, nil, nil, models, builtinToolsets, 32000)
	if !strings.Contains(out, "**Available Models:**") {
		t.Fatal("expected Available Models section header")
	}
	if !strings.Contains(out, "`general`: General-purpose chat") {
		t.Errorf("expected description-bearing alias line; output:\n%s", out)
	}
	if !strings.Contains(out, "- `coding`\n") {
		t.Errorf("expected bare alias line for coding; output:\n%s", out)
	}
	if !strings.Contains(out, "modelProvider") {
		t.Error("expected schema/rule references to modelProvider")
	}
}

func TestBuildAIAssistantSystemPrompt_NoModelsFallsBackToGeneral(t *testing.T) {
	t.Parallel()
	out := buildAIAssistantSystemPrompt(nil, nil, nil, nil, builtinToolsets, 32000)
	if !strings.Contains(out, "`general` (default)") {
		t.Errorf("expected fallback general line when no models configured; output:\n%s", out)
	}
}

func TestGenerateAgent_WiredModelConfig_TranslatesAliasToID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db := testOpenDB(t)
	repo := store.NewModelConfigRepo(db)
	mcSvc := NewModelConfigService(repo, slog.Default())
	mcSvc.SetFeatureRegistry(decisionNodesOn())

	generalResp, err := mcSvc.Create(ctx, &CreateModelConfigRequest{
		Alias: "general", Provider: "openai", ModelName: "gpt-4", APIBase: "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create general: %v", err)
	}
	codingResp, err := mcSvc.Create(ctx, &CreateModelConfigRequest{
		Alias: "coding", Provider: "openai", ModelName: "gpt-4-coding", APIBase: "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create coding: %v", err)
	}
	description := "Placeholder for tests"
	err = repo.Create(ctx, &store.ModelConfiguration{
		Alias:           "vision",
		Provider:        "undefined",
		ModelName:       "undefined",
		ModelAuthType:   "none",
		ModelAuthConfig: `{"type":"none"}`,
		ModelParams:     "{}",
		Description:     &description,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	})
	if err != nil {
		t.Fatalf("seed placeholder: %v", err)
	}

	mockLLM := mockllm.NewFIFOClient([]*llm.ChatResponse{
		{
			Content:      `{"name": "Code Agent", "description": "Code assistant", "systemPrompt": "You are a code assistant.", "modelProvider": ["coding", "made_up_alias", "vision"]}`,
			FinishReason: "stop",
		},
	})

	svc := &AIAssistantService{
		modelConfigSvc: mcSvc,
		agentRepo:      store.NewAgentRepo(db),
		connectorRepo:  store.NewConnectorRepo(db),
		logger:         slog.Default(),
		resolveLLM:     fifoResolver(mockLLM),
	}

	res, err := svc.GenerateAgent(ctx,
		GenerateAgentRequest{Description: "a code assistant that helps with go"}, "tester", "")
	if err != nil {
		t.Fatalf("GenerateAgent: %v", err)
	}
	if !slices.Equal(res.ModelProvider, []string{codingResp.ID}) {
		t.Errorf("expected modelProvider=[%s] (coding UUID), got %v", codingResp.ID, res.ModelProvider)
	}
	if generalResp.ID == "" || generalResp.ID == "general" {
		t.Fatalf("seed sanity: general should have a UUID, got %q", generalResp.ID)
	}
}

func TestGenerateAgent_MaxTokensTruncation(t *testing.T) {
	t.Parallel()
	mockLLM := mockllm.NewFIFOClient([]*llm.ChatResponse{
		{Content: `{"name": "truncated`, FinishReason: "max_tokens"},
	})
	svc := newAIAssistantWithEmptyRepos(t, fifoResolver(mockLLM))

	_, err := svc.GenerateAgent(context.Background(),
		GenerateAgentRequest{Description: "a test agent that does things"}, "tester", "")
	if err == nil || !strings.Contains(err.Error(), "parse LLM response") {
		t.Errorf("expected parse error from truncated content; got %v", err)
	}
}

func TestGenerateAgent_DescriptionLengthValidation(t *testing.T) {
	t.Parallel()
	svc := newAIAssistantWithEmptyRepos(t, fifoResolver(nil))

	cases := []struct {
		name string
		desc string
	}{
		{"too short", "short"},
		{"too long", strings.Repeat("x", 1001)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.GenerateAgent(context.Background(),
				GenerateAgentRequest{Description: c.desc}, "tester", "")
			if err == nil {
				t.Fatal("expected validation error")
			}
			var vErr *ValidationError
			if !errors.As(err, &vErr) {
				t.Errorf("expected *ValidationError, got %T: %v", err, err)
			}
		})
	}
}
