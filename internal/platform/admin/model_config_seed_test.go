// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func setupSeedRepo(t *testing.T) *store.ModelConfigRepo {
	t.Helper()
	db := testOpenDB(t)
	return store.NewModelConfigRepo(db)
}

func TestSeedModelConfigs_FromEnv(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", "gpt-4-turbo")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test-key")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	general, err := repo.GetByAlias(ctx, "general")
	if err != nil {
		t.Fatalf("get general: %v", err)
	}
	if general == nil {
		t.Fatal("expected general to be seeded")
	}
	if general.ModelName != "gpt-4" {
		t.Errorf("general.ModelName = %q, want gpt-4", general.ModelName)
	}
	if general.Provider != "openai" {
		t.Errorf("general.Provider = %q, want openai", general.Provider)
	}
	if general.Description == nil || *general.Description != "Default general-purpose model" {
		t.Errorf("general.Description = %v, want %q", general.Description, "Default general-purpose model")
	}

	planning, err := repo.GetByAlias(ctx, "planning")
	if err != nil {
		t.Fatalf("get planning: %v", err)
	}
	if planning == nil {
		t.Fatal("expected planning to be seeded")
	}
	if planning.ModelName != "gpt-4-turbo" {
		t.Errorf("planning.ModelName = %q, want gpt-4-turbo", planning.ModelName)
	}
	if planning.Description == nil || *planning.Description != "Default planning model" {
		t.Errorf("planning.Description = %v, want %q", planning.Description, "Default planning model")
	}
}

func TestSeedModelConfigs_Idempotent(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	count1, _ := repo.Count(ctx)

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	count2, _ := repo.Count(ctx)

	if count1 != count2 {
		t.Errorf("seed not idempotent: count1=%d, count2=%d", count1, count2)
	}
}

func TestSeedModelConfigs_NoEnvVarsStillCreatesPlaceholderCards(t *testing.T) {
	t.Parallel()
	repo := setupSeedRepo(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	count, _ := repo.Count(ctx)
	if count != len(defaultModelAliases) {
		t.Errorf("expected %d placeholder rows, got %d", len(defaultModelAliases), count)
	}
	for _, alias := range defaultModelAliases {
		mc, err := repo.GetByAlias(ctx, alias)
		if err != nil {
			t.Fatalf("get %q: %v", alias, err)
		}
		if mc == nil {
			t.Fatalf("expected placeholder row for alias %q", alias)
		}
		if mc.Provider != placeholderValue {
			t.Errorf("%s.Provider = %q, want %q", alias, mc.Provider, placeholderValue)
		}
		if mc.ModelName != placeholderValue {
			t.Errorf("%s.ModelName = %q, want %q", alias, mc.ModelName, placeholderValue)
		}
		if mc.ModelAuthType != "none" {
			t.Errorf("%s.ModelAuthType = %q, want \"none\"", alias, mc.ModelAuthType)
		}
		if mc.CreatedBy != "system" {
			t.Errorf("%s.CreatedBy = %q, want \"system\"", alias, mc.CreatedBy)
		}
	}
}

func TestSeedModelConfigs_PlaceholdersIdempotent(t *testing.T) {
	t.Parallel()
	repo := setupSeedRepo(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	count1, _ := repo.Count(ctx)

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	count2, _ := repo.Count(ctx)

	if count1 != count2 {
		t.Errorf("placeholder seed not idempotent: count1=%d, count2=%d", count1, count2)
	}
}

func TestSeedModelConfigs_EnvVarsCoexistWithPlaceholders(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	general, err := repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("get general: %v (nil=%v)", err, general == nil)
	}
	if general.ModelName != "gpt-4" {
		t.Errorf("general.ModelName = %q, want gpt-4 (env-supplied, not placeholder)", general.ModelName)
	}

	planning, err := repo.GetByAlias(ctx, "planning")
	if err != nil || planning == nil {
		t.Fatalf("get planning: %v (nil=%v)", err, planning == nil)
	}
	if planning.ModelName != placeholderValue {
		t.Errorf("planning.ModelName = %q, want %q (placeholder)", planning.ModelName, placeholderValue)
	}
}

func TestSeedModelConfigs_MalformedEndpointSkipsAndLeavesPlaceholder(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "claude-sonnet-4-6")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https:lite-llm.mymass.net")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	general, err := repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("get general: %v (nil=%v)", err, general == nil)
	}
	if general.ModelName != placeholderValue {
		t.Errorf("general.ModelName = %q, want %q (placeholder, not the model name under a bad endpoint)", general.ModelName, placeholderValue)
	}
	if general.Provider != placeholderValue {
		t.Errorf("general.Provider = %q, want %q (not an inferProvider guess)", general.Provider, placeholderValue)
	}
	if general.APIBase != nil {
		t.Errorf("general.APIBase = %v, want nil (bad endpoint not persisted)", *general.APIBase)
	}
}

