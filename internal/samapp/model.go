// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"encoding/pem"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

var apiKeyCounter atomic.Int64

const readFileErrFmt = "read %s (%s): %w"

var knownProviders = map[string]string{
	"openai":      "openai",
	"anthropic":   "anthropic",
	"google":      "gemini",
	"vertex":      "vertex",
	"vertex_ai":   "vertex",
	"gemini":      "gemini",
	"bedrock":     "bedrock",
	"azure":       "azure",
	"cohere":      "cohere",
	"mistral":     "mistral",
	"groq":        "groq",
	"perplexity":  "perplexity",
	"cerebras":    "cerebras",
	"openrouter":  "openrouter",
	"huggingface": "huggingface",
	"ollama":      "ollama",
	"nebius":      "nebius",
	"xai":         "xai",
	"replicate":   "replicate",
}

var knownModelConfigKeys = map[string]bool{
	"model":                              true,
	"api_base":                           true,
	"api_key":                            true,
	"request_timeout_seconds":            true,
	"stream_idle_timeout_seconds":        true,
	"num_retries":                        true,
	"llm_retry_budget_seconds":           true,
	"llm_concurrency":                    true,
	"llm_buffer_size":                    true,
	"parallel_tool_calls":                true,
	"max_tokens":                         true,
	"temperature":                        true,
	"extra_headers":                      true,
	"oauth_token_url":                    true,
	"oauth_client_id":                    true,
	"oauth_client_secret":                true,
	"oauth_scope":                        true,
	"oauth_ca_cert":                      true,
	"oauth_token_refresh_buffer_seconds": true,
	"oauth_max_retries":                  true,
	"oauth_allow_insecure_token_url":     true,
	"oauth_skip_tls_verify":              true,
	"api_skip_tls_verify":                true,
	"api_ca_cert":                        true,
	"api_key_file":                       true,
	"oauth_token_url_file":               true,
	"oauth_client_id_file":               true,
	"oauth_client_secret_file":           true,
	llm.AzureWorkloadIdentityKey:         true,
	llm.GrantIDKey:                       true,
}

var nonCompletionKeys = map[string]bool{
	"cache_strategy": true,
	"thinking":       true,
	"type":           true,
	ModelConfigIDKey: true,
	ModelParamsKey:   true,
}

const ModelConfigIDKey = "model_config_id"

func ModelConfigID(raw map[string]any) string {
	id, _ := raw[ModelConfigIDKey].(string)
	return id
}

func ForPythonAgent(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	out := maps.Clone(raw)
	delete(out, ModelConfigIDKey)
	delete(out, ModelParamsKey)
	if base, ok := pythonAPIBase(out); ok {
		out["api_base"] = base
	}
	return out
}

func pythonAPIBase(raw map[string]any) (string, bool) {
	model, _ := raw["model"].(string)
	base, _ := raw["api_base"].(string)
	if !onLiteLLMOpenAIRoute(model) || strings.Contains(base, "?") {
		return "", false
	}
	return llm.ChatCompletionsRoot(base)
}

func onLiteLLMOpenAIRoute(model string) bool {
	if strings.HasPrefix(strings.ToLower(model), "openai/") {
		return true
	}
	provider, _ := ParseModelProvider(model)
	return provider == "openai" && !strings.Contains(model, "/")
}

func IsClientConfigKey(k string) bool {
	return knownModelConfigKeys[k] || nonCompletionKeys[k] || strings.HasPrefix(k, "oauth_")
}

func ParseModelConfig(raw any, logger *slog.Logger) (*llm.BifrostConfig, error) {
	if logger == nil {
		logger = slog.Default()
	}
	switch v := raw.(type) {
	case map[string]any:
		return parseMapModelConfig(v, logger)
	case string:
		provider, model := ParseModelProvider(v)
		return &llm.BifrostConfig{
			Model:    model,
			Provider: provider,
		}, nil
	default:
		return nil, fmt.Errorf("unexpected model config type %T", raw)
	}
}

