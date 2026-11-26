// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var pathParamRE = regexp.MustCompile(`\{[^/}]*\}`)

var blockedHeaders = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
}

const defaultMaxBodyBytes = 1 << 20
const maxAllowedBodyBytes = 50 << 20
const staticExprPrefix = "static:"

type Config struct {
	Routes       []*RouteConfig
	MaxBodyBytes int64
}

type RouteConfig struct {
	Name  string
	Match RouteMatch

	Auth *AuthConfig

	MaxBodyBytes int64

	Headers map[string]string

	InputExpression string

	TargetAgentName    string
	TargetWorkflowName string

	RunAs string

	ArtifactProcessing *ArtifactProcessingConfig
}

type RouteMatch struct {
	Path   string
	Method string
}

type AuthConfig struct {
	Token *TokenAuthConfig
	HMAC  *HMACAuthConfig
}

type TokenAuthConfig struct {
	Location string
	Name     string
	Value    string
}

type HMACAuthConfig struct {
	Header   string
	Algo     string
	Secret   string
	Prefix   string
	Encoding string
}

type ArtifactProcessingConfig struct {
	ExtractExpression string
	Definition        ArtifactDefinition
}

type ArtifactDefinition struct {
	Filename        string
	Content         string
	MimeType        string
	ContentEncoding string
}

func ParseConfig(adapterCfg map[string]any) (*Config, error) {
	if adapterCfg == nil {
		adapterCfg = map[string]any{}
	}

	cfg := &Config{
		MaxBodyBytes: defaultMaxBodyBytes,
	}

	if err := checkIntType(adapterCfg, "max_body_bytes"); err != nil {
		return nil, err
	}
	if v := getInt(adapterCfg, "max_body_bytes"); v < 0 {
		return nil, fmt.Errorf("max_body_bytes must be non-negative, got %d", v)
	} else if v > int(maxAllowedBodyBytes) {
		return nil, fmt.Errorf("max_body_bytes must not exceed %d bytes (50 MiB), got %d", maxAllowedBodyBytes, v)
	} else if v > 0 {
		cfg.MaxBodyBytes = int64(v)
	}

	routesRaw, _ := adapterCfg["routes"].([]any)
	for i, raw := range routesRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("routes[%d]: expected map", i)
		}
		route, err := parseRoute(m)
		if err != nil {
			return nil, fmt.Errorf("routes[%d]: %w", i, err)
		}
		cfg.Routes = append(cfg.Routes, route)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if len(c.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}
	seenNames := make(map[string]struct{}, len(c.Routes))
	seenPatterns := make(map[string]struct{}, len(c.Routes))
	for _, r := range c.Routes {
		if err := r.validateRoute(seenNames, seenPatterns); err != nil {
			return err
		}
	}
	return checkServeMuxConflicts(c.Routes)
}

func (r *RouteConfig) validateRoute(seenNames, seenPatterns map[string]struct{}) error {
	if err := r.validateIdentity(seenNames); err != nil {
		return err
	}
	if err := r.validateMatch(seenPatterns); err != nil {
		return err
	}
	if err := r.validateAuth(); err != nil {
		return err
	}
	if err := r.validateHeaderAliases(); err != nil {
		return err
	}
	return r.validateArtifactProcessing()
}

func (r *RouteConfig) validateIdentity(seenNames map[string]struct{}) error {
	if r.Name == "" {
		return fmt.Errorf("route 'name' is required")
	}
	nameKey := strings.ToLower(r.Name)
	if _, dup := seenNames[nameKey]; dup {
		return fmt.Errorf("duplicate route name %q", r.Name)
	}
	seenNames[nameKey] = struct{}{}
	if r.Match.Path == "" {
		return fmt.Errorf("route %q: match.path is required", r.Name)
	}
	if !strings.HasPrefix(r.Match.Path, "/") {
		return fmt.Errorf("route %q: match.path must start with '/'", r.Name)
	}
	if r.InputExpression == "" {
		return fmt.Errorf("route %q: input_expression is required", r.Name)
	}
	switch {
	case r.TargetAgentName == "" && r.TargetWorkflowName == "":
		return fmt.Errorf("route %q: target_agent_name or target_workflow_name is required", r.Name)
	case r.TargetAgentName != "" && r.TargetWorkflowName != "":
		return fmt.Errorf("route %q: target_agent_name and target_workflow_name are mutually exclusive", r.Name)
	}
	return nil
}

