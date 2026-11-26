// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tlsutil"
	"github.com/maximhq/bifrost/core/schemas"
)

const TrustAnnotationsKey = "trust_annotations"

const (
	MCPDefaultTimeoutSeconds = 30

	MCPDefaultSSEReadTimeoutSeconds = 300
)

func (cfg *ToolsetConfig) EffectiveSSEReadTimeout() time.Duration {
	switch {
	case cfg.SSEReadTimeout < 0:
		return 0
	case cfg.SSEReadTimeout == 0:
		return time.Duration(MCPDefaultSSEReadTimeoutSeconds) * time.Second
	default:
		return time.Duration(cfg.SSEReadTimeout) * time.Second
	}
}

const (
	connTypeStdio          = "stdio"
	connTypeSSE            = "sse"
	connTypeStreamableHTTP = "streamable-http"
)

const EnvMCPConnectorTLSVerify = "SAM_MCP_CONNECTOR_TLS_VERIFY"

func TLSVerifyFromEnv() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv(EnvMCPConnectorTLSVerify))) != "false"
}

type ToolsetConfig struct {
	ConnectionType string
	Command        string
	CommandArgs    []string
	URL            string
	Timeout        int
	EnvVars        map[string]string

	SSEReadTimeout int

	MaxResponseBytes int64

	TerminateOnClose *bool

	SSLVerify   *bool
	SSLCABundle string

	SSLClientCert string
	SSLClientKey  string

	SSLServerName string

	ToolName       string
	ToolNamePrefix string
	AllowList      []string
	DenyList       []string

	AuthType   string
	AuthConfig *MCPAuthConfig

	Headers map[string]string

	ToolConfig map[string]any

	RequiredScopes []string

	Manifest []ManifestTool

	TrustAnnotations bool

	ClientID string
}

type MCPAuthConfig = toolauth.AuthConfig

type OAuthScheme = toolauth.OAuthScheme

type MCPAuthCred = toolauth.AuthCredential

func ParseToolsetConfig(raw map[string]any) (*ToolsetConfig, error) {
	return parseToolsetConfig(raw, loadManifestFile)
}

func ValidateToolsetConfig(raw map[string]any) error {
	_, err := parseToolsetConfig(raw, deferManifestFile)
	return err
}

type manifestFileLoader func(path string) ([]ManifestTool, error)

func deferManifestFile(string) ([]ManifestTool, error) { return nil, nil }

func parseToolsetConfig(raw map[string]any, loadManifest manifestFileLoader) (*ToolsetConfig, error) {
	cfg := &ToolsetConfig{Timeout: 30}

	connParams, ok := raw["connection_params"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp tool config missing required 'connection_params'")
	}
	connType, _ := connParams["type"].(string)
	if connType == "" {
		return nil, fmt.Errorf("mcp connection_params missing required 'type'")
	}
	cfg.ConnectionType = connType

	switch connType {
	case connTypeStdio:
		if err := parseStdioConnection(cfg, connParams); err != nil {
			return nil, err
		}
	case connTypeSSE, connTypeStreamableHTTP:
		if err := parseHTTPConnection(cfg, connParams); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported mcp connection type %q (want %s, %s, or %s)",
			connType, connTypeStdio, connTypeSSE, connTypeStreamableHTTP)
	}

	mergeTopLevelEnvVars(cfg, raw)

	if err := parseToolFiltering(cfg, raw); err != nil {
		return nil, err
	}

	if authCfg, ok := raw["auth"].(map[string]any); ok {
		cfg.AuthType, _ = authCfg["type"].(string)
		cfg.AuthConfig = parseAuthConfig(authCfg, defaultCredentialKeyBase(cfg))
		if cfg.AuthConfig != nil &&
			(cfg.ConnectionType == connTypeSSE || cfg.ConnectionType == connTypeStreamableHTTP) {
			cfg.AuthConfig.ResourceURL = cfg.URL
		}
	}
	if tc, ok := raw["tool_config"].(map[string]any); ok {
		cfg.ToolConfig = tc
	}
	cfg.RequiredScopes = parseStringSlice(raw["required_scopes"])
	if trust, present := raw[TrustAnnotationsKey]; present {
		trusted, isBool := trust.(bool)
		if !isBool {
			return nil, fmt.Errorf("mcp tool config: %s must be a bool, got %T", TrustAnnotationsKey, trust)
		}
		cfg.TrustAnnotations = trusted
	}

	if err := parseManifestSection(cfg, raw, loadManifest); err != nil {
		return nil, err
	}

	applyMCPTLSVerifyEnvOverride(cfg)

	clientID, err := cfg.deriveClientID()
	if err != nil {
		return nil, err
	}
	cfg.ClientID = clientID

	return cfg, nil
}

