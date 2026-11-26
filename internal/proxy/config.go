// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type ProxyConfig struct {
	Name      string
	Namespace string

	URL           string
	AgentCardPath string
	SSLVerify     bool

	RequestTimeoutSeconds        int
	DefaultRequestTimeoutSeconds int
	DiscoveryIntervalSeconds     int

	UseAgentCardURL bool
	DisplayName     string
	AgentCardData   map[string]any

	AgentCardAuth       *AuthConfig
	TaskAuth            *AuthConfig
	LegacyAuth          *AuthConfig
	UseAuthForAgentCard bool

	AgentCardHeaders []HeaderConfig
	TaskHeaders      []HeaderConfig

	ArtifactHandlingMode string

	TaskStateTTLMinutes        int
	TaskCleanupIntervalMinutes int

	ConvertProgressUpdates bool

	ProtocolVersion string

	TrustedCardJWKSURLs []string
	RequireSignedCard   bool
}

const (
	ProtocolVersionAuto = "auto"
	ProtocolVersion10   = "1.0"
	ProtocolVersion03   = "0.3"
)

type AuthConfig struct {
	Type                      string
	Token                     string
	Header                    string
	TokenURL                  string
	ClientID                  string
	ClientSecret              string
	Scope                     string
	TokenCacheDurationSeconds int
	AllowInsecureTokenURL     bool

	CACertPath                string
	MaxRetries                int
	TokenRefreshBufferSeconds int
	InsecureSkipVerify        bool

	AuthorizationURL        string
	RedirectURI             string
	Scopes                  []string
	TokenEndpointAuthMethod string
}

type HeaderConfig struct {
	Name  string
	Value string
}

func (c *ProxyConfig) EffectiveRequestTimeout() int {
	if c.RequestTimeoutSeconds > 0 {
		return c.RequestTimeoutSeconds
	}
	if c.DefaultRequestTimeoutSeconds > 0 {
		return c.DefaultRequestTimeoutSeconds
	}
	return config.DefaultRequestTimeoutSeconds
}

func (c *ProxyConfig) EffectiveProtocolVersion() string {
	return cmp.Or(c.ProtocolVersion, ProtocolVersionAuto)
}

func (c *ProxyConfig) EffectiveTaskAuth() *AuthConfig {
	if c.TaskAuth != nil {
		return c.TaskAuth
	}
	return c.LegacyAuth
}

func (c *ProxyConfig) EffectiveCardAuth() *AuthConfig {
	if c.AgentCardAuth != nil {
		return c.AgentCardAuth
	}
	if c.UseAuthForAgentCard {
		return c.LegacyAuth
	}
	return nil
}

func intDefault(cfg config.Config, key string, def int) int {
	if cfg.Has(key) {
		return cfg.GetInt(key)
	}
	return def
}

func boolDefault(cfg config.Config, key string, def bool) bool {
	if cfg.Has(key) {
		return cfg.GetBool(key)
	}
	return def
}

func stringDefault(cfg config.Config, key string, def string) string {
	if v := cfg.GetString(key); v != "" {
		return v
	}
	return def
}

const proxyErrorFormat = "proxy %q: %w"