func (r *RouteConfig) validateMatch(seenPatterns map[string]struct{}) error {
	switch strings.ToUpper(r.Match.Method) {
	case "POST", "PUT", "PATCH":
	default:
		return fmt.Errorf("route %q: match.method must be one of POST/PUT/PATCH, got %q", r.Name, r.Match.Method)
	}
	normalisedPath := pathParamRE.ReplaceAllString(r.Match.Path, "{}")
	patternKey := strings.ToUpper(r.Match.Method) + " " + normalisedPath
	if _, dup := seenPatterns[patternKey]; dup {
		return fmt.Errorf("route %q: duplicate method+path %q", r.Name, strings.TrimSpace(patternKey))
	}
	seenPatterns[patternKey] = struct{}{}
	return nil
}

func (r *RouteConfig) validateAuth() error {
	if r.Auth == nil {
		return fmt.Errorf("route %q: auth block is required (token, hmac, or both)", r.Name)
	}
	if r.Auth.Token == nil && r.Auth.HMAC == nil {
		return fmt.Errorf("route %q: auth must configure at least one of token or hmac", r.Name)
	}
	if r.Auth.Token != nil {
		if err := r.validateTokenAuth(); err != nil {
			return err
		}
	}
	if r.Auth.HMAC != nil {
		if err := r.validateHMACAuth(); err != nil {
			return err
		}
	}
	return nil
}

func (r *RouteConfig) validateTokenAuth() error {
	if len(r.Auth.Token.Value) < 8 {
		return fmt.Errorf("route %q: auth.token.value must be at least 8 characters", r.Name)
	}
	switch strings.ToLower(r.Auth.Token.Location) {
	case "", "bearer":
	case "header", "query":
		if r.Auth.Token.Name == "" {
			return fmt.Errorf("route %q: auth.token.name is required when location is %q", r.Name, r.Auth.Token.Location)
		}
	default:
		return fmt.Errorf("route %q: auth.token.location must be bearer, header, or query, got %q", r.Name, r.Auth.Token.Location)
	}
	return nil
}

func (r *RouteConfig) validateHMACAuth() error {
	if r.Auth.HMAC.Header == "" {
		return fmt.Errorf("route %q: auth.hmac.header is required", r.Name)
	}
	if len(r.Auth.HMAC.Secret) < 16 {
		return fmt.Errorf("route %q: auth.hmac.secret must be at least 16 characters", r.Name)
	}
	switch strings.ToLower(r.Auth.HMAC.Algo) {
	case "sha256", "sha512":
	default:
		return fmt.Errorf("route %q: auth.hmac.algo must be sha256 or sha512, got %q", r.Name, r.Auth.HMAC.Algo)
	}
	switch strings.ToLower(r.Auth.HMAC.Encoding) {
	case "hex", "base64", "base64url", "base64raw", "base64rawurl":
	default:
		return fmt.Errorf("route %q: auth.hmac.encoding must be hex, base64, base64url, base64raw, or base64rawurl, got %q", r.Name, r.Auth.HMAC.Encoding)
	}
	return nil
}

func (r *RouteConfig) validateHeaderAliases() error {
	for alias, header := range r.Headers {
		if alias == "" || header == "" {
			return fmt.Errorf("route %q: headers entries must have a non-empty alias and header name", r.Name)
		}
		if _, blocked := blockedHeaders[strings.ToLower(header)]; blocked {
			return fmt.Errorf("route %q: headers alias %q targets %q which may not be exposed to agent input", r.Name, alias, header)
		}
		if r.Auth != nil && r.Auth.HMAC != nil && strings.EqualFold(header, r.Auth.HMAC.Header) {
			return fmt.Errorf("route %q: headers alias %q targets the HMAC signature header which may not be exposed to agent input", r.Name, alias)
		}
		if r.Auth != nil && r.Auth.Token != nil && strings.EqualFold(r.Auth.Token.Location, "header") && strings.EqualFold(header, r.Auth.Token.Name) {
			return fmt.Errorf("route %q: headers alias %q targets the token auth header which may not be exposed to agent input", r.Name, alias)
		}
	}
	return nil
}

func (r *RouteConfig) validateArtifactProcessing() error {
	ap := r.ArtifactProcessing
	if ap == nil {
		return nil
	}
	if ap.ExtractExpression == "" {
		return fmt.Errorf("route %q: artifact_processing.extract_artifacts_expression is required", r.Name)
	}
	if ap.Definition.Content == "" {
		return fmt.Errorf("route %q: artifact_processing.artifact_definition.content is required", r.Name)
	}
	if enc := ap.Definition.ContentEncoding; enc != "" {
		if strings.HasPrefix(enc, staticExprPrefix) {
			literal := strings.ToLower(strings.TrimPrefix(enc, staticExprPrefix))
			switch literal {
			case "", "utf-8", "base64", "base64url", "base64raw", "base64rawurl":
			default:
				return fmt.Errorf("route %q: artifact_processing.artifact_definition.content_encoding %q is not supported; use utf-8, base64, base64url, base64raw, or base64rawurl", r.Name, enc)
			}
		} else if !strings.Contains(enc, ":") {
			switch strings.ToLower(enc) {
			case "utf-8", "base64", "base64url", "base64raw", "base64rawurl":
				ap.Definition.ContentEncoding = staticExprPrefix + enc
			default:
				return fmt.Errorf("route %q: artifact_processing.artifact_definition.content_encoding %q is not supported; use utf-8, base64, base64url, base64raw, or base64rawurl", r.Name, enc)
			}
		}
	}
	return nil
}