func parseMapModelConfig(v map[string]any, logger *slog.Logger) (*llm.BifrostConfig, error) {
	if err := resolveKnownFileKeys(v); err != nil {
		return nil, err
	}

	cfg := &llm.BifrostConfig{}
	if model, ok := v["model"].(string); ok {
		cfg.Provider, cfg.Model = ParseModelProvider(model)
	}
	if base, ok := v["api_base"].(string); ok {
		cfg.BaseURL = strings.TrimSpace(base)
	}
	if t, ok := v["request_timeout_seconds"]; ok {
		cfg.RequestTimeoutSeconds = intOrFloat(t)
	}
	if t, ok := v["stream_idle_timeout_seconds"]; ok {
		cfg.StreamIdleTimeoutSeconds = intOrFloat(t)
	}
	if r, ok := v["num_retries"]; ok {
		cfg.MaxRetries = new(intOrFloat(r))
	}
	if b, ok := v["llm_retry_budget_seconds"]; ok {
		cfg.RetryBudgetSeconds = new(intOrFloat(b))
	}
	if c, ok := v["llm_concurrency"]; ok {
		cfg.Concurrency = intOrFloat(c)
	}
	if b, ok := v["llm_buffer_size"]; ok {
		cfg.BufferSize = intOrFloat(b)
	}
	if headers, ok := v["extra_headers"].(map[string]any); ok {
		cfg.ExtraHeaders = stringifyHeaders(headers)
	}
	if err := parseAPITLSConfig(cfg, v, logger); err != nil {
		return nil, err
	}

	oauthCfg, err := parseOAuth2Config(v, logger)
	if err != nil {
		return nil, err
	}
	cfg.OAuth2 = oauthCfg

	if cfg.AzureWorkloadIdentity, err = parseAzureWorkloadIdentity(v, cfg.Provider); err != nil {
		return nil, err
	}
	if cfg.GrantID, err = parseGrantID(v); err != nil {
		return nil, err
	}

	providerCfg, err := collectProviderConfig(v)
	if err != nil {
		return nil, err
	}
	if endpoint, ok := providerCfg["endpoint"].(string); ok {
		providerCfg["endpoint"] = strings.TrimSpace(endpoint)
	}
	if cfg.AzureWorkloadIdentity != nil {
		if err := checkAzureWorkloadIdentityConfig(v, providerCfg); err != nil {
			return nil, err
		}
	}

	if cfg.Provider == "azure" && oauthCfg != nil {
		azureSP, ok := azureServicePrincipalFromOAuth(oauthCfg, logger)
		if ok {
			var overriddenKeys []string
			for k := range azureSP {
				if _, exists := providerCfg[k]; exists {
					overriddenKeys = append(overriddenKeys, k)
				}
			}
			if len(overriddenKeys) > 0 {
				sort.Strings(overriddenKeys)
				logger.Warn("azure oauth2: operator-supplied provider config keys overridden by oauth2-derived values", "overriddenKeys", overriddenKeys)
			}
			var droppedKeys []string
			for _, k := range []string{
				"oauth_ca_cert",
				"oauth_skip_tls_verify",
				"oauth_token_refresh_buffer_seconds",
				"oauth_max_retries",
				"oauth_allow_insecure_token_url",
			} {
				if _, exists := v[k]; exists {
					droppedKeys = append(droppedKeys, k)
				}
			}
			if len(droppedKeys) > 0 {
				logger.Warn("azure oauth2: oauth_* fields dropped, not supported for Azure", "droppedKeys", droppedKeys)
			}
			for k, v := range azureSP {
				providerCfg[k] = v
			}
			cfg.OAuth2 = nil
		}
	}

	if len(providerCfg) > 0 {
		cfg.ProviderConfig = providerCfg
	}
	cfg.RequestDefaults = requestDefaults(v, cfg.Provider, logger)
	if err := setAPIKeyEnv(cfg, v); err != nil {
		return nil, err
	}
	return cfg, nil
}

func resolveKnownFileKeys(v map[string]any) error {
	for _, baseKey := range []string{"api_key", "oauth_token_url", "oauth_client_id", "oauth_client_secret"} {
		fileKey := baseKey + "_file"
		path, ok := v[fileKey].(string)
		if !ok || path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf(readFileErrFmt, fileKey, path, err)
		}
		v[baseKey] = strings.TrimSpace(string(data))
		delete(v, fileKey)
	}
	return nil
}

func setAPIKeyEnv(cfg *llm.BifrostConfig, v map[string]any) error {
	key, ok := v["api_key"].(string)
	if !ok || key == "" {
		return nil
	}
	envName := fmt.Sprintf("SAM_AWE_LLM_API_KEY_%d", apiKeyCounter.Add(1))
	if err := os.Setenv(envName, key); err != nil {
		return fmt.Errorf("set API key env var: %w", err)
	}
	cfg.APIKeyEnvVar = envName
	return nil
}

func intOrFloat(raw any) int {
	switch n := raw.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

func stringifyHeaders(headers map[string]any) map[string]string {
	out := make(map[string]string, len(headers))
	for k, val := range headers {
		v := fmt.Sprint(val)
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			continue
		}
		out[k] = v
	}
	return out
}