const mcpTLSVerifyEnvVar = "SAM_MCP_CONNECTOR_TLS_VERIFY"

func applyMCPTLSVerifyEnvOverride(cfg *ToolsetConfig) {
	if cfg.SSLVerify != nil {
		return
	}
	if cfg.ConnectionType != connTypeSSE && cfg.ConnectionType != connTypeStreamableHTTP {
		return
	}
	if verify, ok := readEnvBool(mcpTLSVerifyEnvVar); ok && !verify {
		cfg.SSLVerify = new(bool)
	}
}

func readEnvBool(name string) (bool, bool) {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on", "y", "t":
		return true, true
	case "false", "0", "no", "off", "n", "f":
		return false, true
	default:
		return false, false
	}
}

func parseStdioConnection(cfg *ToolsetConfig, connParams map[string]any) error {
	cmd := extractStdioCommand(connParams, cfg)
	if cmd == "" {
		return fmt.Errorf("mcp stdio connection missing required 'command'")
	}
	cfg.Command = cmd

	cfg.CommandArgs = append(cfg.CommandArgs, extractStringList(connParams["args"])...)

	if timeout, ok := connParams["timeout"]; ok {
		cfg.Timeout = coerceIntTimeout(timeout, cfg.Timeout)
	}

	if envMap, ok := connParams["env"].(map[string]any); ok {
		cfg.EnvVars = stringMap(envMap)
	}
	return nil
}

func extractStdioCommand(connParams map[string]any, cfg *ToolsetConfig) string {
	if cmd, _ := connParams["command"].(string); cmd != "" {
		return cmd
	}
	cmdList, ok := connParams["command"].([]any)
	if !ok || len(cmdList) == 0 {
		return ""
	}
	cmd, _ := cmdList[0].(string)
	cfg.CommandArgs = append(cfg.CommandArgs, asStrings(cmdList[1:])...)
	return cmd
}

func parseHTTPConnection(cfg *ToolsetConfig, connParams map[string]any) error {
	url, _ := connParams["url"].(string)
	if url == "" {
		return fmt.Errorf("mcp %s connection missing required 'url'", cfg.ConnectionType)
	}
	cfg.URL = url

	if sslCfg, ok := connParams["ssl_config"].(map[string]any); ok {
		if verify, ok := sslCfg["verify"].(bool); ok {
			cfg.SSLVerify = &verify
		}
		if caBundle, ok := sslCfg["ca_bundle"].(string); ok {
			cfg.SSLCABundle = caBundle
		}
		if clientCert, ok := sslCfg["client_cert"].(string); ok {
			cfg.SSLClientCert = clientCert
		}
		if clientKey, ok := sslCfg["client_key"].(string); ok {
			cfg.SSLClientKey = clientKey
		}
		if serverName, ok := sslCfg["server_name"].(string); ok {
			cfg.SSLServerName = serverName
		}

		if (cfg.SSLClientCert != "") != (cfg.SSLClientKey != "") {
			return fmt.Errorf("mcp ssl_config: client_cert and client_key must be set together (got client_cert=%q, client_key=%q)",
				cfg.SSLClientCert, cfg.SSLClientKey)
		}
	}

	if v, ok := connParams["sse_read_timeout"]; ok {
		cfg.SSEReadTimeout = coerceIntTimeout(v, cfg.SSEReadTimeout)
	}

	if v, ok := connParams["terminate_on_close"].(bool); ok {
		cfg.TerminateOnClose = &v
	}

	if v, ok := connParams["max_response_bytes"]; ok {
		cfg.MaxResponseBytes = coerceInt64(v, cfg.MaxResponseBytes)
	}

	parseStaticHeaders(cfg, connParams)
	return nil
}

