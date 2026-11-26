// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func TestHandleConfig_ReturnsExpectedFields(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	requiredKeys := []string{
		"frontendUseAuthorization",
		"persistenceEnabled",
		"schedulerEnabled",
		"identityServiceType",
		"validationLimits",
		"toolConfigStatus",
		"ttsSettings",
		"sttSettings",
		"backgroundTasksConfig",
		"frontendPlatformServerUrl",
		"frontendPlatformServiceEnabled",
		"productAnalyticsEnabled",
		"sessionIdleTimeoutSeconds",
		"peopleSearchAvailable",
	}
	for _, key := range requiredKeys {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key: %s", key)
		}
	}

	if _, found := resp["frontendFeatureEnablement"]; found {
		t.Errorf("frontendFeatureEnablement should no longer be in the config response")
	}

	if resp["persistenceEnabled"] != false {
		t.Errorf("persistence_enabled = %v, want false", resp["persistenceEnabled"])
	}

	if resp["schedulerEnabled"] != false {
		t.Errorf("scheduler_enabled = %v, want false", resp["schedulerEnabled"])
	}

	if resp["activationGateEnabled"] != false {
		t.Errorf("activationGateEnabled = %v, want false", resp["activationGateEnabled"])
	}

	if resp["productAnalyticsEnabled"] != buildedition.ProductAnalytics {
		t.Errorf("productAnalyticsEnabled = %v, want %v", resp["productAnalyticsEnabled"], buildedition.ProductAnalytics)
	}

	validationLimits, ok := resp["validationLimits"].(map[string]any)
	if !ok {
		t.Fatal("validation_limits is not a map")
	}
	for _, key := range []string{"projectNameMax", "maxPerFileUploadSizeBytes"} {
		if _, found := validationLimits[key]; !found {
			t.Errorf("missing validation_limits key: %s", key)
		}
	}

	ttsSettings, ok := resp["ttsSettings"].(map[string]any)
	if !ok {
		t.Fatal("tts_settings is not a map")
	}
	for _, key := range []string{"textToSpeech", "engineTTS", "ttsProvider"} {
		if _, ok := ttsSettings[key]; !ok {
			t.Errorf("missing tts_settings key: %s", key)
		}
	}
}

func TestHandleConfig_PlatformServiceEnabled(t *testing.T) {
	tests := []struct {
		name        string
		mounted     bool
		url         string
		wantEnabled bool
		wantURL     string
	}{
		{"mounted, no url (co-located, same-origin)", true, "", true, ""},
		{"not mounted, url set (separate platform)", false, "https://platform.example.com", true, "https://platform.example.com"},
		{"mounted wins over url (co-located ignores stale override)", true, "https://platform.example.com", true, ""},
		{"neither", false, "", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter, core, _ := setupTestAdapter(t)
			core.SetPlatformMounted(tc.mounted)
			core.Config().PlatformServiceURL = tc.url

			cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/v1/config", nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}

			var resp map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}

			if got := resp["frontendPlatformServiceEnabled"]; got != tc.wantEnabled {
				t.Errorf("frontendPlatformServiceEnabled = %v, want %v", got, tc.wantEnabled)
			}
			if got := resp["frontendPlatformServerUrl"]; got != tc.wantURL {
				t.Errorf("frontendPlatformServerUrl = %v, want %v", got, tc.wantURL)
			}
		})
	}
}