func ExtraHeadersCRLFKey(modelParams map[string]any) (string, bool) {
	headers, ok := modelParams["extra_headers"].(map[string]any)
	if !ok {
		return "", false
	}
	for k, v := range headers {
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(fmt.Sprint(v), "\r\n") {
			return k, true
		}
	}
	return "", false
}

func parseAPITLSConfig(cfg *llm.BifrostConfig, v map[string]any, logger *slog.Logger) error {
	if skip, ok := v["api_skip_tls_verify"].(bool); ok && skip {
		logger.Warn("TLS certificate verification disabled for LLM provider — do not use in production")
		cfg.InsecureSkipVerify = true
	}
	caCertPath, ok := v["api_ca_cert"].(string)
	if !ok || caCertPath == "" {
		return nil
	}
	if cfg.InsecureSkipVerify {
		logger.Warn("api_ca_cert is ignored when api_skip_tls_verify is true")
		return nil
	}
	pemData, err := readAndValidatePEM("api_ca_cert", caCertPath)
	if err != nil {
		return err
	}
	cfg.CACertPEM = pemData
	logger.Info("custom CA certificate loaded", "path", caCertPath)
	return nil
}

func parseGrantID(v map[string]any) (string, error) {
	raw, ok := v[llm.GrantIDKey]
	if !ok {
		return "", nil
	}
	id, _ := raw.(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("%s must name an OAuth grant", llm.GrantIDKey)
	}
	if key, _ := v["api_key"].(string); key != "" {
		return "", fmt.Errorf("%s cannot be combined with api_key", llm.GrantIDKey)
	}
	if _, oauth := v["oauth_token_url"]; oauth {
		return "", fmt.Errorf("%s cannot be combined with OAuth2 client credentials", llm.GrantIDKey)
	}
	return id, nil
}

func parseAzureWorkloadIdentity(v map[string]any, provider string) (*llm.AzureWorkloadIdentity, error) {
	raw, ok := v[llm.AzureWorkloadIdentityKey]
	if !ok {
		return nil, nil
	}
	block, _ := raw.(map[string]any)
	clientID, _ := block["client_id"].(string)
	tenantID, _ := block["tenant_id"].(string)
	id := &llm.AzureWorkloadIdentity{ClientID: strings.TrimSpace(clientID), TenantID: strings.TrimSpace(tenantID)}
	if id.ClientID == "" || id.TenantID == "" {
		return nil, fmt.Errorf("%s requires client_id and tenant_id", llm.AzureWorkloadIdentityKey)
	}
	if provider != "azure" {
		return nil, fmt.Errorf("%s requires an azure model, got provider %q", llm.AzureWorkloadIdentityKey, provider)
	}
	return id, nil
}

func checkAzureWorkloadIdentityConfig(v, providerCfg map[string]any) error {
	for _, keys := range []map[string]any{v, providerCfg} {
		for key, value := range keys {
			if value == nil || value == "" {
				continue
			}
			if llm.IsAzureCredentialKey(key) {
				return fmt.Errorf("%s cannot be combined with %s", llm.AzureWorkloadIdentityKey, key)
			}
		}
	}
	return nil
}

func parseOAuth2Config(v map[string]any, logger *slog.Logger) (*oauth2cc.Config, error) {
	oauthURL, _ := v["oauth_token_url"].(string)
	oauthID, _ := v["oauth_client_id"].(string)
	oauthSecret, _ := v["oauth_client_secret"].(string)
	if oauthURL == "" && oauthID == "" && oauthSecret == "" {
		return nil, nil
	}
	if oauthURL == "" || oauthID == "" || oauthSecret == "" {
		logger.Warn("incomplete OAuth2 config: oauth_token_url, oauth_client_id, and oauth_client_secret are all required")
		return nil, nil
	}
	oauthCfg := &oauth2cc.Config{
		TokenURL:     oauthURL,
		ClientID:     oauthID,
		ClientSecret: oauthSecret,
	}
	if scope, ok := v["oauth_scope"].(string); ok {
		oauthCfg.Scope = scope
	}
	if err := parseOAuth2TLSConfig(oauthCfg, v, logger); err != nil {
		return nil, err
	}
	if buf, ok := v["oauth_token_refresh_buffer_seconds"]; ok {
		oauthCfg.TokenRefreshBufferSeconds = intOrFloat(buf)
	}
	if retries, ok := v["oauth_max_retries"]; ok {
		oauthCfg.MaxRetries = intOrFloat(retries)
	}
	if allow, ok := v["oauth_allow_insecure_token_url"].(bool); ok {
		oauthCfg.AllowInsecureTokenURL = allow
	}
	return oauthCfg, nil
}