func coerceInt64(v any, defaultVal int64) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int64:
		return t
	case float64:
		return int64(t)
	default:
		return defaultVal
	}
}

func parseStaticHeaders(cfg *ToolsetConfig, connParams map[string]any) {
	headersRaw, _ := connParams["headers"].(map[string]any)
	authRaw, _ := connParams["auth"].(map[string]any)
	if len(headersRaw) == 0 && len(authRaw) == 0 {
		return
	}
	headers := make(map[string]string, len(headersRaw)+1)
	for k, v := range headersRaw {
		if s, ok := v.(string); ok {
			headers[k] = s
		}
	}
	if t, _ := authRaw["type"].(string); t == "basic" {
		user, _ := authRaw["username"].(string)
		pass, _ := authRaw["password"].(string)
		if user != "" || pass != "" {
			headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
		}
	}
	if len(headers) > 0 {
		cfg.Headers = headers
	}
}

func mergeTopLevelEnvVars(cfg *ToolsetConfig, raw map[string]any) {
	envMap, ok := raw["environment_variables"].(map[string]any)
	if !ok {
		return
	}
	if cfg.EnvVars == nil {
		cfg.EnvVars = make(map[string]string, len(envMap))
	}
	for k, v := range envMap {
		if s, ok := v.(string); ok {
			cfg.EnvVars[k] = s
		}
	}
}

func parseToolFiltering(cfg *ToolsetConfig, raw map[string]any) error {
	cfg.ToolName, _ = raw["tool_name"].(string)
	cfg.ToolNamePrefix, _ = raw["tool_name_prefix"].(string)
	cfg.AllowList = parseStringSlice(raw["allow_list"])
	cfg.DenyList = parseStringSlice(raw["deny_list"])

	filterCount := 0
	if cfg.ToolName != "" {
		filterCount++
	}
	if len(cfg.AllowList) > 0 {
		filterCount++
	}
	if len(cfg.DenyList) > 0 {
		filterCount++
	}
	if filterCount > 1 {
		return fmt.Errorf("mcp tool config: tool_name, allow_list, and deny_list are mutually exclusive")
	}
	return nil
}

func parseManifestSection(cfg *ToolsetConfig, raw map[string]any, loadManifest manifestFileLoader) error {
	manifestRaw, hasInline := raw["manifest"]
	manifestFile, _ := raw["manifest_file"].(string)

	if hasInline && manifestRaw == nil {
		hasInline = false
	}

	if hasInline && manifestFile != "" {
		return fmt.Errorf("mcp tool config: 'manifest' and 'manifest_file' are mutually exclusive")
	}

	switch {
	case hasInline:
		list, ok := manifestRaw.([]any)
		if !ok {
			return fmt.Errorf("mcp tool config: 'manifest' must be a list of tool entries, got %T", manifestRaw)
		}
		if len(list) == 0 {
			return nil
		}
		tools, err := parseManifestEntries(list)
		if err != nil {
			return fmt.Errorf("mcp tool config: manifest: %w", err)
		}
		cfg.Manifest = tools
	case manifestFile != "":
		tools, err := loadManifest(manifestFile)
		if err != nil {
			return fmt.Errorf("mcp tool config: %w", err)
		}
		if len(tools) == 0 {
			return nil
		}
		cfg.Manifest = tools
	}
	return nil
}

func coerceIntTimeout(v any, defaultVal int) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return defaultVal
	}
}

func extractStringList(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	return asStrings(list)
}

func asStrings(list []any) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

const bifrostAllToolsWildcard = "*"