func TestSeedModelConfigs_WhitespaceEndpointBehavesAsUnset(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "claude-sonnet-4-6")
	t.Setenv("LLM_SERVICE_ENDPOINT", "   ")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	general, err := repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("get general: %v (nil=%v)", err, general == nil)
	}
	if general.APIBase != nil && strings.TrimSpace(*general.APIBase) == "" {
		t.Errorf("general.APIBase = %q, want nil or an actual value (whitespace must not persist)", *general.APIBase)
	}
}

func TestSeedModelConfigs_UpgradesPlaceholderWhenEnvSetLater(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	general, err := repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("first seed get general: %v (nil=%v)", err, general == nil)
	}
	if general.ModelName != placeholderValue {
		t.Fatalf("after first seed, general.ModelName = %q, want %q (placeholder)", general.ModelName, placeholderValue)
	}

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err = SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	general, err = repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("second seed get general: %v", err)
	}
	if general.ModelName != "gpt-4" {
		t.Errorf("general.ModelName = %q, want gpt-4 (placeholder should have been upgraded)", general.ModelName)
	}
	if general.Provider != "openai" {
		t.Errorf("general.Provider = %q, want openai (re-inferred from env on upgrade)", general.Provider)
	}
	if general.ModelAuthType != "apikey" {
		t.Errorf("general.ModelAuthType = %q, want apikey", general.ModelAuthType)
	}
	if general.Description == nil {
		t.Fatal("general.Description is nil after upgrade, want per-alias default")
	}
	if got, want := *general.Description, "Default general-purpose model"; got != want {
		t.Errorf("general.Description = %q after upgrade, want %q", got, want)
	}

	planning, err := repo.GetByAlias(ctx, "planning")
	if err != nil || planning == nil {
		t.Fatalf("get planning: %v", err)
	}
	if planning.ModelName != placeholderValue {
		t.Errorf("planning.ModelName = %q, want %q (no env var, should remain placeholder)", planning.ModelName, placeholderValue)
	}
}

func TestSeedModelConfigs_DoesNotOverwriteRealConfigOnReboot(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-original")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-5")
	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	general, err := repo.GetByAlias(ctx, "general")
	if err != nil || general == nil {
		t.Fatalf("get general: %v", err)
	}
	if general.ModelName != "gpt-4" {
		t.Errorf("general.ModelName = %q, want gpt-4 (real config should not be overwritten by env change)", general.ModelName)
	}
}

func TestSeedModelConfigs_LateEnvSeedsImageGenAndReportGen(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	t.Setenv("IMAGE_MODEL_NAME", "dall-e-3")
	t.Setenv("IMAGE_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_REPORT_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	imageGen, err := repo.GetByAlias(ctx, "image_gen")
	if err != nil || imageGen == nil {
		t.Fatalf("image_gen not seeded after late env: %v (nil=%v)", err, imageGen == nil)
	}
	if imageGen.ModelName != "dall-e-3" {
		t.Errorf("image_gen.ModelName = %q, want dall-e-3", imageGen.ModelName)
	}
	reportGen, err := repo.GetByAlias(ctx, "report_gen")
	if err != nil || reportGen == nil {
		t.Fatalf("report_gen not seeded after late env: %v", err)
	}
	if reportGen.ModelName != "gpt-4" {
		t.Errorf("report_gen.ModelName = %q, want gpt-4", reportGen.ModelName)
	}
}

func TestSeedModelConfigs_ResolvesNestedEnvRef(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "claude-sonnet-4")
	t.Setenv("LLM_REPORT_MODEL_NAME", "${LLM_SERVICE_GENERAL_MODEL_NAME}")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.anthropic.com")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reportGen, err := repo.GetByAlias(ctx, "report_gen")
	if err != nil || reportGen == nil {
		t.Fatalf("get report_gen: %v (nil=%v)", err, reportGen == nil)
	}
	if reportGen.ModelName != "claude-sonnet-4" {
		t.Errorf("report_gen.ModelName = %q, want claude-sonnet-4 (nested ${...} must resolve, not leak)", reportGen.ModelName)
	}
}