func TestHandleConfig_FrontendLogoUrls(t *testing.T) {
	tests := []struct {
		name      string
		raw       map[string]any
		wantLarge any
		wantSmall any
	}{
		{
			name:      "no logos configured returns null for both",
			raw:       map[string]any{},
			wantLarge: nil,
			wantSmall: nil,
		},
		{
			name: "explicitly emptied logo stays an empty string",
			raw: map[string]any{
				"frontend_logo_url": "",
			},
			wantLarge: "",
			wantSmall: nil,
		},
		{
			name: "single frontend_logo_url passes through",
			raw: map[string]any{
				"frontend_logo_url": "https://cdn.example.com/acme-logo.png",
			},
			wantLarge: "https://cdn.example.com/acme-logo.png",
			wantSmall: nil,
		},
		{
			name: "both keys pass through independently (AC2 path)",
			raw: map[string]any{
				"frontend_logo_url":       "https://cdn.example.com/large.png",
				"frontend_small_logo_url": "https://cdn.example.com/small.png",
			},
			wantLarge: "https://cdn.example.com/large.png",
			wantSmall: "https://cdn.example.com/small.png",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter, core, _ := setupTestAdapter(t)
			cfg := core.Config()
			for k, v := range tc.raw {
				cfg.FrontendConfigRaw[k] = v
			}

			cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/v1/config", nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}

			var resp map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}

			if got, ok := resp["frontendLogoUrl"]; !ok {
				t.Errorf("missing frontendLogoUrl in response")
			} else if got != tc.wantLarge {
				t.Errorf("frontendLogoUrl = %v, want %v", got, tc.wantLarge)
			}

			if got, ok := resp["frontendSmallLogoUrl"]; !ok {
				t.Errorf("missing frontendSmallLogoUrl in response")
			} else if got != tc.wantSmall {
				t.Errorf("frontendSmallLogoUrl = %v, want %v", got, tc.wantSmall)
			}
		})
	}
}

func TestHandleConfig_FailsOpenWhenSettingsPullFails(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)
	cfg := core.Config()
	cfg.FrontendConfigRaw["frontend_app_name"] = "YAML Name"
	cfg.FrontendConfigRaw["frontend_welcome_message"] = "YAML welcome"

	core.SetWebuiSettingsRegistry(webuisettings.NewRegistry(func(context.Context) (*webuisettings.Settings, error) {
		return nil, errors.New("platform unreachable")
	}, nil))

	cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 despite the failed settings pull, got %d", rec.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["frontendAppName"] != "YAML Name" {
		t.Errorf("frontendAppName = %v, want the YAML value (fell through on pull failure)", resp["frontendAppName"])
	}
	if resp["frontendWelcomeMessage"] != "YAML welcome" {
		t.Errorf("frontendWelcomeMessage = %v, want the YAML value", resp["frontendWelcomeMessage"])
	}
}

func TestHandleConfig_FrontendUseAuthorizationNeverInfluencedByDBLayer(t *testing.T) {
	for _, useAuth := range []bool{true, false} {
		t.Run(fmt.Sprintf("useAuthorization=%v", useAuth), func(t *testing.T) {
			adapter, core, _ := setupTestAdapter(t)
			cfg := core.Config()
			cfg.FrontendUseAuthorization = useAuth

			appName := "Overridden Name"
			reg := webuisettings.NewRegistry(func(context.Context) (*webuisettings.Settings, error) {
				return &webuisettings.Settings{AppName: &appName}, nil
			}, nil)
			if _, err := reg.Get(context.Background()); err != nil {
				t.Fatalf("prime registry: %v", err)
			}
			core.SetWebuiSettingsRegistry(reg)

			cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/v1/config", nil)
			mux.ServeHTTP(rec, req)

			var resp map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}

			if resp["frontendUseAuthorization"] != useAuth {
				t.Errorf("frontendUseAuthorization = %v, want %v (config value, unaffected by the DB overrides layer)", resp["frontendUseAuthorization"], useAuth)
			}
			if resp["frontendAppName"] != appName {
				t.Errorf("frontendAppName = %v, want %q (sanity check that the registry override is actually live)", resp["frontendAppName"], appName)
			}
		})
	}
}