func checkServeMuxConflicts(routes []*RouteConfig) error {
	mux := http.NewServeMux()
	for _, route := range routes {
		if err := tryRegister(mux, route); err != nil {
			return err
		}
	}
	return nil
}

func tryRegister(mux *http.ServeMux, route *RouteConfig) (err error) {
	pattern := servePattern(route.Match)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("route %q: pattern %q rejected: %v", route.Name, pattern, r)
		}
	}()
	mux.HandleFunc(pattern, http.NotFound)
	return nil
}

func parseRoute(m map[string]any) (*RouteConfig, error) {
	r := &RouteConfig{
		Name:               getString(m, "name"),
		InputExpression:    getString(m, "input_expression"),
		TargetAgentName:    getString(m, "target_agent_name"),
		TargetWorkflowName: getString(m, "target_workflow_name"),
		RunAs:              getString(m, "run_as"),
	}

	if mm, ok := m["match"].(map[string]any); ok {
		r.Match = RouteMatch{
			Path:   getString(mm, "path"),
			Method: strings.ToUpper(getString(mm, "method")),
		}
	}

	if err := checkIntType(m, "max_body_bytes"); err != nil {
		return nil, err
	}
	if v := getInt(m, "max_body_bytes"); v < 0 {
		return nil, fmt.Errorf("max_body_bytes must be non-negative, got %d", v)
	} else if v > int(maxAllowedBodyBytes) {
		return nil, fmt.Errorf("max_body_bytes must not exceed %d bytes (50 MiB), got %d", maxAllowedBodyBytes, v)
	} else if v > 0 {
		r.MaxBodyBytes = int64(v)
	}

	if authMap, ok := m["auth"].(map[string]any); ok {
		r.Auth = parseAuth(authMap)
	}

	if hm, ok := m["headers"].(map[string]any); ok {
		r.Headers = make(map[string]string, len(hm))
		for alias, v := range hm {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("headers[%q]: value must be a string, got %T", alias, v)
			}
			r.Headers[alias] = s
		}
	}

	if raw, present := m["artifact_processing"]; present {
		ap, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("artifact_processing must be a map, got %T", raw)
		}
		r.ArtifactProcessing = &ArtifactProcessingConfig{
			ExtractExpression: getString(ap, "extract_artifacts_expression"),
		}
		if def, ok := ap["artifact_definition"].(map[string]any); ok {
			r.ArtifactProcessing.Definition = ArtifactDefinition{
				Filename:        getString(def, "filename"),
				Content:         getString(def, "content"),
				MimeType:        getString(def, "mime_type"),
				ContentEncoding: getString(def, "content_encoding"),
			}
		}
	}

	return r, nil
}

func parseAuth(m map[string]any) *AuthConfig {
	auth := &AuthConfig{}
	if tm, ok := m["token"].(map[string]any); ok {
		auth.Token = &TokenAuthConfig{
			Location: getString(tm, "location"),
			Name:     getString(tm, "name"),
			Value:    getString(tm, "value"),
		}
		if auth.Token.Location == "" {
			auth.Token.Location = "bearer"
		}
	}
	if hm, ok := m["hmac"].(map[string]any); ok {
		auth.HMAC = &HMACAuthConfig{
			Header:   getString(hm, "header"),
			Algo:     getString(hm, "algo"),
			Secret:   getString(hm, "secret"),
			Prefix:   getString(hm, "prefix"),
			Encoding: getString(hm, "encoding"),
		}
		if auth.HMAC.Algo == "" {
			auth.HMAC.Algo = "sha256"
		}
		if auth.HMAC.Encoding == "" {
			auth.HMAC.Encoding = "hex"
		}
	}
	if auth.Token == nil && auth.HMAC == nil {
		return nil
	}
	return auth
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func checkIntType(m map[string]any, key string) error {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch v.(type) {
	case int, int64, float64:
		return nil
	default:
		return fmt.Errorf("%s must be a number, got %T", key, v)
	}
}