func TestSeedModelConfigs_SkipsUnresolvedRef(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_REPORT_MODEL_NAME", "${LLM SERVICE MODEL}")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reportGen, err := repo.GetByAlias(ctx, "report_gen")
	if err != nil {
		t.Fatalf("get report_gen: %v", err)
	}
	if reportGen != nil {
		t.Errorf("report_gen was seeded with %q; an unresolved ${...} reference must be skipped", reportGen.ModelName)
	}
}

func TestSeedModelConfigs_SkipsBlankModelName(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_REPORT_MODEL_NAME", " ")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reportGen, err := repo.GetByAlias(ctx, "report_gen")
	if err != nil {
		t.Fatalf("get report_gen: %v", err)
	}
	if reportGen != nil {
		t.Errorf("report_gen was seeded with %q; a blank model name must be skipped", reportGen.ModelName)
	}
}

func TestSeedModelConfigs_TrimsPaddedModelName(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_REPORT_MODEL_NAME", " gpt-4o ")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.openai.com/v1")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reportGen, err := repo.GetByAlias(ctx, "report_gen")
	if err != nil || reportGen == nil {
		t.Fatalf("get report_gen: %v (nil=%v)", err, reportGen == nil)
	}
	if reportGen.ModelName != "gpt-4o" {
		t.Errorf("report_gen.ModelName = %q, want %q (env padding must be trimmed)", reportGen.ModelName, "gpt-4o")
	}
}

func TestSeedModelConfigs_SelfHealsLeakedRefRow(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	leakedDesc := "Default report generation model"
	endpoint := "https://api.anthropic.com"
	if err := repo.Create(ctx, &store.ModelConfiguration{
		Alias:           "report_gen",
		Provider:        "custom",
		ModelName:       "${LLM_SERVICE_GENERAL_MODEL_NAME}",
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-old"}`,
		ModelParams:     "{}",
		APIBase:         &endpoint,
		Description:     &leakedDesc,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}); err != nil {
		t.Fatalf("pre-insert leaked row: %v", err)
	}

	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "claude-sonnet-4")
	t.Setenv("LLM_REPORT_MODEL_NAME", "${LLM_SERVICE_GENERAL_MODEL_NAME}")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.anthropic.com")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reportGen, err := repo.GetByAlias(ctx, "report_gen")
	if err != nil || reportGen == nil {
		t.Fatalf("get report_gen: %v (nil=%v)", err, reportGen == nil)
	}
	if reportGen.ModelName != "claude-sonnet-4" {
		t.Errorf("report_gen.ModelName = %q, want claude-sonnet-4 (leaked ${...} row must self-heal on reseed)", reportGen.ModelName)
	}
}

func TestSeedModelConfigs_SkipsPartialRef(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_REPORT_MODEL_NAME", "claude-${UNSET_REPORT_SUFFIX}")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://api.anthropic.com")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	reportGen, err := repo.GetByAlias(ctx, "report_gen")
	if err != nil {
		t.Fatalf("get report_gen: %v", err)
	}
	if reportGen != nil {
		t.Errorf("report_gen seeded with %q; a partial ${...} expansion must be skipped", reportGen.ModelName)
	}
}

