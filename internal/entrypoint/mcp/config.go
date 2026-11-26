// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Config struct {
	ServerName        string `json:"server_name"`
	ServerDescription string `json:"server_description"`
	Transport         string `json:"transport"`
	Host              string `json:"host"`
	Port              int    `json:"port"`

	TaskTimeoutSeconds int `json:"task_timeout_seconds"`

	IncludeTools []string `json:"include_tools"`
	ExcludeTools []string `json:"exclude_tools"`

	EnableAuth bool `json:"enable_auth"`

	ExternalAuthProvider string `json:"external_auth_provider"`

	RequirePKCE bool `json:"require_pkce"`

	OAuthCodeTTLSeconds int `json:"oauth_code_ttl_seconds"`

	OAuthStateTTLSeconds int `json:"oauth_state_ttl_seconds"`

	Issuer string `json:"issuer"`

	AllowedRedirectURIs []string `json:"allowed_redirect_uris"`

	UserIDClaim string `json:"user_id_claim"`

	DevMode bool `json:"dev_mode"`

	DefaultUserIdentity string `json:"default_user_identity"`

	RunAs string `json:"run_as"`

	StateRedirectionEnabled bool `json:"state_redirection_enabled"`

	OAuthDispatcherURL string `json:"oauth_dispatcher_url"`

	CORSAllowedOrigins []string `json:"cors_allowed_origins"`

	CORSAllowedOriginRegex string `json:"cors_allowed_origin_regex"`

	InlineImageMaxBytes  int `json:"inline_image_max_bytes"`
	InlineAudioMaxBytes  int `json:"inline_audio_max_bytes"`
	InlineTextMaxBytes   int `json:"inline_text_max_bytes"`
	InlineBinaryMaxBytes int `json:"inline_binary_max_bytes"`

	ResourceURIPrefix       string `json:"resource_uri_prefix"`
	EnableArtifactResources bool   `json:"enable_artifact_resources"`

	EnableFileInput   bool `json:"enable_file_input"`
	MaxInputFiles     int  `json:"max_input_files"`
	MaxInputFileBytes int  `json:"max_input_file_bytes"`

	compiledIncludeExact []patternMatcher
	compiledIncludeRegex []patternMatcher
	compiledExcludeExact []patternMatcher
	compiledExcludeRegex []patternMatcher
	compiledCORSRegex    *regexp.Regexp
}

type patternMatcher struct {
	re    *regexp.Regexp
	exact string
}

func (p *patternMatcher) matches(names []string) bool {
	if p.re != nil {
		for _, name := range names {
			if p.re.MatchString(name) {
				return true
			}
		}
		return false
	}
	for _, name := range names {
		if strings.ToLower(name) == p.exact {
			return true
		}
	}
	return false
}