func TestHandleConfig_NativeOIDC_AutoComputesLoginURL(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	cfg := core.Config()
	cfg.ExternalAuthServiceURL = ""
	cfg.FrontendAuthLoginURL = ""

	cfgHandler := &configHandler{
		core:             adapter.core,
		authProviderType: "enterprise",
		logger:           adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if got := resp["externalAuthServiceUrl"]; got != "" {
		t.Errorf("external_auth_service_url = %v, want empty string", got)
	}
	if got := resp["frontendAuthLoginUrl"]; got != "/api/v1/auth/login" {
		t.Errorf("frontend_auth_login_url = %v, want /api/v1/auth/login", got)
	}
}

func TestHandleConfig_ExplicitLoginURL_NotOverridden(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	cfg := core.Config()
	cfg.ExternalAuthServiceURL = ""
	cfg.FrontendAuthLoginURL = "https://my.custom/login"

	cfgHandler := &configHandler{
		core:             adapter.core,
		authProviderType: "enterprise",
		logger:           adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if got := resp["frontendAuthLoginUrl"]; got != "https://my.custom/login" {
		t.Errorf("explicit frontend_auth_login_url should win: got %v", got)
	}
}

func TestHandleConfig_NonEnterpriseProvider_NoAutoCompute(t *testing.T) {
	adapter, core, _ := setupTestAdapter(t)

	cfg := core.Config()
	cfg.ExternalAuthServiceURL = ""
	cfg.FrontendAuthLoginURL = ""

	cfgHandler := &configHandler{
		core:             adapter.core,
		authProviderType: "none",
		logger:           adapter.logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if got := resp["frontendAuthLoginUrl"]; got != "" {
		t.Errorf("non-enterprise provider should not auto-compute login URL: got %v", got)
	}
}

func TestHandleVersion_ReturnsProductInfo(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	originalVersion := samversion.Version
	originalSTRVersion := samversion.STRVersion
	samversion.Version = "test-1.2.3"
	samversion.STRVersion = "test-0.41.0"
	t.Cleanup(func() {
		samversion.Version = originalVersion
		samversion.STRVersion = originalSTRVersion
	})

	cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/version", cfgHandler.handleVersion)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/version", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp VersionResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	wantVersions := map[string]string{
		"sam-gateway-go": "test-1.2.3",
		"sam-str-go":     "test-0.41.0",
	}
	seen := map[string]bool{}
	for _, p := range resp.Products {
		want, ok := wantVersions[p.ID]
		if !ok {
			t.Errorf("unexpected product id: %q", p.ID)
			continue
		}
		seen[p.ID] = true
		if p.Name == "" {
			t.Errorf("product %q has empty name", p.ID)
		}
		if p.Version != want {
			t.Errorf("product %q version = %q, want %q", p.ID, p.Version, want)
		}
	}
	for id := range wantVersions {
		if !seen[id] {
			t.Errorf("missing product: %q", id)
		}
	}
}

func TestVersionResponse_OptionalFloorFields(t *testing.T) {
	withFloor, err := json.Marshal(VersionResponse{
		Products:         []ProductInfo{{ID: "sam-gateway-go", Version: "main-v2.65.8"}},
		MinClientVersion: "main-v2.0.0",
		APIVersion:       "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"minClientVersion":"main-v2.0.0"`, `"apiVersion":"v1"`} {
		if !strings.Contains(string(withFloor), want) {
			t.Errorf("marshaled %s, want it to contain %s", withFloor, want)
		}
	}
	var rt VersionResponse
	if err = json.Unmarshal(withFloor, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.MinClientVersion != "main-v2.0.0" || rt.APIVersion != "v1" {
		t.Errorf("round-trip lost floor fields: %+v", rt)
	}

	noFloor, err := json.Marshal(VersionResponse{
		Products: []ProductInfo{{ID: "sam-gateway-go", Version: "main-v2.65.8"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"minClientVersion", "apiVersion"} {
		if strings.Contains(string(noFloor), omitted) {
			t.Errorf("marshaled %s, want %q omitted when unset", noFloor, omitted)
		}
	}
}

func TestComputeSpeechSettings_AzureSharedCredentials(t *testing.T) {
	h := &configHandler{featureRegistry: voiceEnabledRegistry(t), logger: slog.Default()}
	cfg := &entrypoint.GatewayConfig{
		SpeechRaw: map[string]any{
			"stt": map[string]any{
				"provider": "azure",
				"azure":    map[string]any{"api_key": "azkey", "region": "eastus"},
			},
			"tts": map[string]any{
				"provider": "azure",
				"azure":    map[string]any{"default_voice": "en-US-JennyNeural"},
			},
		},
	}

	ttsSettings, sttSettings, toolStatus := h.computeSpeechSettings(cfg)
	if got := ttsSettings["textToSpeech"]; got != true {
		t.Errorf("textToSpeech = %v, want true (Azure creds present in stt block)", got)
	}
	if got := ttsSettings["engineTTS"]; got != "external" {
		t.Errorf("engineTTS = %v, want external", got)
	}
	if got := ttsSettings["ttsProvider"]; got != "azure" {
		t.Errorf("ttsProvider = %v, want azure", got)
	}
	if got := sttSettings["speechToText"]; got != true {
		t.Errorf("sttSettings.speechToText = %v, want true (Azure STT creds present)", got)
	}
	if got := toolStatus["textToSpeech"]; got != true {
		t.Errorf("tool_config_status.text_to_speech = %v, want true", got)
	}
	if got := toolStatus["speechToText"]; got != true {
		t.Errorf("tool_config_status.speech_to_text = %v, want true", got)
	}
}

func TestCfgMap(t *testing.T) {
	m := map[string]any{"key": map[string]any{"nested": true}}
	result := cfgMap(m, "key")
	if result == nil {
		t.Fatal("expected non-nil map")
	}
	if result["nested"] != true {
		t.Error("expected nested=true")
	}

	if cfgMap(nil, "key") != nil {
		t.Error("expected nil for nil input")
	}
	if cfgMap(m, "missing") != nil {
		t.Error("expected nil for missing key")
	}
}

func TestCfgBool(t *testing.T) {
	m := map[string]any{"flag": true}
	if !cfgBool(m, "flag") {
		t.Error("expected true")
	}
	if cfgBool(m, "missing") {
		t.Error("expected false for missing key")
	}
	if cfgBool(nil, "flag") {
		t.Error("expected false for nil map")
	}
}

func TestCfgString(t *testing.T) {
	m := map[string]any{"name": "hello"}
	if cfgString(m, "name") != "hello" {
		t.Error("expected hello")
	}
	if cfgString(m, "missing") != "" {
		t.Error("expected empty for missing key")
	}
	if cfgString(nil, "name") != "" {
		t.Error("expected empty for nil map")
	}
}

func TestCfgStringDefault(t *testing.T) {
	m := map[string]any{"name": "hello"}
	if cfgStringDefault(m, "name", "default") != "hello" {
		t.Error("expected hello")
	}
	if cfgStringDefault(m, "missing", "default") != "default" {
		t.Error("expected default for missing key")
	}
	if cfgStringDefault(nil, "name", "default") != "default" {
		t.Error("expected default for nil map")
	}
	m2 := map[string]any{"name": ""}
	if cfgStringDefault(m2, "name", "fallback") != "fallback" {
		t.Error("expected fallback for empty string")
	}
}

func TestCfgIntDefault(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]any
		key      string
		def      int
		expected int
	}{
		{"int value", map[string]any{"x": 42}, "x", 0, 42},
		{"float64 value", map[string]any{"x": float64(3.0)}, "x", 0, 3},
		{"int64 value", map[string]any{"x": int64(99)}, "x", 0, 99},
		{"missing key", map[string]any{}, "x", 10, 10},
		{"nil map", nil, "x", 10, 10},
		{"string value", map[string]any{"x": "not-a-number"}, "x", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfgIntDefault(tt.m, tt.key, tt.def); got != tt.expected {
				t.Errorf("got %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestHasLLMModel(t *testing.T) {
	tests := []struct {
		name     string
		modelRaw any
		expected bool
	}{
		{"nil", nil, false},
		{"string model", "gpt-4", true},
		{"empty string", "", false},
		{"map with model", map[string]any{"model": "gpt-4"}, true},
		{"map with empty model", map[string]any{"model": ""}, false},
		{"map without model", map[string]any{"other": "value"}, false},
		{"unsupported type", 42, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &entrypoint.GatewayConfig{ModelRaw: tt.modelRaw}
			if got := hasLLMModel(cfg); got != tt.expected {
				t.Errorf("got %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHandleFeatureFlags_NilRegistry(t *testing.T) {
	h := &configHandler{featureRegistry: nil, logger: slog.Default()}
	rec := httptest.NewRecorder()
	h.handleFeatureFlags(rec, httptest.NewRequest("GET", "/api/v1/config/features", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resultResp struct {
		Data []any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resultResp); err != nil {
		t.Fatal(err)
	}
	result := resultResp.Data
	if len(result) != 0 {
		t.Errorf("expected empty array, got %d items", len(result))
	}
}

func TestHandleFeatureFlags_ReturnsRegistryFlags(t *testing.T) {
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatal(err)
	}

	h := &configHandler{featureRegistry: reg, logger: slog.Default()}
	rec := httptest.NewRecorder()
	h.handleFeatureFlags(rec, httptest.NewRequest("GET", "/api/v1/config/features", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var flagsResp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&flagsResp); err != nil {
		t.Fatal(err)
	}
	flags := flagsResp.Data

	wantKeys := map[string]bool{}
	for _, d := range reg.All() {
		wantKeys[d.Key] = true
	}
	gotKeys := map[string]bool{}
	for _, f := range flags {
		key, _ := f["key"].(string)
		gotKeys[key] = true
	}
	for key := range wantKeys {
		if !gotKeys[key] {
			t.Errorf("registry flag %q is missing from the /config/features response", key)
		}
	}
	for key := range gotKeys {
		if !wantKeys[key] {
			t.Errorf("response contains %q, which is not in the registry", key)
		}
	}

	for i := 1; i < len(flags); i++ {
		prev, _ := flags[i-1]["key"].(string)
		cur, _ := flags[i]["key"].(string)
		if prev > cur {
			t.Errorf("flags not in sorted key order: %q precedes %q", prev, cur)
		}
	}

	for _, f := range flags {
		for _, field := range []string{"key", "name", "releasePhase", "resolved", "hasEnvOverride", "registryDefault", "description"} {
			if _, ok := f[field]; !ok {
				t.Errorf("flag %s missing field %s", f["key"], field)
			}
		}
	}

	for _, f := range flags {
		if f["key"] == "builder" {
			if f["resolved"] != true {
				t.Error("builder should be resolved=true")
			}
			if f["registryDefault"] != true {
				t.Error("builder should have registry_default=true")
			}
			if f["hasEnvOverride"] != false {
				t.Error("builder should have has_env_override=false")
			}
		}
	}
}

func TestHandleFeatureFlags_NotifyServiceFollowsTheMount(t *testing.T) {
	t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "")
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyNotifyService, Name: "Notification Service", Default: true})

	resolved := func(core *entrypoint.GatewayCore) any {
		t.Helper()
		h := &configHandler{core: core, featureRegistry: reg, logger: slog.Default()}
		rec := httptest.NewRecorder()
		h.handleFeatureFlags(rec, httptest.NewRequest("GET", "/api/v1/config/features", nil))
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Data) != 1 {
			t.Fatalf("expected 1 flag, got %d", len(resp.Data))
		}
		return resp.Data[0]["resolved"]
	}

	core := &entrypoint.GatewayCore{}
	if got := resolved(core); got != false {
		t.Errorf("notify not mounted: resolved = %v, want false", got)
	}
	core.SetNotifyMounted(true)
	if got := resolved(core); got != true {
		t.Errorf("notify mounted: resolved = %v, want true", got)
	}
	t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "false")
	if got := resolved(core); got != false {
		t.Errorf("notify mounted but flag turned off: resolved = %v, want false", got)
	}
}

func TestHandleFeatureFlags_EnvOverrideRunsSequentially(t *testing.T) {
	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          "test_flag",
		Name:         "Test Flag",
		ReleasePhase: features.PhaseBeta,
		Default:      false,
		Description:  "A test flag",
	})

	t.Setenv("SAM_FEATURE_TEST_FLAG", "true")

	h := &configHandler{featureRegistry: reg, logger: slog.Default()}
	rec := httptest.NewRecorder()
	h.handleFeatureFlags(rec, httptest.NewRequest("GET", "/api/v1/config/features", nil))

	var flagsResp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&flagsResp); err != nil {
		t.Fatal(err)
	}
	flags := flagsResp.Data
	if len(flags) != 1 {
		t.Fatalf("expected 1 flag, got %d", len(flags))
	}

	f := flags[0]
	if f["resolved"] != true {
		t.Error("env override should resolve to true")
	}
	if f["hasEnvOverride"] != true {
		t.Error("env override should be detected")
	}
	if f["registryDefault"] != false {
		t.Error("registry_default should still be false (env only affects resolved)")
	}
}

func TestHandleFeatureFlags_EnterpriseOverride(t *testing.T) {
	const subjectKey = "override_test_subject"

	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          subjectKey,
		Name:         "Community Subject",
		ReleasePhase: features.PhaseGeneralAvailability,
		Description:  "Community description",
	})
	reg.Register(features.Definition{
		Key:          subjectKey,
		Name:         "Enterprise Subject",
		ReleasePhase: features.PhaseEarlyAccess,
		Description:  "Enterprise description",
	})

	h := &configHandler{featureRegistry: reg, logger: slog.Default()}
	rec := httptest.NewRecorder()
	h.handleFeatureFlags(rec, httptest.NewRequest("GET", "/api/v1/config/features", nil))

	var flagsResp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&flagsResp); err != nil {
		t.Fatal(err)
	}
	flags := flagsResp.Data

	for _, f := range flags {
		if f["key"] == subjectKey {
			if f["releasePhase"] != "early_access" {
				t.Errorf("enterprise override should win: got release_phase=%s", f["releasePhase"])
			}
			if f["description"] != "Enterprise description" {
				t.Errorf("enterprise override should win: got description=%s", f["description"])
			}
			return
		}
	}
	t.Errorf("%s not found in response", subjectKey)
}

func TestHandleConfig_DataRetentionWindow(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	adapter.core.SetRetentionWindowForTest(true, 30, 45)

	cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	retention, ok := resp["dataRetention"].(map[string]any)
	if !ok {
		t.Fatalf("dataRetention missing or not an object: %#v", resp["dataRetention"])
	}
	if got := retention["taskDays"]; got != float64(30) {
		t.Errorf("taskDays = %v, want 30", got)
	}
	if got := retention["feedbackDays"]; got != float64(45) {
		t.Errorf("feedbackDays = %v, want 45", got)
	}
}

func TestHandleConfig_DataRetentionOmittedWhenCleanupDisabled(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	adapter.core.SetRetentionWindowForTest(false, 30, 45)

	cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if _, present := resp["dataRetention"]; present {
		t.Errorf("dataRetention should be absent when cleanup is disabled, got %#v", resp["dataRetention"])
	}
}

func TestHandleConfig_SessionIdleTimeoutSeconds(t *testing.T) {
	tests := []struct {
		name             string
		authProviderType string
		samAccessToken   map[string]any
		want             float64
	}{
		{"enterprise default is 4h", "enterprise", nil, 14400},
		{"enterprise explicit value", "enterprise", map[string]any{"idle_timeout_seconds": 1800}, 1800},
		{"enterprise explicit 0 is off", "enterprise", map[string]any{"idle_timeout_seconds": 0}, 0},
		{"no login means no idle timeout", "none", map[string]any{"idle_timeout_seconds": 1800}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, core, _ := setupTestAdapter(t)
			if tt.samAccessToken != nil {
				core.RawConfig()["sam_access_token"] = tt.samAccessToken
			}
			cfgHandler := &configHandler{core: adapter.core, authProviderType: tt.authProviderType, logger: adapter.logger}

			rec := httptest.NewRecorder()
			cfgHandler.handleConfig(rec, httptest.NewRequest("GET", "/api/v1/config", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			var resp map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}
			if got := resp["sessionIdleTimeoutSeconds"]; got != tt.want {
				t.Errorf("sessionIdleTimeoutSeconds = %v, want %v", got, tt.want)
			}
		})
	}
}

type probeIdentitySvc struct {
	ok  bool
	err error
}

func (p *probeIdentitySvc) SearchUsers(_ context.Context, _ string, _ int) ([]*identity.Profile, error) {
	return []*identity.Profile{}, nil
}

func (p *probeIdentitySvc) SearchConfigured(_ context.Context) (bool, error) {
	return p.ok, p.err
}

func fetchPeopleSearchAvailable(t *testing.T, idSvc auth.IdentityService) any {
	t.Helper()
	adapter, _, _ := setupTestAdapter(t)
	if idSvc != nil {
		adapter.core.SetIdentityService(idSvc)
	}
	cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp["peopleSearchAvailable"]
}

func TestHandleConfig_PeopleSearchAvailable_True(t *testing.T) {
	if got := fetchPeopleSearchAvailable(t, &probeIdentitySvc{ok: true}); got != true {
		t.Errorf("peopleSearchAvailable = %v, want true", got)
	}
}

func TestHandleConfig_PeopleSearchAvailable_False(t *testing.T) {
	if got := fetchPeopleSearchAvailable(t, &probeIdentitySvc{ok: false}); got != false {
		t.Errorf("peopleSearchAvailable = %v, want false", got)
	}
}

func TestHandleConfig_PeopleSearchAvailable_ProbeError(t *testing.T) {
	if got := fetchPeopleSearchAvailable(t, &probeIdentitySvc{ok: true, err: errors.New("db down")}); got != false {
		t.Errorf("peopleSearchAvailable = %v, want false", got)
	}
}

func TestHandleConfig_PeopleSearchAvailable_NoProbe(t *testing.T) {
	if got := fetchPeopleSearchAvailable(t, &fakeIdentitySvc{}); got != false {
		t.Errorf("peopleSearchAvailable = %v, want false", got)
	}
}

func TestHandleConfig_PeopleSearchAvailable_NoIdentityService(t *testing.T) {
	if got := fetchPeopleSearchAvailable(t, nil); got != false {
		t.Errorf("peopleSearchAvailable = %v, want false", got)
	}
}

type configuredIdentitySource struct{}

func (configuredIdentitySource) Get(_ context.Context) (string, string, bool, error) {
	return "ts-1", "email", true, nil
}

type searchCapableEnricher struct{}

func (searchCapableEnricher) EnrichAuthenticatedUser(_ context.Context, _ string) (*identity.Profile, error) {
	return nil, nil
}
func (searchCapableEnricher) SearchUsers(_ context.Context, _ string, _ int) ([]*identity.Profile, error) {
	return nil, nil
}
func (searchCapableEnricher) GetUserProfile(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}
func (searchCapableEnricher) GetTimeOff(_ context.Context, _, _, _ string) ([]identity.TimeOffEntry, error) {
	return nil, nil
}
func (searchCapableEnricher) GetUserProfilePicture(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}
func (searchCapableEnricher) HasTool(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

func TestHandleConfig_PeopleSearchAvailable_ThroughLazyService(t *testing.T) {
	lazy := identity.NewLazyService()
	if got := fetchPeopleSearchAvailable(t, lazy); got != false {
		t.Fatalf("unbound lazy service: peopleSearchAvailable = %v, want false", got)
	}

	lazy.SetDelegate(identity.New(configuredIdentitySource{}, nil, searchCapableEnricher{}, nil))
	if got := fetchPeopleSearchAvailable(t, lazy); got != true {
		t.Errorf("bound lazy service: peopleSearchAvailable = %v, want true", got)
	}
}

func TestHandleConfig_GoogleChatEndpointBase(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"unset", "", ""},
		{"trailing slash trimmed", "https://sam.example.com/", "https://sam.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tc.env)
			adapter, core, _ := setupTestAdapter(t)
			core.Config().FrontendServerURL = "http://localhost:5174"

			cfgHandler := &configHandler{core: adapter.core, logger: adapter.logger}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/config", cfgHandler.handleConfig)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			var resp map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}
			if got := resp["googleChatEndpointBase"]; got != tc.want {
				t.Errorf("googleChatEndpointBase = %v, want %q", got, tc.want)
			}
		})
	}
}