func TestSeedModelConfigs_DisplayMasksUnhealableLeakedRow(t *testing.T) {
	svc, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	desc := "Default report generation model"
	endpoint := "https://api.anthropic.com"
	if err := repo.Create(ctx, &store.ModelConfiguration{
		Alias:           "report_gen",
		Provider:        "custom",
		ModelName:       "${LLM_SERVICE_GENERAL_MODEL_NAME}",
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-old"}`,
		ModelParams:     "{}",
		APIBase:         &endpoint,
		Description:     &desc,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}); err != nil {
		t.Fatalf("pre-insert leaked row: %v", err)
	}

	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, mc := range list {
		if mc.Alias != "report_gen" {
			continue
		}
		found = true
		if mc.ModelName != nil {
			t.Errorf("report_gen.ModelName = %q, want nil (unresolved ${...} must serialize as null)", *mc.ModelName)
		}
	}
	if !found {
		t.Fatal("report_gen row missing from List")
	}
}

func TestSeedModelConfigs_MalformedEndpointWARN_FiresOnceAcrossSharedAliases(t *testing.T) {
	repo := setupSeedRepo(t)
	ctx := context.Background()

	t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_REPORT_MODEL_NAME", "gpt-4")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https:lite-llm.mymass.net")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if err := SeedModelConfigs(ctx, repo, nil, logger); err != nil {
		t.Fatalf("seed: %v", err)
	}

	count := strings.Count(buf.String(), "model config endpoint env is malformed")
	if count != 1 {
		t.Errorf("expected exactly one malformed-endpoint WARN for LLM_SERVICE_ENDPOINT across 3 aliases, got %d; log output:\n%s", count, buf.String())
	}
}

func TestInferProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		endpoint string
		model    string
		want     string
	}{
		{"openai by host", "https://api.openai.com/v1", "gpt-4", "openai"},
		{"anthropic by host", "https://api.anthropic.com", "claude-3", "anthropic"},
		{"azure by host", "https://myresource.openai.azure.com/", "gpt-4", "azure_openai"},
		{"azure gov by host", "https://myresource.openai.azure.us/", "gpt-4", "azure_openai"},
		{"azure cn by host", "https://myresource.openai.azure.cn/", "gpt-4", "azure_openai"},
		{"azure cognitive services by host", "https://myresource.cognitiveservices.azure.com/", "gpt-4", "azure_openai"},
		{"azure cognitive services gov by host", "https://myresource.cognitiveservices.azure.us/", "gpt-4", "azure_openai"},
		{"azure cognitive services cn by host", "https://myresource.cognitiveservices.azure.cn/", "gpt-4", "azure_openai"},
		{"azure cognitive services microsoft by host", "https://myresource.api.cognitive.microsoft.com/", "gpt-4", "azure_openai"},
		{"microsoft foundry by host", "https://contoso.services.ai.azure.com/", "gpt-4", "azure_openai"},
		{"bedrock by host", "https://bedrock-runtime.us-east-1.amazonaws.com", "model", "bedrock"},
		{"vertex by host", "https://us-central1-aiplatform.googleapis.com/v1", "gemini", "vertex_ai"},
		{"google ai studio by host", "https://generativelanguage.googleapis.com/v1beta", "gemini", "google_ai_studio"},

		{"endpoint without known host → custom with /v1/ path", "https://custom.example.com/v1/", "my-model", "custom"},
		{"endpoint without known host → custom with /v1 path", "https://custom.example.com/v1", "my-model", "custom"},
		{"endpoint without known host → custom with no /v1", "https://custom.example.com/api", "my-model", "custom"},
		{"endpoint without known host → custom even for claude model", "https://my-proxy.example.com", "claude-3-opus", "custom"},
		{"endpoint without known host → custom even for gpt model", "https://my-proxy.example.com", "gpt-4-turbo", "custom"},
		{"endpoint without known host → custom even for gemini model", "https://my-proxy.example.com", "gemini-pro", "custom"},

		{"anthropic by model prefix (no endpoint)", "", "claude-3-opus", "anthropic"},
		{"openai by model prefix (no endpoint)", "", "gpt-4-turbo", "openai"},
		{"google by model prefix (no endpoint)", "", "gemini-pro", "google_ai_studio"},

		{"custom default", "", "some-model", "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferProvider(tt.endpoint, tt.model)
			if got != tt.want {
				t.Errorf("inferProvider(%q, %q) = %q, want %q", tt.endpoint, tt.model, got, tt.want)
			}
		})
	}
}