func azureServicePrincipalFromOAuth(oauthCfg *oauth2cc.Config, logger *slog.Logger) (map[string]any, bool) {
	tenant, ok := extractEntraTenantID(oauthCfg.TokenURL)
	if !ok {
		tokenURLHost := oauthCfg.TokenURL
		if parsed, parseErr := url.Parse(oauthCfg.TokenURL); parseErr == nil {
			tokenURLHost = parsed.Host
		}
		logger.Info("azure oauth2: token_url is not an Entra endpoint, using generic OAuth2 path (may not work with Azure OpenAI)", "tokenURLHost", tokenURLHost)
		return nil, false
	}
	out := map[string]any{
		"client_id":     oauthCfg.ClientID,
		"client_secret": oauthCfg.ClientSecret,
		"tenant_id":     tenant,
	}
	if scopes := strings.Fields(oauthCfg.Scope); len(scopes) > 0 {
		out["scopes"] = scopes
	}
	return out, true
}

func extractEntraTenantID(tokenURL string) (string, bool) {
	u, err := url.Parse(tokenURL)
	if err != nil {
		return "", false
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 2 || segments[0] == "" || segments[1] != "oauth2" {
		return "", false
	}
	return segments[0], true
}

func parseOAuth2TLSConfig(oauthCfg *oauth2cc.Config, v map[string]any, logger *slog.Logger) error {
	if skip, ok := v["oauth_skip_tls_verify"].(bool); ok && skip {
		logger.Warn("TLS certificate verification disabled for OAuth2 token endpoint — do not use in production")
		oauthCfg.InsecureSkipVerify = true
	}
	caCertPath, ok := v["oauth_ca_cert"].(string)
	if !ok || caCertPath == "" {
		return nil
	}
	oauthCfg.CACertPath = caCertPath
	if oauthCfg.InsecureSkipVerify {
		logger.Warn("oauth_ca_cert is ignored when oauth_skip_tls_verify is true")
		return nil
	}
	if _, err := readAndValidatePEM("oauth_ca_cert", caCertPath); err != nil {
		return err
	}
	logger.Info("custom OAuth2 CA certificate loaded", "path", caCertPath)
	return nil
}

func readAndValidatePEM(configKey, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf(readFileErrFmt, configKey, path, err)
	}
	if block, _ := pem.Decode(data); block == nil {
		return "", fmt.Errorf("%s (%s): no valid PEM block found", configKey, path)
	}
	return string(data), nil
}

func collectProviderConfig(v map[string]any) (map[string]any, error) {
	extra := make(map[string]any)
	for k, val := range v {
		if IsClientConfigKey(k) {
			continue
		}
		extra[k] = val
	}
	for k, val := range extra {
		if !strings.HasSuffix(k, "_file") {
			continue
		}
		path, ok := val.(string)
		if !ok || path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf(readFileErrFmt, k, path, err)
		}
		baseKey := strings.TrimSuffix(k, "_file")
		extra[baseKey] = strings.TrimSpace(string(data))
		delete(extra, k)
	}
	return extra, nil
}

func ExtractCacheStrategy(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	s, ok := m["cache_strategy"].(string)
	if !ok {
		return ""
	}
	switch s {
	case "none", "5m", "1h":
		return s
	}
	return ""
}

func ExtractReasoningEffort(raw any) (normalized string, offending string, rejected bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return "", "", false
	}
	thinking, ok := m["thinking"].(map[string]any)
	if !ok {
		return "", "", false
	}
	value, present := thinking["effort"]
	if !present || value == nil {
		return "", "", false
	}
	effort, isString := value.(string)
	if !isString {
		return "", fmt.Sprintf("%v", value), true
	}
	if strings.TrimSpace(effort) == "" {
		return "", "", false
	}
	if normalized = llm.NormalizeReasoningEffort(effort); normalized == "" {
		return "", effort, true
	}
	return normalized, "", false
}

func ExtractReasoningBudgetTokens(raw any) int {
	m, ok := raw.(map[string]any)
	if !ok {
		return 0
	}
	thinking, ok := m["thinking"].(map[string]any)
	if !ok {
		return 0
	}
	switch v := thinking["budget_tokens"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

func SentModelName(model string) string {
	return llm.QualifiedModel(ParseModelProvider(model))
}

func ParseModelProvider(model string) (provider, bareModel string) {
	if i := strings.IndexByte(model, '/'); i >= 0 {
		prefix := strings.ToLower(model[:i])
		if p, ok := knownProviders[prefix]; ok {
			return p, model[i+1:]
		}
	}

	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "claude") || strings.Contains(lower, "anthropic"):
		return "anthropic", model
	case strings.Contains(lower, "gemini"):
		return "gemini", model
	default:
		return "openai", model
	}
}