func (c *Config) defaults() {
	if c.ServerName == "" {
		c.ServerName = "SAM MCP Entrypoint"
	}
	if c.ServerDescription == "" {
		c.ServerDescription = "Model Context Protocol entrypoint for Solace Agent Mesh"
	}
	if c.Transport == "" {
		c.Transport = "sse"
	}
	if c.Host == "" {
		c.Host = "0.0.0.0"
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	if c.TaskTimeoutSeconds == 0 {
		c.TaskTimeoutSeconds = 300
	}
	if c.OAuthCodeTTLSeconds == 0 {
		c.OAuthCodeTTLSeconds = 300
	}
	if c.OAuthStateTTLSeconds == 0 {
		c.OAuthStateTTLSeconds = 300
	}
	if c.UserIDClaim == "" {
		c.UserIDClaim = "email"
	}
	if c.InlineImageMaxBytes == 0 {
		c.InlineImageMaxBytes = 5_242_880
	}
	if c.InlineAudioMaxBytes == 0 {
		c.InlineAudioMaxBytes = 10_485_760
	}
	if c.InlineTextMaxBytes == 0 {
		c.InlineTextMaxBytes = 1_048_576
	}
	if c.InlineBinaryMaxBytes == 0 {
		c.InlineBinaryMaxBytes = 524_288
	}
	if c.ResourceURIPrefix == "" {
		c.ResourceURIPrefix = "artifact"
	}
	if c.MaxInputFiles == 0 {
		c.MaxInputFiles = defaultMaxInputFiles
	}
	if c.MaxInputFileBytes == 0 {
		c.MaxInputFileBytes = defaultMaxInputFileBytes
	}
}

const (
	defaultMaxInputFiles      = 5
	defaultMaxInputFileBytes  = 10 << 20
	ceilingMaxInputFiles      = 20
	ceilingInputFileTotalSize = 128 << 20
	baseRequestBodyBytes      = 8 << 20
)

func (c *Config) fileInputBodyLimit() int64 {
	return baseRequestBodyBytes + int64(c.MaxInputFiles)*int64(base64.StdEncoding.EncodedLen(c.MaxInputFileBytes))
}

func (c *Config) validate() error {
	if c.Transport == "http" {
		c.Transport = "sse"
	}
	switch c.Transport {
	case "sse", "stdio":
	default:
		return fmt.Errorf("mcp adapter: unknown transport %q (expected \"sse\", \"http\", or \"stdio\")", c.Transport)
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("mcp adapter: invalid port %d", c.Port)
	}
	switch c.UserIDClaim {
	case "email", "sub", "upn", "preferred_username":
	default:
		return fmt.Errorf("mcp adapter: invalid user_id_claim %q (expected one of: email, sub, upn, preferred_username)", c.UserIDClaim)
	}
	if c.EnableAuth && c.DefaultUserIdentity != "" && c.Transport != "stdio" {
		return fmt.Errorf("mcp adapter: default_user_identity is incompatible with enable_auth=true on transport %q (set one or the other)", c.Transport)
	}
	if rbac.IsSystemPrincipal(c.DefaultUserIdentity) {
		return fmt.Errorf("mcp adapter: default_user_identity %q uses the reserved %q prefix — set run_as to name a service account instead", c.DefaultUserIdentity, rbac.SystemSubPrefix)
	}
	if c.StateRedirectionEnabled && c.OAuthDispatcherURL == "" {
		return fmt.Errorf("mcp adapter: state_redirection_enabled=true requires oauth_dispatcher_url")
	}
	if c.StateRedirectionEnabled && !c.EnableAuth {
		return fmt.Errorf("mcp adapter: state_redirection_enabled=true requires enable_auth=true")
	}
	if c.MaxInputFiles < 1 || c.MaxInputFiles > ceilingMaxInputFiles {
		return fmt.Errorf("mcp adapter: max_input_files %d is outside 1-%d", c.MaxInputFiles, ceilingMaxInputFiles)
	}
	if c.MaxInputFileBytes < 0 || c.MaxInputFileBytes > ceilingInputFileTotalSize/c.MaxInputFiles {
		return fmt.Errorf("mcp adapter: max_input_files x max_input_file_bytes (%d x %d) exceeds %d bytes",
			c.MaxInputFiles, c.MaxInputFileBytes, ceilingInputFileTotalSize)
	}
	return nil
}

func (c *Config) compilePatterns() error {
	compile := func(patterns []string) (exact, regex []patternMatcher, err error) {
		for _, p := range patterns {
			isRegex := strings.ContainsAny(p, ".*+?^${}()|[]\\")
			if isRegex {
				re, cerr := regexp.Compile("(?i)" + p)
				if cerr != nil {
					return nil, nil, fmt.Errorf("mcp adapter: invalid filter pattern %q: %w", p, cerr)
				}
				regex = append(regex, patternMatcher{re: re})
			} else {
				exact = append(exact, patternMatcher{exact: strings.ToLower(p)})
			}
		}
		return exact, regex, nil
	}

	var err error
	c.compiledIncludeExact, c.compiledIncludeRegex, err = compile(c.IncludeTools)
	if err != nil {
		return err
	}
	c.compiledExcludeExact, c.compiledExcludeRegex, err = compile(c.ExcludeTools)
	if err != nil {
		return err
	}
	c.compiledCORSRegex, err = httputil.CompileCORSOriginRegex(c.CORSAllowedOriginRegex, nil)
	if err != nil {
		return fmt.Errorf("mcp adapter: invalid cors_allowed_origin_regex %q: %w", c.CORSAllowedOriginRegex, err)
	}
	return nil
}

func optString(raw map[string]any, key string, dst *string) {
	if v, ok := raw[key].(string); ok {
		*dst = v
	}
}

func optBool(raw map[string]any, key string, dst *bool) {
	if v, ok := raw[key].(bool); ok {
		*dst = v
	}
}

func optInt(raw map[string]any, key string, dst *int) {
	switch v := raw[key].(type) {
	case int:
		*dst = v
	case float64:
		*dst = int(v)
	}
}

func optStringSlice(raw map[string]any, key string, dst *[]string) {
	v, ok := raw[key].([]any)
	if !ok {
		return
	}
	for _, item := range v {
		if s, ok := item.(string); ok {
			*dst = append(*dst, s)
		}
	}
}

func parseConfig(raw map[string]any) (*Config, error) {
	cfg := &Config{RequirePKCE: true, EnableArtifactResources: true}
	if raw == nil {
		cfg.defaults()
		if err := cfg.validate(); err != nil {
			return cfg, err
		}
		err := cfg.compilePatterns()
		return cfg, err
	}

	optString(raw, "server_name", &cfg.ServerName)
	optString(raw, "server_description", &cfg.ServerDescription)
	optString(raw, "transport", &cfg.Transport)
	optString(raw, "host", &cfg.Host)
	optInt(raw, "port", &cfg.Port)
	optInt(raw, "task_timeout_seconds", &cfg.TaskTimeoutSeconds)
	optStringSlice(raw, "include_tools", &cfg.IncludeTools)
	optStringSlice(raw, "exclude_tools", &cfg.ExcludeTools)
	optBool(raw, "enable_auth", &cfg.EnableAuth)
	optString(raw, "external_auth_provider", &cfg.ExternalAuthProvider)
	optBool(raw, "require_pkce", &cfg.RequirePKCE)
	optInt(raw, "oauth_code_ttl_seconds", &cfg.OAuthCodeTTLSeconds)
	optInt(raw, "oauth_state_ttl_seconds", &cfg.OAuthStateTTLSeconds)
	optString(raw, "issuer", &cfg.Issuer)
	optStringSlice(raw, "allowed_redirect_uris", &cfg.AllowedRedirectURIs)
	optString(raw, "user_id_claim", &cfg.UserIDClaim)
	optBool(raw, "dev_mode", &cfg.DevMode)
	optString(raw, "default_user_identity", &cfg.DefaultUserIdentity)
	optString(raw, "run_as", &cfg.RunAs)
	optBool(raw, "state_redirection_enabled", &cfg.StateRedirectionEnabled)
	optString(raw, "oauth_dispatcher_url", &cfg.OAuthDispatcherURL)
	optStringSlice(raw, "cors_allowed_origins", &cfg.CORSAllowedOrigins)
	optString(raw, "cors_allowed_origin_regex", &cfg.CORSAllowedOriginRegex)
	optInt(raw, "inline_image_max_bytes", &cfg.InlineImageMaxBytes)
	optInt(raw, "inline_audio_max_bytes", &cfg.InlineAudioMaxBytes)
	optInt(raw, "inline_text_max_bytes", &cfg.InlineTextMaxBytes)
	optInt(raw, "inline_binary_max_bytes", &cfg.InlineBinaryMaxBytes)
	optString(raw, "resource_uri_prefix", &cfg.ResourceURIPrefix)
	optBool(raw, "enable_artifact_resources", &cfg.EnableArtifactResources)
	optBool(raw, "enable_file_input", &cfg.EnableFileInput)
	optInt(raw, "max_input_files", &cfg.MaxInputFiles)
	optInt(raw, "max_input_file_bytes", &cfg.MaxInputFileBytes)

	cfg.defaults()
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	err := cfg.compilePatterns()
	return cfg, err
}