func (cfg *ToolsetConfig) ToBifrostClientConfig() *schemas.MCPClientConfig {
	bc := &schemas.MCPClientConfig{
		ID:                     cfg.ClientID,
		Name:                   cfg.ClientID,
		NeedsSessionStickiness: new(true),
	}

	switch cfg.ConnectionType {
	case connTypeStdio:
		bc.ConnectionType = schemas.MCPConnectionTypeSTDIO
		envs := make([]string, 0, len(cfg.EnvVars))
		for k, v := range cfg.EnvVars {
			envs = append(envs, k+"="+v)
		}
		bc.StdioConfig = &schemas.MCPStdioConfig{
			Command: cfg.Command,
			Args:    cfg.CommandArgs,
			Envs:    envs,
		}
	case connTypeSSE:
		bc.ConnectionType = schemas.MCPConnectionTypeSSE
		bc.ConnectionString = schemas.NewSecretVar(cfg.URL)
	case connTypeStreamableHTTP:
		bc.ConnectionType = schemas.MCPConnectionTypeHTTP
		bc.ConnectionString = schemas.NewSecretVar(cfg.URL)
	}

	switch {
	case cfg.AuthType == "headers" || len(cfg.Headers) > 0:
		bc.AuthType = schemas.MCPAuthTypeHeaders
		if len(cfg.Headers) > 0 {
			bc.Headers = make(map[string]schemas.SecretVar, len(cfg.Headers))
			for k, v := range cfg.Headers {
				bc.Headers[k] = *schemas.NewSecretVar(v)
			}
		}
	default:
		bc.AuthType = schemas.MCPAuthTypeNone
	}

	bc.ToolsToExecute = []string{bifrostAllToolsWildcard}

	return bc
}

func (cfg *ToolsetConfig) EffectiveSSLVerify() bool {
	if cfg.SSLVerify != nil {
		return *cfg.SSLVerify
	}
	if cfg.ConnectionType != connTypeSSE && cfg.ConnectionType != connTypeStreamableHTTP {
		return true
	}
	return TLSVerifyFromEnv()
}

func (cfg *ToolsetConfig) HasSSLConfig() bool {
	return !cfg.EffectiveSSLVerify() ||
		cfg.SSLCABundle != "" ||
		cfg.SSLClientCert != "" ||
		cfg.SSLServerName != ""
}

func (cfg *ToolsetConfig) BuildHTTPClient() (*http.Client, error) {
	tlsCfg, err := tlsutil.BuildTLSConfig(tlsutil.TLSSettings{
		InsecureSkipVerify: !cfg.EffectiveSSLVerify(),
		CAFile:             cfg.SSLCABundle,
		ClientCertFile:     cfg.SSLClientCert,
		ClientKeyFile:      cfg.SSLClientKey,
		ServerName:         cfg.SSLServerName,
	}, os.ReadFile)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: tlsCfg,
	}}, nil
}

func (cfg *ToolsetConfig) ShouldIncludeTool(name string) bool {
	if cfg.ToolName != "" {
		return name == cfg.ToolName
	}
	if len(cfg.AllowList) > 0 {
		for _, allowed := range cfg.AllowList {
			if name == allowed {
				return true
			}
		}
		return false
	}
	if len(cfg.DenyList) > 0 {
		for _, denied := range cfg.DenyList {
			if name == denied {
				return false
			}
		}
	}
	return true
}

func defaultCredentialKeyBase(cfg *ToolsetConfig) string {
	switch cfg.ConnectionType {
	case connTypeStdio:
		parts := strings.Split(cfg.Command, "/")
		base := parts[len(parts)-1]
		return sanitizeName("mcp_" + base)
	case connTypeSSE, connTypeStreamableHTTP:
		base := cfg.URL
		base = strings.TrimPrefix(base, "https://")
		base = strings.TrimPrefix(base, "http://")
		visible, credential := splitCredentialMaterial(base)
		id := sanitizeName("mcp_" + visible)
		if credential != "" {
			id += "_" + credentialFingerprint(credential)
		}
		return id
	default:
		return "mcp_unknown"
	}
}

func (cfg *ToolsetConfig) ServerKey() string {
	return defaultCredentialKeyBase(cfg)
}

func splitCredentialMaterial(base string) (visible, credential string) {
	query := ""
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base, query = base[:i], base[i+1:]
	}
	base = strings.TrimRight(base, "/")

	authority, path := base, ""
	if i := strings.IndexByte(base, '/'); i >= 0 {
		authority, path = base[:i], base[i:]
	}

	userinfo := ""
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		userinfo, authority = authority[:i], authority[i+1:]
	}

	if userinfo == "" && query == "" {
		return authority + path, ""
	}
	return authority + path, userinfo + "\x00" + query
}

func credentialFingerprint(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:])[:8]
}

func sanitizeName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func parseAuthConfig(raw map[string]any, clientID string) *MCPAuthConfig {
	return toolauth.ParseAuthConfig(raw, clientID)
}

func parseStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