func ParseProxyConfig(name string, cfg config.Config, logger *slog.Logger) (*ProxyConfig, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if name == "" {
		return nil, fmt.Errorf("proxy config missing required name")
	}

	namespace := cfg.GetString(config.KeyNamespace)
	if err := a2a.ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf(proxyErrorFormat, name, err)
	}
	if namespace == "" {
		namespace = config.DefaultNamespace
	}

	pc := &ProxyConfig{
		Name:      name,
		Namespace: namespace,
		URL:       cfg.GetString("url"),

		AgentCardPath: stringDefault(cfg, "agent_card_path", "/.well-known/agent-card.json"),
		SSLVerify:     boolDefault(cfg, "ssl_verify", true),

		RequestTimeoutSeconds:        intDefault(cfg, "request_timeout_seconds", 0),
		DefaultRequestTimeoutSeconds: intDefault(cfg, "default_request_timeout_seconds", config.DefaultRequestTimeoutSeconds),
		DiscoveryIntervalSeconds:     intDefault(cfg, "discovery_interval_seconds", config.DefaultDiscoveryIntervalSeconds),

		UseAgentCardURL: boolDefault(cfg, "use_agent_card_url", true),
		DisplayName:     cfg.GetString("display_name"),

		ArtifactHandlingMode: stringDefault(cfg, "artifact_handling_mode", "reference"),

		TaskStateTTLMinutes:        intDefault(cfg, "task_state_ttl_minutes", 60),
		TaskCleanupIntervalMinutes: intDefault(cfg, "task_cleanup_interval_minutes", 10),

		ConvertProgressUpdates: boolDefault(cfg, "convert_progress_updates", true),
	}

	if m, ok := staticCardData(cfg.GetRaw("agent_card_data")); ok {
		pc.AgentCardData = m
	}

	if pc.URL == "" && pc.AgentCardData == nil {
		return nil, fmt.Errorf("proxy %q: 'url' is required (or provide 'agent_card_data' for static card)", name)
	}

	protocolVersion, err := parseProtocolVersion(cfg.GetString("protocol_version"))
	if err != nil {
		return nil, fmt.Errorf(proxyErrorFormat, name, err)
	}
	pc.ProtocolVersion = protocolVersion

	if err = parseCardTrust(cfg, pc); err != nil {
		return nil, fmt.Errorf(proxyErrorFormat, name, err)
	}

	pc.AgentCardAuth = parseAuthConfig(cfg.GetSection("agent_card_authentication"), logger)
	pc.TaskAuth = parseAuthConfig(cfg.GetSection("task_authentication"), logger)
	pc.LegacyAuth = parseAuthConfig(cfg.GetSection("authentication"), logger)
	pc.UseAuthForAgentCard = boolDefault(cfg, "use_auth_for_agent_card", false)

	pc.AgentCardHeaders = parseHeaders(cfg.GetRaw("agent_card_headers"))
	pc.TaskHeaders = parseHeaders(cfg.GetRaw("task_headers"))

	return pc, nil
}

func parseProtocolVersion(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "", ProtocolVersionAuto:
		return ProtocolVersionAuto, nil
	case "1", ProtocolVersion10:
		return ProtocolVersion10, nil
	case ProtocolVersion03, "0.3.0":
		return ProtocolVersion03, nil
	default:
		return "", fmt.Errorf("'protocol_version' must be %s, %s or %s, got %q", ProtocolVersionAuto, ProtocolVersion10, ProtocolVersion03, raw)
	}
}

func parseCardTrust(cfg config.Config, pc *ProxyConfig) error {
	if err := refuseBlankCardTrust(cfg); err != nil {
		return err
	}
	var err error
	if pc.TrustedCardJWKSURLs, err = parseTrustedCardJWKSURLs(cfg.GetRaw("trusted_card_jwks_urls")); err != nil {
		return err
	}
	if pc.RequireSignedCard, err = parseStrictBool("require_signed_card", cfg.GetRaw("require_signed_card")); err != nil {
		return err
	}
	switch {
	case pc.AgentCardData != nil && (len(pc.TrustedCardJWKSURLs) > 0 || pc.RequireSignedCard):
		return errors.New("'trusted_card_jwks_urls' and 'require_signed_card' check a fetched card, so they cannot be used with 'agent_card_data'")
	case pc.RequireSignedCard && len(pc.TrustedCardJWKSURLs) == 0:
		return errors.New("'require_signed_card' needs 'trusted_card_jwks_urls' to verify the signature against")
	}
	return nil
}

func parseStrictBool(key string, raw any) (bool, error) {
	switch v := raw.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on":
			return true, nil
		case "false", "no", "off":
			return false, nil
		}
	}
	return false, fmt.Errorf("'%s' must be true or false", key)
}

func parseTrustedCardJWKSURLs(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("'trusted_card_jwks_urls' must be a list of URLs")
	}
	if len(list) > MaxTrustedCardJWKSURLs {
		return nil, fmt.Errorf("'trusted_card_jwks_urls' takes at most %d URLs", MaxTrustedCardJWKSURLs)
	}
	urls := make([]string, 0, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("'trusted_card_jwks_urls' entry %d must be a string", i+1)
		}
		u, err := url.Parse(strings.TrimSpace(s))
		if err != nil || u.Host == "" || (u.Scheme != "https" && (u.Scheme != "http" || !IsLoopbackHost(u.Hostname()))) {
			return nil, fmt.Errorf("'trusted_card_jwks_urls' entry %d must be an absolute https URL (http only on a loopback host)", i+1)
		}
		if !slices.Contains(urls, u.String()) {
			urls = append(urls, u.String())
		}
	}
	return urls, nil
}

const MaxTrustedCardJWKSURLs = 10

func refuseBlankCardTrust(cfg config.Config) error {
	for _, key := range cardTrustFields {
		if cfg.Has(key) && cfg.GetRaw(key) == nil {
			return fmt.Errorf("'%s' is present but blank; remove it or give it a value", key)
		}
	}
	return nil
}

func staticCardData(raw any) (map[string]any, bool) {
	m, ok := raw.(map[string]any)
	return m, ok
}

func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func parseAuthConfig(cfg config.Config, logger *slog.Logger) *AuthConfig {
	if cfg == nil {
		return nil
	}
	authType := cfg.GetString("type")
	if authType == "" {
		switch scheme := cfg.GetString("scheme"); scheme {
		case "bearer":
			authType = "static_bearer"
			logger.Warn("proxy auth: 'scheme: bearer' is deprecated, use 'type: static_bearer'")
		case "apikey":
			authType = "static_apikey"
			logger.Warn("proxy auth: 'scheme: apikey' is deprecated, use 'type: static_apikey'")
		case "":
			return nil
		default:
			logger.Warn("proxy auth: unknown scheme, ignoring", "scheme", scheme)
			return nil
		}
	}
	ac := &AuthConfig{
		Type:                      authType,
		Token:                     cfg.GetString("token"),
		Header:                    cfg.GetString("header"),
		TokenURL:                  cfg.GetString("token_url"),
		ClientID:                  cfg.GetString("client_id"),
		ClientSecret:              cfg.GetString("client_secret"),
		Scope:                     cfg.GetString("scope"),
		TokenCacheDurationSeconds: intDefault(cfg, "token_cache_duration_seconds", 3300),
		AllowInsecureTokenURL:     cfg.GetBool("allow_insecure_token_url"),
		AuthorizationURL:          cfg.GetString("authorization_url"),
		RedirectURI:               cfg.GetString("redirect_uri"),
		TokenEndpointAuthMethod:   stringDefault(cfg, "token_endpoint_auth_method", "client_secret_post"),
	}

	ac.CACertPath = cfg.GetString("ca_cert_path")
	ac.MaxRetries = intDefault(cfg, "max_retries", 0)
	ac.InsecureSkipVerify = cfg.GetBool("insecure_skip_verify")

	if cfg.Has("token_refresh_buffer_seconds") {
		ac.TokenRefreshBufferSeconds = cfg.GetInt("token_refresh_buffer_seconds")
	} else if cfg.Has("token_cache_duration_seconds") && authType == "oauth2_client_credentials" {
		buffer := 3600 - ac.TokenCacheDurationSeconds
		if buffer <= 0 {
			buffer = 300
		}
		ac.TokenRefreshBufferSeconds = buffer
		logger.Warn("proxy auth: 'token_cache_duration_seconds' is deprecated for oauth2_client_credentials, use 'token_refresh_buffer_seconds' instead",
			"derivedBuffer", buffer,
			"assumption", "3600s token lifetime")
	}

	if raw := cfg.GetRaw("scopes"); raw != nil {
		if list, ok := raw.([]any); ok {
			for _, item := range list {
				if s, ok := item.(string); ok {
					ac.Scopes = append(ac.Scopes, s)
				}
			}
		}
	}

	if authType == "oauth2_client_credentials" && ac.Scope == "" && len(ac.Scopes) > 0 {
		logger.Warn("proxy auth: oauth2_client_credentials ignores the list 'scopes' key — use 'scope:' as a space-separated string instead",
			"scopes", ac.Scopes)
	}

	if authType == "oauth2_authorization_code" {
		if ac.ClientID == "" {
			logger.Warn("proxy auth: oauth2_authorization_code requires 'client_id'")
		}
		if ac.AuthorizationURL == "" {
			logger.Warn("proxy auth: oauth2_authorization_code requires 'authorization_url'")
		}
		if ac.TokenURL == "" {
			logger.Warn("proxy auth: oauth2_authorization_code requires 'token_url'")
		}
		if ac.Scope != "" && len(ac.Scopes) == 0 {
			logger.Warn("proxy auth: oauth2_authorization_code ignores the string 'scope' key — use 'scopes:' as a list instead",
				"scope", ac.Scope)
		}
		if ac.RedirectURI == "" {
			logger.Warn("proxy auth: oauth2_authorization_code 'redirect_uri' not set; will resolve at runtime via (1) $OAUTH_TOOL_REDIRECT_URI, (2) $FRONTEND_REDIRECT_URI + /api/v1/auth/tool/callback, or (3) http://127.0.0.1:8800/api/v1/auth/tool/callback (dev-only default)")
		}
	}

	return ac
}

func parseHeaders(raw any) []HeaderConfig {
	if raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	var headers []HeaderConfig
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		value, _ := m["value"].(string)
		if name != "" {
			headers = append(headers, HeaderConfig{Name: name, Value: value})
		}
	}
	return headers
}
