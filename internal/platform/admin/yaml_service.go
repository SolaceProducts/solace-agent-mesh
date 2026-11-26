// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const connectorToolTimeoutSeconds = 120

const emailToolTimeoutSeconds = 30

type YAMLService struct {
	namespace       string
	modelConfigRepo *store.ModelConfigRepo
	systemUsers     systemUserIDLookup
	logger          *slog.Logger
}

type systemUserIDLookup interface {
	SystemUserID(ctx context.Context, issuer, sub string) (string, error)
}

func NewYAMLService(namespace string, logger *slog.Logger) *YAMLService {
	return &YAMLService{namespace: namespace, logger: logger}
}

func (s *YAMLService) SetModelConfigRepo(repo *store.ModelConfigRepo) {
	s.modelConfigRepo = repo
}

func (s *YAMLService) SetSystemUserLookup(lookup systemUserIDLookup) {
	s.systemUsers = lookup
}

func (s *YAMLService) resolveModelProviderAlias(agent *store.Agent) string {
	raw := resolveModelProvider(agent)
	if s.modelConfigRepo == nil || raw == "" {
		return raw
	}
	mc, err := s.modelConfigRepo.GetByAliasOrID(context.Background(), raw)
	if err != nil {
		s.logger.Warn("resolve model_provider alias: repo lookup failed; emitting raw value",
			"agentID", agent.ID, "modelProvider", raw, "error", err)
		return raw
	}
	if mc == nil {
		return raw
	}
	return mc.Alias
}

func (s *YAMLService) resolvedNamespace() string {
	return cmp.Or(s.namespace, "solace-agent-mesh")
}

type ConnectorConfig struct {
	ID          string
	Name        string
	Type        string
	Subtype     string
	Values      map[string]any
	ResourceKey string
}

type DeployOptions struct {
	TestMode           bool
	UserID             string
	SkillNames         []string
	SkillConfigsByName map[string]map[string]map[string]any
	PeerAllowList      []string
	DeploymentID       string
}

func (s *YAMLService) BuildAgentConfig(agent *store.Agent, toolsets, inputModes, outputModes []string, connectors []ConnectorConfig, opts ...DeployOptions) (map[string]any, error) {
	return s.buildAgentConfig(agent, toolsets, inputModes, outputModes, connectors, ToolEmission{}, opts...)
}

func buildToolConfigForFile(f store.ToolPackageFile, configValues map[string]any) map[string]any {
	if f.ConfigSchema == nil || *f.ConfigSchema == "" {
		return nil
	}
	var schema []map[string]any
	if err := json.Unmarshal([]byte(*f.ConfigSchema), &schema); err != nil || len(schema) == 0 {
		return nil
	}

	toolConfig := make(map[string]any, len(schema))
	for _, field := range schema {
		key, _ := field["key"].(string)
		if key == "" {
			continue
		}
		if configValues != nil {
			if v, ok := configValues[key]; ok {
				toolConfig[key] = v
				continue
			}
		}
		if def, ok := field["default"]; ok {
			toolConfig[key] = def
		}
	}
	if len(toolConfig) == 0 {
		return nil
	}
	return toolConfig
}

func buildToolAuthBlock(f store.ToolPackageFile, deployerAuth map[string]any) map[string]any {
	if f.Auth == nil || *f.Auth == "" {
		return nil
	}
	var decoded remotetool.AuthDeclaration
	if err := json.Unmarshal([]byte(*f.Auth), &decoded); err != nil || decoded.Type == "" {
		return nil
	}
	out := map[string]any{"type": decoded.Type}
	if dType, ok := deployerAuth["type"].(string); ok && dType != "" {
		out["type"] = dType
	}
	scheme := map[string]any{}
	if decoded.Scheme != nil {
		if decoded.Scheme.AuthorizationURL != "" {
			scheme["authorization_url"] = decoded.Scheme.AuthorizationURL
		}
		if decoded.Scheme.TokenURL != "" {
			scheme["token_url"] = decoded.Scheme.TokenURL
		}
		if len(decoded.Scheme.Scopes) > 0 {
			scheme["scopes"] = decoded.Scheme.Scopes
		}
		if decoded.Scheme.TokenEndpointAuthMethod != "" {
			scheme["token_endpoint_auth_method"] = decoded.Scheme.TokenEndpointAuthMethod
		}
	}

	if dScheme, ok := deployerAuth["scheme"].(map[string]any); ok {
		for k, v := range dScheme {
			if _, allowed := allowedAuthSchemeKeys[k]; !allowed || v == nil {
				continue
			}
			scheme[k] = v
		}
	}
	if len(scheme) > 0 && out["type"] == "oauth2" {
		out["scheme"] = scheme
	}

	if dCred, ok := deployerAuth["credential"].(map[string]any); ok {
		credential := map[string]any{}
		for k, v := range dCred {
			if _, allowed := allowedAuthCredentialKeys[k]; !allowed || v == nil {
				continue
			}
			credential[k] = v
		}
		if len(credential) > 0 {
			out["credential"] = credential
		}
	}
	return out
}

func deployerAuthFromConfig(configValues map[string]any) map[string]any {
	if configValues == nil {
		return nil
	}
	m, _ := configValues[reservedAuthConfigKey].(map[string]any)
	return m
}

func hilFromConfig(configValues map[string]any) map[string]any {
	if configValues == nil {
		return nil
	}
	m, _ := configValues[reservedHILConfigKey].(map[string]any)
	return m
}

type ToolEmission struct {
	Packages               map[string]ToolPackageInfo
	BuiltinToolConfigs     map[string]map[string]map[string]any
	BuiltinVolumeBindings  map[string]map[string]string
	BuiltinNetworkBindings map[string]map[string]string
	BuiltinExcludedTools   map[string][]string
	BuiltinToolHIL         map[string]map[string]any
}

func (s *YAMLService) BuildAgentConfigWithPackages(agent *store.Agent, toolsets, inputModes, outputModes []string, connectors []ConnectorConfig, packages map[string]ToolPackageInfo, opts ...DeployOptions) (map[string]any, error) {
	return s.buildAgentConfig(agent, toolsets, inputModes, outputModes, connectors, ToolEmission{Packages: packages}, opts...)
}

func (s *YAMLService) BuildAgentConfigFull(agent *store.Agent, toolsets, inputModes, outputModes []string, connectors []ConnectorConfig, emission ToolEmission, opts ...DeployOptions) (map[string]any, error) {
	return s.buildAgentConfig(agent, toolsets, inputModes, outputModes, connectors, emission, opts...)
}

func (s *YAMLService) agentLogger(agent *store.Agent) *slog.Logger {
	if s.logger == nil {
		return nil
	}
	return s.logger.With("agentID", agent.ID)
}

func (s *YAMLService) buildAgentConfig(agent *store.Agent, toolsets, inputModes, outputModes []string, connectors []ConnectorConfig, emission ToolEmission, opts ...DeployOptions) (map[string]any, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent is nil")
	}

	safeID := strings.ReplaceAll(agent.ID, "-", "_")
	name := AgentInstanceName(agent)

	emission = withResearchConnector(s.agentLogger(agent), toolsets, connectors, emission)
	tools := buildToolsListWithPackages(toolsets, connectors, emission)

	agentCard := buildAgentCard(agent, inputModes, outputModes)

	modelProvider := s.resolveModelProviderAlias(agent)
	appConfig := map[string]any{
		"namespace":             s.resolvedNamespace(),
		"agent_name":            name,
		"display_name":          agent.Name,
		"model_provider":        []string{modelProvider},
		"tools":                 tools,
		"session_service":       configbuilder.SessionBlock(safeID),
		"artifact_service":      configbuilder.ArtifactBlock(),
		"agent_card":            agentCard,
		"agent_card_publishing": map[string]any{"interval_seconds": 5},
		"agent_discovery":       map[string]any{"enabled": true},
	}

	if agent.SystemPrompt != nil && *agent.SystemPrompt != "" {
		appConfig["instruction"] = *agent.SystemPrompt
	}

	if agent.IsEphemeral {
		appConfig["ephemeral"] = true
		if agent.SourceComponentName != nil && *agent.SourceComponentName != "" {
			appConfig["display_name"] = *agent.SourceComponentName
		}
	}

	var opt DeployOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	var skillRefs []string
	if len(opt.SkillNames) > 0 {
		skillRefs = opt.SkillNames
	} else if agent.SkillRefs != nil && *agent.SkillRefs != "" {
		_ = json.Unmarshal([]byte(*agent.SkillRefs), &skillRefs)
	}
	if len(skillRefs) > 0 {
		appConfig["skills_base_path"] = cmp.Or(os.Getenv("SAM_SKILLS_DIR"), "")
		skillsList := make([]map[string]any, len(skillRefs))
		for i, ref := range skillRefs {
			entry := map[string]any{"name": ref}
			if tc, ok := opt.SkillConfigsByName[ref]; ok && len(tc) > 0 {
				toolConfig := make(map[string]any, len(tc))
				for toolName, fields := range tc {
					if len(fields) == 0 {
						continue
					}
					toolConfig[toolName] = fields
				}
				if len(toolConfig) > 0 {
					entry["tool_config"] = toolConfig
				}
			}
			skillsList[i] = entry
		}
		appConfig["skills"] = skillsList
	}
	if opt.TestMode && opt.UserID != "" {
		appConfig["required_scopes"] = []string{
			rbac.UserAgentInvokeScope(opt.UserID, name),
		}
	}

	if opt.DeploymentID != "" {
		appConfig["deployment"] = map[string]any{"id": opt.DeploymentID}
	}

	allowList := []string{}
	if len(opt.PeerAllowList) > 0 {
		allowList = opt.PeerAllowList
	}
	appConfig["inter_agent_communication"] = map[string]any{
		"allow_list": allowList,
	}

	mergeAdditionalConfigurations(s.logger, appConfig, agent)

	applyAutoInjectOverrides(s.logger, appConfig, toolsets, emission, agent.Name)

	if hasVolumeSlots(appConfig) {
		setDefaultVolumeServiceUnlessCallerSet(appConfig)
	}

	return configbuilder.AppConfig(name, appConfig), nil
}

const (
	volumeRootEnvVar  = "SAM_VOLUME_ROOT"
	defaultVolumeRoot = "/app/data/volumes"
)

func hasVolumeSlots(appConfig map[string]any) bool {
	switch v := appConfig["volumes"].(type) {
	case []any:
		return len(v) > 0
	case []map[string]any:
		return len(v) > 0
	default:
		return false
	}
}

func setDefaultVolumeServiceUnlessCallerSet(appConfig map[string]any) {
	if _, ok := appConfig["volume_service"]; ok {
		return
	}
	appConfig["volume_service"] = map[string]any{
		"type": "filesystem",
		"root": envOr(volumeRootEnvVar, defaultVolumeRoot),
	}
}

func mergeAdditionalConfigurations(logger *slog.Logger, appConfig map[string]any, agent *store.Agent) {
	if agent == nil {
		return
	}
	parsed, ok := parseAdditionalConfigurationsForMerge(logger, agent)
	if !ok {
		return
	}
	overlay := translateAgentExtraConfig(parsed, agentExtraConfigSchema, logger, agent.ID)
	if len(overlay) == 0 {
		return
	}
	preserved := make(map[string]any, len(forbiddenComputedRuntimeKeys))
	for key := range forbiddenComputedRuntimeKeys {
		if v, exists := appConfig[key]; exists {
			preserved[key] = v
		}
	}

	deepMergeMap(appConfig, overlay)

	for key := range forbiddenComputedRuntimeKeys {
		if v, exists := preserved[key]; exists {
			appConfig[key] = v
		} else {
			delete(appConfig, key)
		}
	}
}

func parseAdditionalConfigurationsForMerge(logger *slog.Logger, agent *store.Agent) (map[string]any, bool) {
	raw := agent.AdditionalConfigurations
	if len(raw) == 0 {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		if logger != nil {
			logger.Warn("agent additional_configurations is not valid JSON; ignoring",
				"agentID", agent.ID,
				"error", err)
		}
		return nil, false
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		if logger != nil {
			logger.Warn("agent additional_configurations is not a JSON object; ignoring",
				"agentID", agent.ID)
		}
		return nil, false
	}
	if len(obj) == 0 {
		return nil, false
	}
	return obj, true
}

func deepMergeMap(dst, src map[string]any) {
	for key, srcVal := range src {
		if dstVal, ok := dst[key]; ok {
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := srcVal.(map[string]any)
			if dstIsMap && srcIsMap {
				deepMergeMap(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
}

func (s *YAMLService) GenerateAgentYAML(agent *store.Agent, toolsets, inputModes, outputModes []string, connectors []ConnectorConfig, opts ...DeployOptions) (string, error) {
	config, err := s.BuildAgentConfig(agent, toolsets, inputModes, outputModes, connectors, opts...)
	if err != nil {
		return "", err
	}
	return configbuilder.MarshalYAML(config)
}

func (s *YAMLService) GenerateAgentYAMLWithPackages(agent *store.Agent, toolsets, inputModes, outputModes []string, connectors []ConnectorConfig, packages map[string]ToolPackageInfo, opts ...DeployOptions) (string, error) {
	config, err := s.BuildAgentConfigWithPackages(agent, toolsets, inputModes, outputModes, connectors, packages, opts...)
	if err != nil {
		return "", err
	}
	return configbuilder.MarshalYAML(config)
}

func (s *YAMLService) GenerateAgentYAMLFull(agent *store.Agent, toolsets, inputModes, outputModes []string, connectors []ConnectorConfig, emission ToolEmission, opts ...DeployOptions) (string, error) {
	config, err := s.BuildAgentConfigFull(agent, toolsets, inputModes, outputModes, connectors, emission, opts...)
	if err != nil {
		return "", err
	}
	return configbuilder.MarshalYAML(config)
}

func (s *YAMLService) BuildGatewayConfig(gw *store.Gateway) (map[string]any, error) {
	if gw == nil {
		return nil, fmt.Errorf("entrypoint is nil")
	}

	var values map[string]any
	if gw.Values != nil {
		if err := json.Unmarshal([]byte(*gw.Values), &values); err != nil {
			return nil, fmt.Errorf("parse gateway values: %w", err)
		}
	}

	var config map[string]any
	switch gw.Type {
	case "httpsse":
		config = s.buildHTTPSSEGatewayConfig(gw, values)
	case "teams":
		config = s.buildTeamsGatewayConfig(gw, values)
	case "slack":
		config = s.buildSlackGatewayConfig(gw, values)
	case "email":
		config = s.buildEmailGatewayConfig(gw, values)
	case "whatsapp":
		config = s.buildWhatsAppGatewayConfig(gw, values)
	case "event_mesh":
		config = s.buildEventMeshGatewayConfig(gw, values)
	case "mcp":
		config = s.buildMCPGatewayConfig(gw, values)
	case "webhook":
		config = s.buildWebhookGatewayConfig(gw, values)
	case "google_chat":
		config = s.buildGoogleChatGatewayConfig(gw, values)
	case platpkg.AgentLinkGatewayType:
		config = s.buildAgentLinkGatewayConfig(gw, values)
	default:
		return nil, fmt.Errorf("unsupported entrypoint type: %s", gw.Type)
	}
	apps, _ := config["apps"].([]map[string]any)
	for _, app := range apps {
		if appConfig, ok := app["app_config"].(map[string]any); ok {
			appConfig["gateway_uuid"] = gw.ID
			appConfig["scheduler_service"] = map[string]any{"enabled": false}
			s.renderRunAsUserUUIDs(gw.ID, appConfig)
		}
	}
	return config, nil
}

const runAsUserUUIDsKey = "run_as_user_uuids"

func (s *YAMLService) renderRunAsUserUUIDs(gatewayID string, appConfig map[string]any) {
	if s.systemUsers == nil {
		return
	}
	uuids := map[string]any{}
	for _, principal := range runAsPrincipals(appConfig) {
		if rbac.IsReservedSystemUserSub(principal) {
			continue
		}
		id, err := s.systemUsers.SystemUserID(context.Background(), rbac.SystemUserIssuer, principal)
		if err != nil {
			s.logger.Warn("run_as system user lookup failed", "gatewayID", gatewayID, "principal", principal, "error", err)
			continue
		}
		if id != "" {
			uuids[principal] = id
		}
	}
	if len(uuids) > 0 {
		appConfig[runAsUserUUIDsKey] = uuids
	}
}

func runAsPrincipals(appConfig map[string]any) []string {
	found := map[string]struct{}{}
	collectRunAsPrincipals(appConfig, found)
	return slices.Sorted(maps.Keys(found))
}

func collectRunAsPrincipals(node any, found map[string]struct{}) {
	switch v := node.(type) {
	case map[string]any:
		collectRunAsPrincipalsIn(v, found)
	case []map[string]any:
		for _, child := range v {
			collectRunAsPrincipalsIn(child, found)
		}
	case []any:
		for _, child := range v {
			collectRunAsPrincipals(child, found)
		}
	}
}

func collectRunAsPrincipalsIn(m map[string]any, found map[string]struct{}) {
	for key, child := range m {
		if name, ok := child.(string); ok && key == "run_as" {
			if name = strings.TrimSpace(name); name != "" {
				found[rbac.SystemUserPrincipal(name)] = struct{}{}
			}
			continue
		}
		collectRunAsPrincipals(child, found)
	}
}

func (s *YAMLService) GenerateGatewayYAML(gw *store.Gateway) (string, error) {
	config, err := s.BuildGatewayConfig(gw)
	if err != nil {
		return "", err
	}
	return configbuilder.MarshalYAML(config)
}

func gatewayWireID(gw *store.Gateway) string {
	return cmp.Or(gw.Slug, gw.ID)
}

func (s *YAMLService) gatewayAdapterAppConfig(gw *store.Gateway, safeID, adapterType string, adapterConfig map[string]any, values map[string]any) map[string]any {
	appConfig := map[string]any{
		"namespace":        s.resolvedNamespace(),
		"gateway_id":       gatewayWireID(gw),
		"gateway_adapter":  map[string]any{"type": adapterType, "adapter_config": adapterConfig},
		"model":            configbuilder.ModelBlock(),
		"session_service":  configbuilder.GatewaySessionBlock(safeID),
		"artifact_service": configbuilder.ArtifactBlock(),
		"agent_discovery":  map[string]any{"enabled": true},
	}
	applyAssistantContext(appConfig, values)
	applyClusterAuth(appConfig, values)
	return appConfig
}

func clusterAuthEnabled() bool {
	return strings.EqualFold(configbuilder.ResolveEnv("FRONTEND_USE_AUTHORIZATION", "false"), "true")
}

func entrypointAuthEnforce(values map[string]any) bool {
	if v, ok := boolFromValue(values["enableAuth"]); ok {
		return v
	}
	return clusterAuthEnabled()
}

func boolFromValue(v any) (val, ok bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func applyClusterAuth(appConfig, values map[string]any) {
	authn := entrypointAuthEnforce(values)
	if !authn && !clusterAuthEnabled() {
		return
	}
	if authn {
		appConfig["frontend_use_authorization"] = true
		appConfig["external_auth_provider"] = configbuilder.ResolveEnv("EXTERNAL_AUTH_PROVIDER", "azure")
	}
	appConfig["authorization_service"] = map[string]any{
		"type":                           configbuilder.ResolveEnv("AUTHORIZATION_TYPE", "default_rbac"),
		"role_to_scope_definitions_path": configbuilder.ResolveEnv("ROLE_DEFINITIONS_PATH", configbuilder.DefaultRoleDefinitionsPath),
		"user_to_role_assignments_path":  configbuilder.ResolveEnv("USER_ROLES_PATH", configbuilder.DefaultUserRolesPath),
	}
}

func applyAssistantContext(appConfig, values map[string]any) {
	setFromFirstPresentAlias(appConfig, "system_purpose", values, "systemPurpose", "system_purpose")
	setFromFirstPresentAlias(appConfig, "response_format", values, "responseFormat", "response_format")
}

func setFromFirstPresentAlias(m map[string]any, key string, values map[string]any, aliases ...string) {
	for _, alias := range aliases {
		s, isString := values[alias].(string)
		if !isString {
			continue
		}
		if strings.TrimSpace(s) != "" {
			m[key] = s
		}
		return
	}
}

func (s *YAMLService) buildHTTPSSEGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")

	appConfig := map[string]any{
		"namespace":        s.resolvedNamespace(),
		"gateway_id":       gatewayWireID(gw),
		"model":            configbuilder.ModelBlock(),
		"session_service":  configbuilder.GatewaySessionBlock(safeID),
		"artifact_service": configbuilder.ArtifactBlock(),
		"agent_discovery":  map[string]any{"enabled": true},
	}
	configbuilder.SetIfPresent(appConfig, "default_agent_name", values)
	applyAssistantContext(appConfig, values)
	applyClusterAuth(appConfig, values)

	return configbuilder.AppConfig(gw.ID, appConfig)
}

func (s *YAMLService) buildTeamsGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")
	envSuffix := strings.ToUpper(safeID)

	adapterConfig := map[string]any{}
	configbuilder.SetOrEnvVar(adapterConfig, "microsoft_app_id", values, "MICROSOFT_APP_ID_"+envSuffix)
	configbuilder.SetOrEnvVar(adapterConfig, "microsoft_app_password", values, "MICROSOFT_APP_PASSWORD_"+envSuffix)
	configbuilder.SetIfPresent(adapterConfig, "microsoft_app_tenant_id", values)
	configbuilder.SetIfPresent(adapterConfig, "default_agent_name", values)
	configbuilder.SetIfPresent(adapterConfig, "initial_status_message", values)
	configbuilder.SetIfPresent(adapterConfig, "enable_typing_indicator", values)
	configbuilder.SetIfPresent(adapterConfig, "feedback_enabled", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "run_as", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "use_user_identity_in_channels", values)
	adapterConfig["max_download_file_size_mb"] = configbuilder.GetIntOrDefault(values, "max_download_file_size_mb", 100)

	appConfig := s.gatewayAdapterAppConfig(gw, safeID, "teams", adapterConfig, values)
	configbuilder.SetIfPresent(appConfig, "feedback_publishing", values)

	return configbuilder.AppConfig(gw.ID, appConfig)
}

func (s *YAMLService) buildSlackGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")
	envSuffix := strings.ToUpper(safeID)

	adapterConfig := map[string]any{}
	configbuilder.SetOrEnvVarFrom(adapterConfig, "slack_bot_token", values, "bot_token", "SLACK_BOT_TOKEN_"+envSuffix)
	configbuilder.SetOrEnvVarFrom(adapterConfig, "slack_app_token", values, "app_token", "SLACK_APP_TOKEN_"+envSuffix)
	configbuilder.SetIfPresent(adapterConfig, "default_agent_name", values)
	configbuilder.SetIfPresent(adapterConfig, "feedback_enabled", values)
	configbuilder.SetIfPresent(adapterConfig, "show_activity_timeline", values)
	configbuilder.SetIfPresent(adapterConfig, "activity_display_mode", values)
	configbuilder.SetIfPresent(adapterConfig, "slack_thread_history_limit", values)
	configbuilder.SetIfPresent(adapterConfig, "slack_channel_history_limit", values)
	configbuilder.SetIfPresent(adapterConfig, "slack_history_max_attachments", values)
	configbuilder.SetIfPresent(adapterConfig, "slack_initial_status_message", values)
	configbuilder.SetIfPresent(adapterConfig, "correct_markdown_formatting", values)
	configbuilder.SetIfPresent(adapterConfig, "slack_email_cache_ttl_seconds", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "run_as", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "use_user_identity_in_channels", values)

	appConfig := s.gatewayAdapterAppConfig(gw, safeID, "slack", adapterConfig, values)
	configbuilder.SetIfPresent(appConfig, "feedback_publishing", values)

	return configbuilder.AppConfig(gw.ID, appConfig)
}

func (s *YAMLService) buildWhatsAppGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")
	envSuffix := strings.ToUpper(safeID)

	adapterConfig := map[string]any{}
	configbuilder.SetIfPresent(adapterConfig, "phone_number_id", values)
	configbuilder.SetOrEnvVar(adapterConfig, "access_token", values, "WHATSAPP_ACCESS_TOKEN_"+envSuffix)
	configbuilder.SetOrEnvVar(adapterConfig, "app_secret", values, "WHATSAPP_APP_SECRET_"+envSuffix)
	configbuilder.SetOrEnvVar(adapterConfig, "verify_token", values, "WHATSAPP_VERIFY_TOKEN_"+envSuffix)
	configbuilder.SetIfPresent(adapterConfig, "default_agent_name", values)

	configbuilder.SetKeyIfPresent(adapterConfig, "disable_file_uploads", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "run_as", values)
	configbuilder.SetIfPresent(adapterConfig, "identity_lookup_key", values)
	configbuilder.SetIfPresent(adapterConfig, "identity_claim_key", values)
	configbuilder.SetIfPresent(adapterConfig, "contact_request_template", values)
	configbuilder.SetIfPresent(adapterConfig, "contact_request_template_language", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "feedback_enabled", values)

	s.warnRetiredAllowlistStored(gw, values)

	appConfig := s.gatewayAdapterAppConfig(gw, safeID, "whatsapp", adapterConfig, values)
	configbuilder.SetIfPresent(appConfig, "feedback_publishing", values)

	return configbuilder.AppConfig(gw.ID, appConfig)
}

func (s *YAMLService) buildGoogleChatGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")
	envSuffix := strings.ToUpper(safeID)
	values = maps.Clone(values)
	maps.DeleteFunc(values, func(_ string, v any) bool { return v == nil })

	adapterConfig := map[string]any{}
	for _, key := range []string{
		"delivery",
		"pubsub_subscription",
		"project_number",
		"auth_audience_type",
		"endpoint_url",
		"addon_service_account_email",
		"default_agent_name",
		"allowed_email_domains",
		"min_edit_interval_ms",
		"initial_status_message",
		"thread_history_limit",
		"artifact_drive_folder_id",
		"artifact_drive_access_days",
	} {
		configbuilder.SetIfPresent(adapterConfig, key, values)
	}
	configbuilder.SetOrEnvVar(adapterConfig, "service_account_key", values, "GOOGLE_CHAT_SERVICE_ACCOUNT_KEY_"+envSuffix)
	if claimKey := strings.TrimSpace(stringOrDefault(values, "identity_claim_key", "")); claimKey != "" {
		adapterConfig["identity_claim_key"] = claimKey
	}
	configbuilder.SetKeyIfPresent(adapterConfig, "run_as", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "use_user_identity_in_channels", values)
	configbuilder.SetKeyIfPresent(adapterConfig, "feedback_enabled", values)
	delivery := googleChatDelivery(values)
	adapterConfig["delivery"] = delivery
	if googleChatEndpointUnset(values) && delivery == googlechat.DeliveryHTTPS {
		delete(adapterConfig, "endpoint_url")
		if derived := googleChatDerivedEndpointURL(gw); derived != "" {
			adapterConfig["endpoint_url"] = derived
		}
	}

	appConfig := s.gatewayAdapterAppConfig(gw, safeID, "google_chat", adapterConfig, values)
	configbuilder.SetIfPresent(appConfig, "feedback_publishing", values)
	return configbuilder.AppConfig(gw.ID, appConfig)
}

func googleChatEndpointURL(gw *store.Gateway) string {
	var values map[string]any
	if gw.Values != nil && json.Unmarshal([]byte(*gw.Values), &values) != nil {
		return ""
	}
	if googleChatDelivery(values) != googlechat.DeliveryHTTPS {
		return ""
	}
	return cmp.Or(strings.TrimSpace(asScalarString(values, "endpoint_url")), googleChatDerivedEndpointURL(gw))
}

func googleChatDerivedEndpointURL(gw *store.Gateway) string {
	base := googleChatExternalBase()
	if base == "" {
		return ""
	}
	return base + "/gw/" + gatewayWireID(gw) + "/events"
}

func googleChatExternalBase() string {
	return strings.TrimRight(configbuilder.ResolveEnv("WEBUI_FRONTEND_SERVER_URL", ""), "/")
}

func (s *YAMLService) warnRetiredAllowlistStored(gw *store.Gateway, values map[string]any) {
	var present []string
	for _, key := range whatsapp.RetiredAllowlistKeys {
		if _, ok := values[key]; ok {
			present = append(present, key)
		}
	}
	if len(present) == 0 {
		return
	}
	if s.logger == nil {
		return
	}
	s.logger.Warn("whatsapp entrypoint still stores the removed inbound sender allowlist, which no longer restricts anyone: every sender who can message this business number now reaches the entrypoint, so save the entrypoint to clear the keys and scope access with sender identity or run_as instead",
		"gatewayID", gw.ID, "gatewayName", gw.Name, "retiredKeys", strings.Join(present, ","))
}

func (s *YAMLService) buildEmailGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")
	envSuffix := strings.ToUpper(safeID)

	adapterConfig := map[string]any{}

	configbuilder.SetIfPresent(adapterConfig, "mailbox", values)
	configbuilder.SetIfPresent(adapterConfig, "imap_host", values)
	if port := configbuilder.GetIntOrDefault(values, "imap_port", defaultIMAPPort); port > 0 {
		adapterConfig["imap_port"] = port
	}
	configbuilder.SetIfPresent(adapterConfig, "smtp_host", values)
	if port := configbuilder.GetIntOrDefault(values, "smtp_port", 587); port > 0 {
		adapterConfig["smtp_port"] = port
	}

	authMode, _ := values["auth_mode"].(string)
	switch authMode {
	case "oauth2":
		configbuilder.SetIfPresent(adapterConfig, "oauth2_tenant_id", values)
		configbuilder.SetIfPresent(adapterConfig, "oauth2_client_id", values)
		configbuilder.SetOrEnvVarFrom(adapterConfig, "oauth2_client_secret", values, "oauth2_client_secret", "EMAIL_OAUTH2_CLIENT_SECRET_"+envSuffix)
	default:
		configbuilder.SetOrEnvVarFrom(adapterConfig, "imap_password", values, "imap_password", "EMAIL_IMAP_PASSWORD_"+envSuffix)
	}

	if rawIDs, ok := values["trusted_authserv_ids"].(string); ok {
		var ids []any
		for _, part := range strings.Split(rawIDs, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				ids = append(ids, trimmed)
			}
		}
		if len(ids) > 0 {
			adapterConfig["trusted_authserv_ids"] = ids
		}
	} else if ids, ok := values["trusted_authserv_ids"].([]any); ok {
		adapterConfig["trusted_authserv_ids"] = ids
	}
	configbuilder.SetIfPresent(adapterConfig, "policy_mode", values)
	configbuilder.SetIfPresent(adapterConfig, "delivery_mode", values)

	if v, ok := values["require_dmarc_for_identity"].(bool); ok {
		adapterConfig["require_dmarc_for_identity"] = v
	}

	configbuilder.SetIfPresent(adapterConfig, "trusted_internal_tenant_id", values)

	if raw, ok := values["allowed_senders"].(string); ok {
		var senders []any
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				senders = append(senders, trimmed)
			}
		}
		if len(senders) > 0 {
			adapterConfig["allowed_senders"] = senders
		}
	} else if senders, ok := values["allowed_senders"].([]any); ok {
		adapterConfig["allowed_senders"] = senders
	}

	for _, key := range []string{"mime_allowlist", "mime_denylist"} {
		if raw, ok := values[key].(string); ok {
			var entries []any
			for _, part := range strings.Split(raw, ",") {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					entries = append(entries, trimmed)
				}
			}
			if len(entries) > 0 {
				adapterConfig[key] = entries
			}
		} else if entries, ok := values[key].([]any); ok {
			adapterConfig[key] = entries
		}
	}

	if v := configbuilder.GetIntOrDefault(values, "poll_interval_seconds", 30); v > 0 {
		adapterConfig["poll_interval_seconds"] = v
	}
	if v := configbuilder.GetIntOrDefault(values, "max_message_size_mb", 25); v > 0 {
		adapterConfig["max_message_size_bytes"] = v * 1024 * 1024
	}
	if v := configbuilder.GetIntOrDefault(values, "max_attachment_size_mb", 10); v > 0 {
		adapterConfig["max_attachment_size_bytes"] = v * 1024 * 1024
	}

	configbuilder.SetIfPresent(adapterConfig, "default_agent_name", values)

	appConfig := s.gatewayAdapterAppConfig(gw, safeID, "email", adapterConfig, values)

	return configbuilder.AppConfig(gw.ID, appConfig)
}

func (s *YAMLService) buildEventMeshGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")
	envSuffix := strings.ToUpper(safeID)

	values = withoutDevBrokerPlaceholders(values)
	brokerURL, _ := values["broker_url"].(string)
	var emBroker map[string]any
	if strings.TrimSpace(brokerURL) != "" {
		emBroker = map[string]any{
			"broker_url":      stringOrResolvedEnv(values, "broker_url", "BROKER_URL_"+envSuffix),
			"broker_vpn":      stringOrResolvedEnv(values, "broker_vpn", "BROKER_VPN_"+envSuffix),
			"broker_username": stringOrResolvedEnv(values, "broker_username", "BROKER_USERNAME_"+envSuffix),
			"broker_password": stringOrResolvedEnv(values, "broker_password", "BROKER_PASSWORD_"+envSuffix),
		}
		if skipVerify, ok := values["tls_skip_verify"].(bool); ok && skipVerify {
			emBroker["tls_skip_verify"] = true
		} else if s, ok := values["tls_skip_verify"].(string); ok && s == "true" {
			emBroker["tls_skip_verify"] = true
		}
	} else {
		emBroker = map[string]any{
			"broker_url":      cmp.Or(os.Getenv("BROKER_URL_"+envSuffix), os.Getenv("SOLACE_BROKER_URL")),
			"broker_vpn":      cmp.Or(os.Getenv("BROKER_VPN_"+envSuffix), os.Getenv("SOLACE_BROKER_VPN")),
			"broker_username": cmp.Or(os.Getenv("BROKER_USERNAME_"+envSuffix), os.Getenv("SOLACE_BROKER_USERNAME")),
			"broker_password": stringOrResolvedEnv(values, "broker_password", "BROKER_PASSWORD_"+envSuffix),
		}
	}

	rules, _ := values["event_rules"].([]any)
	eventHandlers, outputHandlers := translateEventRules(rules)
	if eventHandlers == nil {
		eventHandlers = []map[string]any{}
	}
	if outputHandlers == nil {
		outputHandlers = []map[string]any{}
	}

	appConfig := map[string]any{
		"namespace":                s.resolvedNamespace(),
		"gateway_id":               gatewayWireID(gw),
		"gateway_type":             "event_mesh",
		"model":                    configbuilder.ModelBlock(),
		"session_service":          configbuilder.GatewaySessionBlock(safeID),
		"artifact_service":         configbuilder.ArtifactBlock(),
		"agent_discovery":          map[string]any{"enabled": true},
		"event_mesh_broker_config": emBroker,
		"event_handlers":           eventHandlers,
		"output_handlers":          outputHandlers,
	}
	applyAssistantContext(appConfig, values)
	applyClusterAuth(appConfig, values)

	return configbuilder.AppConfig(gw.ID, appConfig)
}

func (s *YAMLService) buildMCPGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")

	enableAuth := entrypointAuthEnforce(values)

	authProvider := configbuilder.ResolveEnv("EXTERNAL_AUTH_PROVIDER", "azure")
	externalBase := configbuilder.ResolveEnv("WEBUI_FRONTEND_SERVER_URL", "")

	adapterConfig := map[string]any{
		"transport":   stringOrDefault(values, "transport", "sse"),
		"enable_auth": enableAuth,
	}
	if externalBase != "" {
		adapterConfig["issuer"] = fmt.Sprintf("%s/gw/%s", strings.TrimRight(externalBase, "/"), gatewayWireID(gw))
	}

	if name, ok := values["serverName"].(string); ok && name != "" {
		adapterConfig["server_name"] = name
	}
	if desc, ok := values["serverDescription"].(string); ok && desc != "" {
		adapterConfig["server_description"] = desc
	}
	if v, ok := values["taskTimeoutSeconds"]; ok {
		adapterConfig["task_timeout_seconds"] = v
	}
	if v, ok := values["userIdClaim"].(string); ok && v != "" {
		adapterConfig["user_id_claim"] = v
	}
	if fileInput, _ := mcpBoolValue(values, "enableFileInput"); fileInput {
		adapterConfig["enable_file_input"] = true
	}

	if enableAuth {
		adapterConfig["external_auth_provider"] = authProvider
		adapterConfig["allowed_redirect_uris"] = flattenObjectArray(values, "allowedRedirectUris", "uri")

		dispatcherURL := mcpDispatcherURL(values)
		stateRedirection, explicit := mcpBoolValue(values, "stateRedirectionEnabled")
		if !explicit {
			stateRedirection = dispatcherURL != ""
		}
		switch {
		case stateRedirection && dispatcherURL != "":
			adapterConfig["state_redirection_enabled"] = true
			adapterConfig["oauth_dispatcher_url"] = dispatcherURL
		case stateRedirection && s.logger != nil:
			s.logger.Warn("mcp gateway state redirection requested but no dispatcher URL available; using legacy callback",
				"gatewayID", gatewayWireID(gw))
		}
	} else if ident, ok := values["defaultUserIdentity"].(string); ok && ident != "" {
		adapterConfig["default_user_identity"] = ident
	}

	if runAs, ok := values["runAs"].(string); ok && runAs != "" {
		adapterConfig["run_as"] = runAs
	}

	if includes := flattenObjectArray(values, "includeTools", "pattern"); len(includes) > 0 {
		adapterConfig["include_tools"] = includes
	}
	if excludes := flattenObjectArray(values, "excludeTools", "pattern"); len(excludes) > 0 {
		adapterConfig["exclude_tools"] = excludes
	}

	if origins := flattenObjectArray(values, "corsAllowedOrigins", "origin"); len(origins) > 0 {
		adapterConfig["cors_allowed_origins"] = origins
	}
	if v, ok := values["corsAllowedOriginRegex"].(string); ok && v != "" {
		adapterConfig["cors_allowed_origin_regex"] = v
	}

	appConfig := map[string]any{
		"namespace":        s.resolvedNamespace(),
		"gateway_id":       gatewayWireID(gw),
		"model":            configbuilder.ModelBlock(),
		"session_service":  configbuilder.GatewaySessionBlock(safeID),
		"artifact_service": configbuilder.ArtifactBlock(),
		"agent_discovery":  map[string]any{"enabled": true},
		"gateway_adapter": map[string]any{
			"type":           "mcp",
			"adapter_config": adapterConfig,
		},
	}

	applyAssistantContext(appConfig, values)
	applyClusterAuth(appConfig, values)

	return configbuilder.AppConfig(gw.ID, appConfig)
}

func stringOrDefault(values map[string]any, key, fallback string) string {
	if s, ok := values[key].(string); ok && s != "" {
		return s
	}
	return fallback
}

func stringOrAbsentDefault(values map[string]any, key, fallback string) string {
	if s, ok := values[key].(string); ok {
		return s
	}
	return fallback
}

func mcpDispatcherURL(values map[string]any) string {
	if u := strings.TrimSpace(stringOrDefault(values, "oauthDispatcherUrl", "")); u != "" {
		return u
	}
	if base := strings.TrimRight(strings.TrimSpace(configbuilder.ResolveEnv("WEBUI_FRONTEND_SERVER_URL", "")), "/"); base != "" {
		return base + "/api/v1/auth/callback"
	}
	return ""
}

func flattenObjectArray(values map[string]any, field, innerKey string) []any {
	raw, ok := values[field].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case string:
			if v != "" {
				out = append(out, v)
			}
		case map[string]any:
			if s, ok := v[innerKey].(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func buildPackageToolEntry(toolset string, f store.ToolPackageFile, pkgInfo ToolPackageInfo) map[string]any {
	cfgValues := pkgInfo.ConfigValues[f.ToolName]
	entry := map[string]any{
		"tool_type": "builtin",
		"tool_name": toolset + "__" + f.ToolName,
	}
	if toolCfg := buildToolConfigForFile(f, cfgValues); toolCfg != nil {
		entry["tool_config"] = toolCfg
	}
	if authBlock := buildToolAuthBlock(f, deployerAuthFromConfig(cfgValues)); authBlock != nil {
		entry["auth"] = authBlock
	}
	if hilBlock := hilFromConfig(cfgValues); len(hilBlock) > 0 {
		entry["hil"] = hilBlock
	}
	if scopes := requiredScopesFromConfig(cfgValues); len(scopes) > 0 {
		entry["required_scopes"] = scopes
	}
	return entry
}

func buildToolsListWithPackages(toolsets []string, connectors []ConnectorConfig, emission ToolEmission) []map[string]any {
	var tools []map[string]any
	for _, ts := range toolsets {
		if pkgInfo, ok := emission.Packages[ts]; ok {
			tools = append(tools, packageToolEntries(ts, pkgInfo)...)
			continue
		}
		tools = append(tools, builtinToolsetEntries(ts, emission)...)
	}
	for _, conn := range connectors {
		tools = append(tools, buildConnectorTools(conn)...)
	}
	if tools == nil {
		tools = []map[string]any{}
	}
	return tools
}

func packageToolEntries(toolset string, pkgInfo ToolPackageInfo) []map[string]any {
	excluded := toolNameSet(pkgInfo.ExcludedTools)
	entries := make([]map[string]any, 0, len(pkgInfo.ToolFiles))
	for i := range pkgInfo.ToolFiles {
		f := pkgInfo.ToolFiles[i]
		if _, skip := excluded[f.ToolName]; skip {
			continue
		}
		entries = append(entries, buildPackageToolEntry(toolset, f, pkgInfo))
	}
	return entries
}

func builtinToolsetEntries(toolset string, emission ToolEmission) []map[string]any {
	perTool := emission.BuiltinToolConfigs[toolset]
	vbinds := emission.BuiltinVolumeBindings[toolset]
	nbinds := emission.BuiltinNetworkBindings[toolset]
	gates := emission.BuiltinToolHIL[toolset]
	excluded := toolNameSet(emission.BuiltinExcludedTools[toolset])

	toolNames, isBuiltin := builtinToolsetTools[toolset]
	needsPerToolEntries := len(perTool) > 0 || len(gates) > 0 || len(vbinds) > 0 || len(nbinds) > 0 || len(excluded) > 0
	if !isBuiltin || !needsPerToolEntries {
		return []map[string]any{{"tool_type": "builtin-group", "group_name": toolset}}
	}

	entries := make([]map[string]any, 0, len(toolNames))
	for _, name := range toolNames {
		if _, skip := excluded[name]; skip {
			continue
		}
		entry := map[string]any{
			"tool_type": "builtin",
			"tool_name": name,
		}
		if cfg, ok := perTool[name]; ok && len(cfg) > 0 {
			entry["tool_config"] = cfg
		}
		if gate, gated := gates[name]; gated {
			entry["hil"] = gate
		}
		if vb := volumeBindingsYAML(vbinds); vb != nil {
			entry["volume_bindings"] = vb
		}
		if nb := networkBindingsForTool(name, nbinds); nb != nil {
			entry["network_bindings"] = nb
		}
		entries = append(entries, entry)
	}
	return entries
}

func toolNameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

var autoInjectGroupConfigKeys = map[string]string{
	"builtin_artifact_tools": "auto_inject_artifact_tools",
	"data_analysis":          "auto_inject_data_analysis_tools",
}

func applyAutoInjectOverrides(logger *slog.Logger, appConfig map[string]any, toolsets []string, emission ToolEmission, agentName string) {
	for key, value := range autoInjectOverrides(toolsets, emission) {
		if authored, ok := appConfig[key]; ok && authored != value && logger != nil {
			logger.Warn("overriding authored auto-injection flag: every tool in the toolset is excluded",
				"configKey", key, "authoredValue", authored, "agentName", agentName)
		}
		appConfig[key] = value
	}
}

func autoInjectOverrides(toolsets []string, emission ToolEmission) map[string]any {
	if len(emission.BuiltinExcludedTools) == 0 {
		return nil
	}
	var out map[string]any
	for _, ts := range toolsets {
		configKey, autoInjected := autoInjectGroupConfigKeys[schema.CanonicalBuiltinGroup(ts)]
		if !autoInjected {
			continue
		}
		members := builtinToolsetTools[ts]
		if len(members) == 0 {
			continue
		}
		excluded := make(map[string]struct{}, len(emission.BuiltinExcludedTools[ts]))
		for _, name := range emission.BuiltinExcludedTools[ts] {
			excluded[name] = struct{}{}
		}
		allExcluded := true
		for _, name := range members {
			if _, skip := excluded[name]; !skip {
				allExcluded = false
				break
			}
		}
		if !allExcluded {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(autoInjectGroupConfigKeys))
		}
		out[configKey] = false
	}
	return out
}

func volumeBindingsYAML(bindings map[string]string) map[string]any {
	if len(bindings) == 0 {
		return nil
	}
	out := make(map[string]any, len(bindings))
	for param, slot := range bindings {
		out[param] = slot
	}
	return out
}

func networkBindingsForTool(toolName string, bindings map[string]string) map[string]any {
	if len(bindings) == 0 {
		return nil
	}
	declared := builtinToolNetworkParams[toolName]
	if len(declared) == 0 {
		return nil
	}
	out := make(map[string]any, len(bindings))
	for param, policy := range bindings {
		if _, ok := declared[param]; ok {
			out[param] = policy
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildConnectorTools(conn ConnectorConfig) []map[string]any {
	entries := buildConnectorToolEntries(conn)

	for _, entry := range entries {
		if entry == nil {
			continue
		}
		entry["source_connector_category"] = conn.Type
		switch entry["tool_type"] {
		case "openapi", "mcp":
			entry["source_connector_id"] = conn.ID
		}
	}
	return entries
}

func connectorEnvID(connID string) string {
	return strings.ToUpper(strings.ReplaceAll(connID, "-", "_"))
}

func buildConnectorToolEntries(conn ConnectorConfig) []map[string]any {
	connUUID := connectorEnvID(conn.ID)

	switch conn.Type {
	case "sql":
		return []map[string]any{buildSQLConnectorTool(conn, connUUID)}
	case "mcp":
		return []map[string]any{buildMCPConnectorTool(conn, connUUID)}
	case "api":
		return []map[string]any{buildAPIConnectorTool(conn, connUUID)}
	case "knowledge_base":
		return []map[string]any{buildKnowledgeBaseConnectorTool(conn, connUUID)}
	case "document_db":
		return []map[string]any{buildDocumentDBConnectorTool(conn, connUUID)}
	case "graph_db":
		return []map[string]any{buildGraphDBConnectorTool(conn, connUUID)}
	case "search":
		return []map[string]any{buildSearchConnectorTool(conn, connUUID)}
	case webSearchConnectorType:
		return []map[string]any{buildWebSearchConnectorTool(conn, connUUID)}
	case "slack":
		return buildSlackConnectorTools(conn, connUUID)
	case "email":
		return []map[string]any{buildEmailConnectorTool(conn, connUUID)}
	case "event_mesh":
		return buildEventMeshConnectorTools(conn, connUUID)
	default:
		return nil
	}
}

func buildSQLConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	host := stringOrEnvVar(conn.Values, "hostname", "HOSTNAME_"+connUUID)
	port := stringOrEnvVar(conn.Values, "port", "PORT_"+connUUID)
	user := stringOrEnvVar(conn.Values, "username", "USERNAME_"+connUUID)
	pass := stringOrEnvVar(conn.Values, "password", "PASSWORD_"+connUUID)
	dbName := stringOrEnvVar(conn.Values, "database", "DATABASE_"+connUUID)

	driver := "postgresql"
	dialect := cmp.Or(conn.Subtype, "postgresql")
	switch conn.Subtype {
	case "mysql", "mariadb":
		driver = "mysql"
	case "mssql":
		driver = "sqlserver"
	case "oracle":
		driver = "oracle"
	}

	suffix := connectorSuffix(conn.ID)
	dbLiteral, _ := conn.Values["database"].(string)
	toolName := fmt.Sprintf("sql_query_%s", suffix)
	toolDescription := fmt.Sprintf("Query the %s database.", dialect)
	if dbLiteral != "" {
		toolName = fmt.Sprintf("%s_sql_query_%s", dbLiteral, suffix)
		toolDescription = fmt.Sprintf("Query the %s %s database.", dbLiteral, dialect)
	}

	if name, ok := conn.Values["tool_name"].(string); ok && name != "" {
		toolName = name
	}
	if desc, ok := conn.Values["tool_description"].(string); ok && desc != "" {
		toolDescription = desc
	}

	encodedUser := urlEncodeIfLiteral(user)
	encodedPass := urlEncodeIfLiteral(pass)

	var connStr string
	switch conn.Subtype {
	case "mssql":
		connStr = fmt.Sprintf("%s://%s:%s@%s:%s?database=%s", driver, encodedUser, encodedPass, host, port, dbName)
		encrypt, _ := conn.Values["encrypt"].(string)
		encrypt = cmp.Or(encrypt, "yes")
		connStr += "&encrypt=" + mssqlEncryptValue(encrypt)
		if encrypt == "yes" {
			if trustCert, _ := conn.Values["trust_server_certificate"].(string); trustCert == "yes" {
				connStr += "&TrustServerCertificate=true"
			}
		}
	case "oracle":
		serviceName := stringOrEnvVar(conn.Values, "service_name", "DATABASE_"+connUUID)
		connStr = fmt.Sprintf("%s://%s:%s@%s:%s/%s", driver, encodedUser, encodedPass, host, port, serviceName)
	default:
		connStr = fmt.Sprintf("%s://%s:%s@%s:%s/%s", driver, encodedUser, encodedPass, host, port, dbName)
	}

	return withConnectorHIL(map[string]any{
		"tool_type":       "builtin",
		"tool_name":       toolName,
		"timeout_seconds": connectorToolTimeoutSeconds,
		"tool_config": map[string]any{
			"connection_string": connStr,
			"tool_name":         toolName,
			"tool_description":  toolDescription,
			"_str_binary":       "execute_sql_query",
		},
	}, conn.Values, "sql_query")
}

func urlEncodeIfLiteral(s string) string {
	if strings.HasPrefix(s, "${") {
		return s
	}
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func stringOrEnvVar(values map[string]any, key, envVar string) string {
	if v, ok := values[key]; ok {
		switch val := v.(type) {
		case string:
			if val != "" {
				return val
			}
		case float64:
			return fmt.Sprintf("%d", int(val))
		}
	}
	return fmt.Sprintf("${%s}", envVar)
}

func stringOrResolvedEnv(values map[string]any, key, envVar string) string {
	if v, ok := values[key]; ok {
		switch val := v.(type) {
		case string:
			if val != "" {
				return val
			}
		case float64:
			return fmt.Sprintf("%d", int(val))
		}
	}
	return os.Getenv(envVar)
}

func buildMCPConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	serverURL := stringOrEnvVar(conn.Values, "server_url", "SERVER_URL_"+connUUID)

	ct, _ := conn.Values["connection_type"].(string)
	connType := cmp.Or(ct, "streamable-http")

	params := map[string]any{
		"type": connType,
		"url":  serverURL,
	}
	if ssl := deployableMCPSSLConfig(conn); ssl != nil {
		params[mcpSSLConfigKey] = ssl
	}

	headers := map[string]string{}

	authType, _ := conn.Values["auth_type"].(string)
	if authType != "" && authType != "none" {
		switch authType {
		case "http":
			scheme, _ := conn.Values["auth_http_scheme"].(string)
			switch scheme {
			case "bearer":
				token := stringOrEnvVar(conn.Values, "auth_http_bearer_token", "BEARER_TOKEN_"+connUUID)
				headers["Authorization"] = "Bearer " + token
			case "basic":
				user := stringOrEnvVar(conn.Values, "auth_http_basic_username", "BASIC_USERNAME_"+connUUID)
				pass := stringOrEnvVar(conn.Values, "auth_http_basic_password", "BASIC_PASSWORD_"+connUUID)
				isEnvRef := strings.HasPrefix(user, "${") || strings.HasPrefix(pass, "${")
				if isEnvRef {
					params["auth"] = map[string]any{
						"type":     "basic",
						"username": user,
						"password": pass,
					}
				} else {
					headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
				}
			}
		case "apikey":
			location, _ := conn.Values["auth_apikey_location"].(string)
			name := stringOrEnvVar(conn.Values, "auth_apikey_name", "API_KEY_NAME_"+connUUID)
			val := stringOrEnvVar(conn.Values, "auth_apikey_value", "API_KEY_VALUE_"+connUUID)
			if location == "query" {
				params["url"] = serverURL + "?" + name + "=" + val
			} else {
				headers[name] = val
			}
		case "oauth":
			result := map[string]any{
				"tool_type":         "mcp",
				"connection_params": params,
				"auth":              buildOAuthBlock(conn.Values, connUUID),
			}
			mergeMCPFilterAndManifest(result, conn.Values)
			mergeCustomHeaders(conn.Values, params)
			return result
		}
	}

	if customHeaders, ok := conn.Values["custom_headers"].(map[string]any); ok {
		for k, v := range customHeaders {
			if s, ok := v.(string); ok {
				headers[k] = s
			}
		}
	}

	if len(headers) > 0 {
		params["headers"] = headers
	}

	result := map[string]any{
		"tool_type":         "mcp",
		"connection_params": params,
	}

	mergeMCPFilterAndManifest(result, conn.Values)

	return result
}

func mergeMCPFilterAndManifest(result, values map[string]any) {
	if v, ok := values["tool_name"].(string); ok && v != "" {
		result["tool_name"] = v
	}
	if v, ok := values["tool_name_prefix"].(string); ok && v != "" {
		result["tool_name_prefix"] = v
	}
	if list := splitMCPNameList(values["allow_list"]); len(list) > 0 {
		result["allow_list"] = list
	}
	if list := splitMCPNameList(values["deny_list"]); len(list) > 0 {
		result["deny_list"] = list
	}
	if manifest, ok := values["manifest"]; ok && manifest != nil {
		result["manifest"] = filterManifestBySelection(manifest, values["selected_tools"])
	}
	if hil := normalizeConnectorHIL(values["hil"]); hil != nil {
		result["hil"] = hil
	}
}

func filterManifestBySelection(manifestRaw, selectedRaw any) any {
	selected := parseMCPSelectedTools(map[string]any{"selected_tools": selectedRaw})
	if len(selected) == 0 {
		return manifestRaw
	}
	entries := parseMCPManifest(map[string]any{"manifest": manifestRaw})
	if entries == nil {
		return manifestRaw
	}
	byName := make(map[string]map[string]any, len(entries))
	for _, entry := range entries {
		if name, _ := entry["name"].(string); name != "" {
			byName[name] = entry
		}
	}
	filtered := make([]map[string]any, 0, len(selected))
	for _, name := range selected {
		if entry, ok := byName[name]; ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func normalizeConnectorHIL(raw any) map[string]any {
	switch v := raw.(type) {
	case nil:
		return nil
	case map[string]any:
		if len(v) == 0 {
			return nil
		}
		return v
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(s), &parsed); err != nil {
			return nil
		}
		if len(parsed) == 0 {
			return nil
		}
		return parsed
	default:
		return nil
	}
}

func splitMCPNameList(raw any) []string {
	switch v := raw.(type) {
	case string:
		var out []string
		for name := range strings.SplitSeq(v, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, name := range v {
			name = strings.TrimSpace(name)
			if name != "" {
				out = append(out, name)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, entry := range v {
			if s, ok := entry.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	}
	return nil
}

func resolveSpecSource(conn ConnectorConfig) string {
	if conn.ResourceKey != "" {
		return "objstore://" + conn.ResourceKey
	}
	if u, _ := conn.Values["specification_url"].(string); isExternalSpecURL(u) {
		return u
	}
	slog.Default().Error("openapi connector has no spec resource", "connectorId", conn.ID)
	return "ERROR"
}

func isExternalSpecURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func buildAPIConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	specURL := resolveSpecSource(conn)

	tool := map[string]any{
		"tool_type":         "openapi",
		"specification_url": specURL,
		"base_url":          stringOrEnvVar(conn.Values, "base_url", "BASE_URL_"+connUUID),
	}

	if auth := buildOpenAPIAuthentication(conn, connUUID); auth != nil {
		tool["auth"] = auth
	}

	if customHeaders, ok := conn.Values["custom_headers"].(map[string]any); ok && len(customHeaders) > 0 {
		tool["headers"] = customHeaders
	}

	if allowList, ok := conn.Values["allow_list"].(string); ok && allowList != "" {
		var ops []string
		for op := range strings.SplitSeq(allowList, ",") {
			op = strings.TrimSpace(op)
			if op != "" {
				ops = append(ops, op)
			}
		}
		if len(ops) > 0 {
			tool["allow_list"] = ops
		}
	}

	if hil := normalizeConnectorHIL(conn.Values["hil"]); hil != nil {
		tool["hil"] = hil
	}

	return tool
}

func buildOpenAPIAuthentication(conn ConnectorConfig, connUUID string) map[string]any {
	values := conn.Values
	authType, _ := values["auth_type"].(string)
	if authType == "" || authType == "none" {
		return nil
	}
	switch authType {
	case "oauth":
		return buildOpenAPIOAuth(conn, connUUID)
	case "http":
		scheme, _ := values["auth_http_scheme"].(string)
		switch scheme {
		case "bearer":
			return map[string]any{
				"type":  "bearer",
				"token": stringOrEnvVar(values, "auth_http_bearer_token", "BEARER_TOKEN_"+connUUID),
			}
		case "basic":
			return map[string]any{
				"type":     "basic",
				"username": stringOrEnvVar(values, "auth_http_basic_username", "BASIC_USERNAME_"+connUUID),
				"password": stringOrEnvVar(values, "auth_http_basic_password", "BASIC_PASSWORD_"+connUUID),
			}
		}
		return nil
	case "apikey":
		location, _ := values["auth_apikey_location"].(string)
		if location == "" {
			location = "header"
		}
		return map[string]any{
			"type":  "apikey",
			"in":    location,
			"name":  stringOrEnvVar(values, "auth_apikey_name", "API_KEY_NAME_"+connUUID),
			"value": stringOrEnvVar(values, "auth_apikey_value", "API_KEY_VALUE_"+connUUID),
		}
	}
	return nil
}

func buildOpenAPIOAuth(conn ConnectorConfig, connUUID string) map[string]any {
	values := conn.Values
	result := map[string]any{
		"type":              "oauth2",
		"authorization_url": stringOrEnvVar(values, "auth_oauth_authorization_url", "AUTH_OAUTH_AUTHORIZATION_URL_"+connUUID),
		"token_url":         stringOrEnvVar(values, "auth_oauth_token_url", "AUTH_OAUTH_TOKEN_URL_"+connUUID),
		"client_id":         stringOrEnvVar(values, "auth_oauth_client_id", "AUTH_OAUTH_CLIENT_ID_"+connUUID),
		"credential_key":    openAPIConnectorCredentialKey(conn.ID),
	}

	if name := strings.TrimSpace(conn.Name); name != "" {
		result["display_name"] = name
	}

	if v, ok := values["auth_oauth_client_secret"].(string); ok && v != "" {
		result["client_secret"] = stringOrEnvVar(values, "auth_oauth_client_secret", "AUTH_OAUTH_CLIENT_SECRET_"+connUUID)
	}

	if v, ok := values["auth_oauth_refresh_url"].(string); ok && v != "" {
		result["refresh_url"] = stringOrEnvVar(values, "auth_oauth_refresh_url", "AUTH_OAUTH_REFRESH_URL_"+connUUID)
	}

	if scopes, ok := values["auth_oauth_scopes"].(string); ok && scopes != "" {
		result["scopes"] = strings.Fields(scopes)
	}

	if method, _ := values["auth_oauth_token_endpoint_auth_method"].(string); method != "" {
		result["token_endpoint_auth_method"] = method
	}

	if openAPIConnectorSendsPKCE(values) {
		result["use_pkce"] = true
	}

	return result
}

const (
	openAPIPKCEField    = "auth_oauth_pkce"
	openAPIPKCEDisabled = "disabled"
	publicClientMethod  = "none"
)

func openAPIConnectorSendsPKCE(values map[string]any) bool {
	setting, _ := values[openAPIPKCEField].(string)
	method, _ := values["auth_oauth_token_endpoint_auth_method"].(string)
	return setting != openAPIPKCEDisabled || method == publicClientMethod
}

func openAPIConnectorCredentialKey(connectorID string) string {
	return "openapi_connector_" + connectorID
}

func buildKnowledgeBaseConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	toolName := fmt.Sprintf("query_bedrock_kb_%s", connectorSuffix(conn.ID))

	toolConfig := map[string]any{
		"tool_name":        toolName,
		"tool_description": "Search Amazon Bedrock Knowledge Base for relevant documents",
		"kb_id":            stringOrEnvVar(conn.Values, "kb_id", "KB_ID_"+connUUID),
		"region":           stringOrEnvVar(conn.Values, "region", "REGION_"+connUUID),
		"max_results":      10,
	}

	authType, _ := conn.Values["auth_type"].(string)
	switch authType {
	case "iam":
		accountID := stringOrEnvVar(conn.Values, "aws_account_id", "AWS_ACCOUNT_ID_"+connUUID)
		roleName := stringOrEnvVar(conn.Values, "role_name", "ROLE_NAME_"+connUUID)
		if !strings.HasPrefix(accountID, "${") && !strings.HasPrefix(roleName, "${") {
			toolConfig["role_arn"] = fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)
		} else {
			toolConfig["role_arn"] = fmt.Sprintf("${ROLE_ARN_%s}", connUUID)
		}
		if v, ok := conn.Values["external_id"].(string); ok && v != "" {
			toolConfig["external_id"] = stringOrEnvVar(conn.Values, "external_id", "EXTERNAL_ID_"+connUUID)
		}
		if v, ok := conn.Values["session_name"].(string); ok && v != "" {
			toolConfig["session_name"] = stringOrEnvVar(conn.Values, "session_name", "SESSION_NAME_"+connUUID)
		}
	default:
		toolConfig["aws_access_key_id"] = stringOrEnvVar(conn.Values, "aws_access_key_id", "AWS_ACCESS_KEY_ID_"+connUUID)
		toolConfig["aws_secret_access_key"] = stringOrEnvVar(conn.Values, "aws_secret_access_key", "AWS_SECRET_ACCESS_KEY_"+connUUID)
	}

	toolConfig["_str_binary"] = "query_bedrock_kb"

	return withConnectorHIL(map[string]any{
		"tool_type":       "builtin",
		"tool_name":       toolName,
		"timeout_seconds": connectorToolTimeoutSeconds,
		"tool_config":     toolConfig,
	}, conn.Values, "query_bedrock_kb")
}

func buildDocumentDBConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	suffix := connectorSuffix(conn.ID)
	subtype := strings.ToLower(conn.Subtype)

	switch subtype {
	case "mongodb":
		return buildMongoDBConnectorTool(conn, connUUID, suffix)
	case "dynamodb":
		return buildDynamoDBConnectorTool(conn, connUUID, suffix)
	default:
		return nil
	}
}

func connectorScheme(values map[string]any, def string) string {
	if s, ok := values["scheme"].(string); ok && s != "" {
		return s
	}
	return def
}

func connectorPort(values map[string]any, def string) string {
	switch v := values["port"].(type) {
	case string:
		if v != "" {
			return v
		}
	case float64:
		return fmt.Sprintf("%d", int(v))
	case int:
		return fmt.Sprintf("%d", v)
	}
	return def
}

func hostSchemeURL(values map[string]any, connUUID, defScheme, defPort string) string {
	scheme := connectorScheme(values, defScheme)
	host := stringOrEnvVar(values, "hostname", "HOSTNAME_"+connUUID)
	if port := connectorPort(values, defPort); port != "" {
		return fmt.Sprintf("%s://%s:%s", scheme, host, port)
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func connectionStringOrHostURL(values map[string]any, connUUID, defScheme, defPort string) string {
	if cs, ok := values["connection_string"].(string); ok && cs != "" {
		return cs
	}
	return hostSchemeURL(values, connUUID, defScheme, defPort)
}

func mongoConnectionString(values map[string]any, connUUID string) string {
	if cs, ok := values["connection_string"].(string); ok && cs != "" {
		return cs
	}
	scheme := connectorScheme(values, "mongodb")
	user, _ := values["username"].(string)
	pass, _ := values["password"].(string)
	host := stringOrEnvVar(values, "hostname", "HOSTNAME_"+connUUID)

	var b strings.Builder
	b.WriteString(scheme)
	b.WriteString("://")
	if user != "" {
		b.WriteString(urlEncodeIfLiteral(user))
		if pass != "" {
			b.WriteString(":")
			b.WriteString(urlEncodeIfLiteral(pass))
		}
		b.WriteString("@")
	}
	b.WriteString(host)
	if scheme != "mongodb+srv" {
		if port := connectorPort(values, "27017"); port != "" {
			b.WriteString(":")
			b.WriteString(port)
		}
	}
	b.WriteString("/")
	if db, _ := values["database"].(string); db != "" {
		b.WriteString(db)
	}
	if opts, _ := values["options"].(string); opts != "" {
		b.WriteString("?")
		b.WriteString(opts)
	}
	return b.String()
}

func buildMongoDBConnectorTool(conn ConnectorConfig, connUUID, suffix string) map[string]any {
	toolName, toolDescription := resolveBuiltinConnectorNaming(conn.Values, builtinConnectorNaming{
		suffix:         suffix,
		baseVerb:       "mongo_query",
		literalKey:     "collection",
		defaultDesc:    "Query a MongoDB collection using aggregation pipelines.",
		literalDescFmt: "Query the %s MongoDB collection using aggregation pipelines.",
	})

	toolConfig := map[string]any{
		"backend":           "mongodb",
		"connection_string": mongoConnectionString(conn.Values, connUUID),
		"collection":        stringOrEnvVar(conn.Values, "collection", "COLLECTION_"+connUUID),
		"tool_name":         toolName,
		"tool_description":  toolDescription,
		"_str_binary":       "document_db_query",
	}
	if database, _ := conn.Values["database"].(string); database != "" {
		toolConfig["database"] = database
	}

	return withConnectorHIL(wrapBuiltinConnectorTool(toolName, toolConfig), conn.Values, "mongo_query")
}

func buildDynamoDBConnectorTool(conn ConnectorConfig, connUUID, suffix string) map[string]any {
	toolName, toolDescription := resolveBuiltinConnectorNaming(conn.Values, builtinConnectorNaming{
		suffix:         suffix,
		baseVerb:       "dynamodb_query",
		literalKey:     "table_name",
		defaultDesc:    "Query an Amazon DynamoDB table using PartiQL.",
		literalDescFmt: "Query the %s DynamoDB table using PartiQL.",
	})

	toolConfig := map[string]any{
		"backend":          "dynamodb",
		"region":           stringOrEnvVar(conn.Values, "region", "REGION_"+connUUID),
		"table_name":       stringOrEnvVar(conn.Values, "table_name", "TABLE_NAME_"+connUUID),
		"tool_name":        toolName,
		"tool_description": toolDescription,
		"_str_binary":      "document_db_query",
	}
	applyAWSAuth(toolConfig, conn.Values, connUUID)

	return withConnectorHIL(wrapBuiltinConnectorTool(toolName, toolConfig), conn.Values, "dynamodb_query")
}

func buildGraphDBConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	suffix := connectorSuffix(conn.ID)
	subtype := strings.ToLower(conn.Subtype)

	switch subtype {
	case "neo4j":
		return buildNeo4jConnectorTool(conn, connUUID, suffix)
	case "neptune":
		return buildNeptuneConnectorTool(conn, connUUID, suffix)
	default:
		return nil
	}
}

func buildNeo4jConnectorTool(conn ConnectorConfig, connUUID, suffix string) map[string]any {
	toolName, toolDescription := resolveBuiltinConnectorNaming(conn.Values, builtinConnectorNaming{
		suffix:         suffix,
		baseVerb:       "neo4j_query",
		literalKey:     "database",
		defaultDesc:    "Query a Neo4j graph database with Cypher.",
		literalDescFmt: "Query the %s Neo4j graph database with Cypher.",
	})

	toolConfig := map[string]any{
		"backend":           "neo4j",
		"connection_string": connectionStringOrHostURL(conn.Values, connUUID, "neo4j", "7687"),
		"username":          stringOrEnvVar(conn.Values, "username", "USERNAME_"+connUUID),
		"password":          stringOrEnvVar(conn.Values, "password", "PASSWORD_"+connUUID),
		"tool_name":         toolName,
		"tool_description":  toolDescription,
		"_str_binary":       "graph_db_query",
	}
	if dbLiteral, _ := conn.Values["database"].(string); dbLiteral != "" {
		toolConfig["database"] = dbLiteral
	}

	return withConnectorHIL(wrapBuiltinConnectorTool(toolName, toolConfig), conn.Values, "neo4j_query")
}

func buildNeptuneConnectorTool(conn ConnectorConfig, connUUID, suffix string) map[string]any {
	toolName, toolDescription := resolveBuiltinConnectorNaming(conn.Values, builtinConnectorNaming{
		suffix:         suffix,
		baseVerb:       "neptune_query",
		literalKey:     "database",
		defaultDesc:    "Query an Amazon Neptune graph database with openCypher.",
		literalDescFmt: "Query the %s Amazon Neptune graph database with openCypher.",
	})

	toolConfig := map[string]any{
		"backend":           "neptune",
		"connection_string": connectionStringOrHostURL(conn.Values, connUUID, "bolt+s", "8182"),
		"region":            stringOrEnvVar(conn.Values, "region", "REGION_"+connUUID),
		"tool_name":         toolName,
		"tool_description":  toolDescription,
		"_str_binary":       "graph_db_query",
	}
	if dbLiteral, _ := conn.Values["database"].(string); dbLiteral != "" {
		toolConfig["database"] = dbLiteral
	}
	applyAWSAuth(toolConfig, conn.Values, connUUID)

	return withConnectorHIL(wrapBuiltinConnectorTool(toolName, toolConfig), conn.Values, "neptune_query")
}

func buildSearchConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	suffix := connectorSuffix(conn.ID)
	subtype := strings.ToLower(conn.Subtype)

	switch subtype {
	case "elasticsearch":
		return buildElasticsearchConnectorTool(conn, connUUID, suffix)
	case "opensearch":
		return buildOpenSearchConnectorTool(conn, connUUID, suffix)
	default:
		return nil
	}
}

func buildElasticsearchConnectorTool(conn ConnectorConfig, connUUID, suffix string) map[string]any {
	toolName, toolDescription := resolveBuiltinConnectorNaming(conn.Values, builtinConnectorNaming{
		suffix:      suffix,
		baseVerb:    "elasticsearch_query",
		defaultDesc: "Query an Elasticsearch cluster with the Query DSL.",
	})

	toolConfig := map[string]any{
		"backend":          "elasticsearch",
		"tool_name":        toolName,
		"tool_description": toolDescription,
		"_str_binary":      "search_query",
	}
	if cs := elasticsearchConnectionURL(conn.Values, connUUID); cs != "" {
		toolConfig["connection_string"] = cs
	}
	if v, ok := conn.Values["cloud_id"].(string); ok && v != "" {
		toolConfig["cloud_id"] = v
	}
	if v, ok := conn.Values["api_key"].(string); ok && v != "" {
		toolConfig["api_key"] = stringOrEnvVar(conn.Values, "api_key", "API_KEY_"+connUUID)
	}
	if user, ok := conn.Values["username"].(string); ok && user != "" {
		toolConfig["username"] = user
		toolConfig["password"] = stringOrEnvVar(conn.Values, "password", "PASSWORD_"+connUUID)
	}

	return withConnectorHIL(wrapBuiltinConnectorTool(toolName, toolConfig), conn.Values, "elasticsearch_query")
}

func buildOpenSearchConnectorTool(conn ConnectorConfig, connUUID, suffix string) map[string]any {
	toolName, toolDescription := resolveBuiltinConnectorNaming(conn.Values, builtinConnectorNaming{
		suffix:      suffix,
		baseVerb:    "opensearch_query",
		defaultDesc: "Query an Amazon-managed OpenSearch cluster with the Query DSL.",
	})

	toolConfig := map[string]any{
		"backend":           "opensearch",
		"connection_string": connectionStringOrHostURL(conn.Values, connUUID, "https", ""),
		"region":            stringOrEnvVar(conn.Values, "region", "REGION_"+connUUID),
		"tool_name":         toolName,
		"tool_description":  toolDescription,
		"_str_binary":       "search_query",
	}
	applyAWSAuth(toolConfig, conn.Values, connUUID)

	return withConnectorHIL(wrapBuiltinConnectorTool(toolName, toolConfig), conn.Values, "opensearch_query")
}

func elasticsearchConnectionURL(values map[string]any, connUUID string) string {
	if cs, ok := values["connection_string"].(string); ok && cs != "" {
		return cs
	}
	if host, ok := values["hostname"].(string); !ok || host == "" {
		return ""
	}
	return hostSchemeURL(values, connUUID, "https", "9200")
}

type builtinConnectorNaming struct {
	suffix         string
	baseVerb       string
	literalKey     string
	defaultDesc    string
	literalDescFmt string
}

func resolveBuiltinConnectorNaming(values map[string]any, n builtinConnectorNaming) (string, string) {
	toolName := fmt.Sprintf("%s_%s", n.baseVerb, n.suffix)
	toolDescription := n.defaultDesc
	if n.literalKey != "" {
		if literal, _ := values[n.literalKey].(string); literal != "" {
			toolName = fmt.Sprintf("%s_%s_%s", sanitizeForBrokerTopic(literal), n.baseVerb, n.suffix)
			if n.literalDescFmt != "" {
				toolDescription = fmt.Sprintf(n.literalDescFmt, literal)
			}
		}
	}
	if v, ok := values["tool_name"].(string); ok && v != "" {
		toolName = v
	}
	if v, ok := values["tool_description"].(string); ok && v != "" {
		toolDescription = v
	}
	return toolName, toolDescription
}

func wrapBuiltinConnectorTool(toolName string, toolConfig map[string]any) map[string]any {
	return map[string]any{
		"tool_type":       "builtin",
		"tool_name":       toolName,
		"timeout_seconds": connectorToolTimeoutSeconds,
		"tool_config":     toolConfig,
	}
}

func applyAWSAuth(toolConfig, values map[string]any, connUUID string) {
	authType, _ := values["auth_type"].(string)
	switch authType {
	case "iam":
		toolConfig["role_arn"] = stringOrEnvVar(values, "role_arn", "ROLE_ARN_"+connUUID)
		if v, ok := values["session_name"].(string); ok && v != "" {
			toolConfig["session_name"] = stringOrEnvVar(values, "session_name", "SESSION_NAME_"+connUUID)
		}
		if v, ok := values["external_id"].(string); ok && v != "" {
			toolConfig["external_id"] = stringOrEnvVar(values, "external_id", "EXTERNAL_ID_"+connUUID)
		}
	default:
		toolConfig["aws_access_key_id"] = stringOrEnvVar(values, "aws_access_key_id", "AWS_ACCESS_KEY_ID_"+connUUID)
		toolConfig["aws_secret_access_key"] = stringOrEnvVar(values, "aws_secret_access_key", "AWS_SECRET_ACCESS_KEY_"+connUUID)
	}
}

func sanitizeForBrokerTopic(name string) string {
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

const slackToolTimeoutSeconds = 30

func buildSlackConnectorTools(conn ConnectorConfig, connUUID string) []map[string]any {
	token := stringOrEnvVar(conn.Values, "slack_bot_token", "SLACK_BOT_TOKEN_"+connUUID)

	suffix := connectorSuffix(conn.ID)

	sendName := fmt.Sprintf("slack_send_message_%s", suffix)
	sendDescription := "Send messages to Slack channels."
	if name, ok := conn.Values["tool_name"].(string); ok && name != "" {
		sendName = name
	}
	if desc, ok := conn.Values["tool_description"].(string); ok && desc != "" {
		sendDescription = desc
	}

	sendConfig := map[string]any{
		"slack_bot_token":  token,
		"tool_name":        sendName,
		"tool_description": sendDescription,
		"_str_binary":      "slack",
	}

	sendTool := map[string]any{
		"tool_type":       "builtin",
		"tool_name":       sendName,
		"timeout_seconds": slackToolTimeoutSeconds,
		"tool_config":     sendConfig,
	}

	updateName := fmt.Sprintf("slack_update_%s", suffix)
	updateConfig := map[string]any{
		"slack_bot_token": token,
		"_str_binary":     "slack_update",
	}
	updateTool := map[string]any{
		"tool_type":       "builtin",
		"tool_name":       updateName,
		"timeout_seconds": slackToolTimeoutSeconds,
		"tool_config":     updateConfig,
	}

	return []map[string]any{
		withConnectorHIL(sendTool, conn.Values, "slack_send_message"),
		withConnectorHIL(updateTool, conn.Values, "slack_update"),
	}
}

func buildEmailConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	suffix := connectorSuffix(conn.ID)
	toolName := fmt.Sprintf("send_email_%s", suffix)

	var allowlist []string
	if raw, ok := conn.Values["recipient_allowlist"].(string); ok && raw != "" {
		for domain := range strings.SplitSeq(raw, "\n") {
			domain = strings.TrimSpace(domain)
			if domain != "" {
				allowlist = append(allowlist, domain)
			}
		}
	}

	authType, _ := conn.Values["smtp_auth_type"].(string)
	if authType == "" {
		authType = "plain"
	}

	var envelopeFrom string
	switch authType {
	case "oauth_microsoft":
		envelopeFrom = stringOrEnvVar(conn.Values, "oauth_microsoft_email", "ENVELOPE_FROM_"+connUUID)
	case "oauth_google":
		envelopeFrom = stringOrEnvVar(conn.Values, "oauth_google_email", "ENVELOPE_FROM_"+connUUID)
	case "login":
		envelopeFrom = stringOrEnvVar(conn.Values, "envelope_from_login", "ENVELOPE_FROM_"+connUUID)
	case "none":
		envelopeFrom = stringOrEnvVar(conn.Values, "envelope_from_none", "ENVELOPE_FROM_"+connUUID)
	default:
		envelopeFrom = stringOrEnvVar(conn.Values, "envelope_from", "ENVELOPE_FROM_"+connUUID)
	}

	rateLimitPerMinute := 60
	if v, ok := conn.Values["rate_limit_per_minute"].(float64); ok {
		rateLimitPerMinute = int(v)
	}
	rateLimitPerHour := 500
	if v, ok := conn.Values["rate_limit_per_hour"].(float64); ok {
		rateLimitPerHour = int(v)
	}

	maxAttachmentSize := int64(10 * 1024 * 1024)
	if v, ok := conn.Values["max_attachment_size_mb"].(float64); ok {
		maxAttachmentSize = int64(v) * 1024 * 1024
	}
	maxTotalAttachmentSize := int64(25 * 1024 * 1024)
	if v, ok := conn.Values["max_total_attachment_size_mb"].(float64); ok {
		maxTotalAttachmentSize = int64(v) * 1024 * 1024
	}

	smtpTLS, _ := conn.Values["smtp_tls"].(string)
	smtpTLS = cmp.Or(smtpTLS, "starttls")

	toolConfig := map[string]any{
		"tool_name":                  toolName,
		"tool_description":           "Send emails via SMTP. Supports plain text, HTML, and attachments. Recipients must be in the configured allowlist.",
		"smtp_host":                  stringOrEnvVar(conn.Values, "smtp_host", "SMTP_HOST_"+connUUID),
		"smtp_port":                  stringOrEnvVar(conn.Values, "smtp_port", "SMTP_PORT_"+connUUID),
		"smtp_tls":                   smtpTLS,
		"smtp_auth_type":             authType,
		"envelope_from":              envelopeFrom,
		"recipient_allowlist":        allowlist,
		"rate_limit_per_minute":      rateLimitPerMinute,
		"rate_limit_per_hour":        rateLimitPerHour,
		"max_attachment_size_bytes":  maxAttachmentSize,
		"max_total_attachment_bytes": maxTotalAttachmentSize,
		"_str_binary":                "send_email",
	}

	switch authType {
	case "plain":
		toolConfig["smtp_username"] = stringOrEnvVar(conn.Values, "smtp_username", "SMTP_USERNAME_"+connUUID)
		toolConfig["smtp_password"] = stringOrEnvVar(conn.Values, "smtp_password", "SMTP_PASSWORD_"+connUUID)
	case "login":
		toolConfig["smtp_username"] = stringOrEnvVar(conn.Values, "smtp_username_login", "SMTP_USERNAME_"+connUUID)
		toolConfig["smtp_password"] = stringOrEnvVar(conn.Values, "smtp_password_login", "SMTP_PASSWORD_"+connUUID)
	case "oauth_microsoft":
		toolConfig["oauth_tenant_id"] = stringOrEnvVar(conn.Values, "oauth_microsoft_tenant_id", "OAUTH_TENANT_ID_"+connUUID)
		toolConfig["oauth_client_id"] = stringOrEnvVar(conn.Values, "oauth_microsoft_client_id", "OAUTH_CLIENT_ID_"+connUUID)
		toolConfig["oauth_client_secret"] = stringOrEnvVar(conn.Values, "oauth_microsoft_client_secret", "OAUTH_CLIENT_SECRET_"+connUUID)
		toolConfig["oauth_email"] = stringOrEnvVar(conn.Values, "oauth_microsoft_email", "OAUTH_EMAIL_"+connUUID)
	case "oauth_google":
		toolConfig["oauth_client_id"] = stringOrEnvVar(conn.Values, "oauth_google_client_id", "OAUTH_CLIENT_ID_"+connUUID)
		toolConfig["oauth_client_secret"] = stringOrEnvVar(conn.Values, "oauth_google_client_secret", "OAUTH_CLIENT_SECRET_"+connUUID)
		toolConfig["oauth_refresh_token"] = stringOrEnvVar(conn.Values, "oauth_google_refresh_token", "OAUTH_REFRESH_TOKEN_"+connUUID)
		toolConfig["oauth_email"] = stringOrEnvVar(conn.Values, "oauth_google_email", "OAUTH_EMAIL_"+connUUID)
	}

	return withConnectorHIL(map[string]any{
		"tool_type":       "builtin",
		"tool_name":       toolName,
		"timeout_seconds": emailToolTimeoutSeconds,
		"tool_config":     toolConfig,
	}, conn.Values, "send_email")
}

const eventMeshToolTimeoutSeconds = 120

func buildEventMeshConnectorTool(conn ConnectorConfig, connUUID string) map[string]any {
	operations, _ := eventMeshOperations(conn.Values)
	if len(operations) > 0 {
		return buildEventMeshOperationTool(conn, connUUID, operations[0])
	}
	return buildEventMeshOperationTool(conn, connUUID, conn.Values)
}

func buildEventMeshConnectorTools(conn ConnectorConfig, connUUID string) []map[string]any {
	operations, err := eventMeshOperations(conn.Values)
	if err != nil || len(operations) == 0 {
		return []map[string]any{buildEventMeshConnectorTool(conn, connUUID)}
	}
	tools := make([]map[string]any, 0, len(operations))
	for _, operation := range operations {
		tools = append(tools, buildEventMeshOperationTool(conn, connUUID, operation))
	}
	return tools
}

func buildEventMeshOperationTool(conn ConnectorConfig, connUUID string, operation map[string]any) map[string]any {
	operationValues := eventMeshOperationValues(operation)
	suffix := connectorSuffix(conn.ID)

	role := eventMeshRole(operationValues)
	toolName := fmt.Sprintf("%s_%s", role, suffix)

	brokerConfig := map[string]any{
		"broker_url":      stringOrEnvVar(conn.Values, "broker_url", "BROKER_URL_"+connUUID),
		"broker_vpn":      stringOrEnvVar(conn.Values, "broker_vpn", "BROKER_VPN_"+connUUID),
		"broker_username": stringOrEnvVar(conn.Values, "broker_username", "BROKER_USERNAME_"+connUUID),
		"broker_password": stringOrEnvVar(conn.Values, "broker_password", "BROKER_PASSWORD_"+connUUID),
	}

	eventMeshConfig := map[string]any{"broker_config": brokerConfig}
	if expiry, ok := toFloat64(operationValues["request_expiry_ms"]); ok && expiry > 0 {
		eventMeshConfig["request_expiry_ms"] = int(expiry)
	} else {
		eventMeshConfig["request_expiry_ms"] = 60000
	}
	if format, ok := operationValues["payload_format"].(string); ok && format != "" {
		eventMeshConfig["payload_format"] = format
	} else {
		eventMeshConfig["payload_format"] = "json"
	}
	if qos, ok := toFloat64(operationValues["qos"]); ok && (qos == 0 || qos == 1) {
		eventMeshConfig["qos"] = int(qos)
	} else if qosString, ok := operationValues["qos"].(string); ok && (qosString == "0" || qosString == "1") {
		eventMeshConfig["qos"], _ = strconv.Atoi(qosString)
	}
	if rm, ok := operationValues["reply_mode"].(string); ok && rm != "" {
		eventMeshConfig["reply_mode"] = rm
	}

	toolConfig := map[string]any{
		"tool_name":         toolName,
		"topic":             operationValues["topic"],
		"event_mesh_config": eventMeshConfig,
		"_str_binary":       "event_mesh_request",
	}

	if desc, ok := operationValues["tool_description"].(string); ok && desc != "" {
		toolConfig["description"] = desc
	}

	waitForResponse := true
	if wait, ok := operationValues["wait_for_response"].(bool); ok {
		waitForResponse = wait
	} else if wait, ok := operationValues["wait_for_response"].(string); ok {
		waitForResponse = wait == "true"
	}
	toolConfig["wait_for_response"] = waitForResponse

	if paramsJSON, ok := operationValues["parameters"].(string); ok && paramsJSON != "" {
		var params []map[string]any
		if json.Unmarshal([]byte(paramsJSON), &params) == nil && len(params) > 0 {
			toolConfig["parameters"] = params
		}
	} else if params, ok := operationValues["parameters"].([]map[string]any); ok && len(params) > 0 {
		toolConfig["parameters"] = params
	} else if params, ok := operationValues["parameters"].([]any); ok && len(params) > 0 {
		toolConfig["parameters"] = params
	}
	if inputSchema, ok := operationValues["input_schema"].(map[string]any); ok {
		toolConfig["input_schema"] = inputSchema
	}

	return withConnectorHIL(map[string]any{
		"tool_type":       "builtin",
		"tool_name":       toolName,
		"timeout_seconds": eventMeshToolTimeoutSeconds,
		"tool_config":     toolConfig,
	}, conn.Values, role)
}

func AgentInstanceName(agent *store.Agent) string {
	if agent == nil {
		return ""
	}
	return a2a.InstanceName(a2a.KindAgent, agent.ID)
}

func A2AAgentInstanceName(a *store.A2AAgent) string {
	if a == nil {
		return ""
	}
	return a2a.InstanceName(a2a.KindAgent, a.ID)
}

func buildAgentCard(agent *store.Agent, inputModes, outputModes []string) map[string]any {
	description := ""
	if agent.Description != nil {
		description = *agent.Description
	}

	if len(inputModes) == 0 {
		inputModes = []string{"text"}
	}
	if len(outputModes) == 0 {
		outputModes = []string{"text"}
	}

	card := map[string]any{
		"description":        description,
		"defaultInputModes":  inputModes,
		"defaultOutputModes": outputModes,
	}

	skills := parseSkills(agent.Skills)
	if len(skills) > 0 {
		var skillList []map[string]any
		for _, skill := range skills {
			id := skill.id
			if id == "" {
				id = nameToSnakeCase(skill.name)
			}
			entry := map[string]any{
				"id":          id,
				"name":        skill.name,
				"description": skill.description,
			}
			if len(skill.tags) > 0 {
				entry["tags"] = skill.tags
			}
			if len(skill.examples) > 0 {
				entry["examples"] = skill.examples
			}
			if len(skill.inputModes) > 0 {
				entry["inputModes"] = skill.inputModes
			}
			if len(skill.outputModes) > 0 {
				entry["outputModes"] = skill.outputModes
			}
			skillList = append(skillList, entry)
		}
		card["skills"] = skillList
	}

	return card
}

type skillInfo struct {
	id          string
	name        string
	description string
	tags        []string
	examples    []string
	inputModes  []string
	outputModes []string
}

func parseSkills(skillsJSON *string) []skillInfo {
	if skillsJSON == nil || *skillsJSON == "" {
		return nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(*skillsJSON), &raw); err != nil {
		return nil
	}
	var skills []skillInfo
	for _, s := range raw {
		name, _ := s["name"].(string)
		if name == "" {
			continue
		}
		id, _ := s["id"].(string)
		desc, _ := s["description"].(string)
		skills = append(skills, skillInfo{
			id:          id,
			name:        name,
			description: desc,
			tags:        toStringSlice(s["tags"]),
			examples:    toStringSlice(s["examples"]),
			inputModes:  toStringSlice(s["inputModes"]),
			outputModes: toStringSlice(s["outputModes"]),
		})
	}
	return skills
}

func nameToSnakeCase(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

func connectorSuffix(connID string) string {
	s := connID
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ReplaceAll(s, "-", "_")
}

func mssqlEncryptValue(uiValue string) string {
	switch uiValue {
	case "yes":
		return "true"
	case "no":
		return "disable"
	default:
		return uiValue
	}
}

func buildOAuthBlock(values map[string]any, connUUID string) map[string]any {
	mode, _ := values["auth_oauth_mode"].(string)
	if mode == "" {
		mode = "discovery"
	}

	block := map[string]any{"type": "oauth2"}

	if mode == "discovery" {
		if scopes, ok := values["auth_oauth_scopes"].(string); ok && scopes != "" {
			block["scheme"] = map[string]any{"scopes": strings.Fields(scopes)}
		}
		return block
	}

	scheme := map[string]any{
		"authorization_url": stringOrEnvVar(values, "auth_oauth_authorization_url", "AUTH_OAUTH_AUTHORIZATION_URL_"+connUUID),
		"token_url":         stringOrEnvVar(values, "auth_oauth_token_url", "AUTH_OAUTH_TOKEN_URL_"+connUUID),
	}

	if v, ok := values["auth_oauth_refresh_url"].(string); ok && v != "" {
		scheme["refresh_url"] = stringOrEnvVar(values, "auth_oauth_refresh_url", "AUTH_OAUTH_REFRESH_URL_"+connUUID)
	}
	if scopes, ok := values["auth_oauth_scopes"].(string); ok && scopes != "" {
		scheme["scopes"] = strings.Fields(scopes)
	}
	method, _ := values["auth_oauth_token_endpoint_auth_method"].(string)
	if method == "" {
		method = "client_secret_basic"
	}
	scheme["token_endpoint_auth_method"] = method

	credential := map[string]any{
		"client_id": stringOrEnvVar(values, "auth_oauth_client_id", "AUTH_OAUTH_CLIENT_ID_"+connUUID),
	}
	if v, ok := values["auth_oauth_client_secret"].(string); ok && v != "" {
		credential["client_secret"] = stringOrEnvVar(values, "auth_oauth_client_secret", "AUTH_OAUTH_CLIENT_SECRET_"+connUUID)
	}

	block["scheme"] = scheme
	block["credential"] = credential
	return block
}

func (s *YAMLService) buildWebhookGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	safeID := strings.ReplaceAll(gw.ID, "-", "_")
	envSuffix := strings.ToUpper(safeID)

	route := map[string]any{
		"name": "default",
		"match": map[string]any{
			"path":   stringOrDefault(values, "routePath", "/"),
			"method": stringOrDefault(values, "method", "POST"),
		},
		"input_expression": stringOrDefault(values, "inputExpression", ""),
	}

	switch stringOrDefault(values, "authMode", "token") {
	case "hmac":
		hmac := map[string]any{
			"header":   stringOrDefault(values, "hmacHeader", "X-Hub-Signature-256"),
			"algo":     stringOrDefault(values, "hmacAlgorithm", "sha256"),
			"secret":   stringOrResolvedEnv(values, "hmacSecret", "WEBHOOK_HMAC_SECRET_"+envSuffix),
			"prefix":   stringOrAbsentDefault(values, "hmacPrefix", "sha256="),
			"encoding": stringOrDefault(values, "hmacEncoding", "hex"),
		}
		route["auth"] = map[string]any{"hmac": hmac}
	default:
		token := map[string]any{
			"location": stringOrDefault(values, "authTokenLocation", "bearer"),
			"value":    stringOrResolvedEnv(values, "authToken", "WEBHOOK_AUTH_TOKEN_"+envSuffix),
		}
		if name := stringOrDefault(values, "authTokenName", ""); name != "" {
			token["name"] = name
		}
		route["auth"] = map[string]any{"token": token}
	}

	if v := stringOrDefault(values, "targetAgentName", ""); v != "" {
		route["target_agent_name"] = v
	}
	if v := stringOrDefault(values, "targetWorkflowName", ""); v != "" {
		route["target_workflow_name"] = v
	}
	if v := stringOrDefault(values, "runAs", ""); v != "" {
		route["run_as"] = v
	}
	if n, ok := values["maxBodyBytes"]; ok {
		route["max_body_bytes"] = n
	}

	adapterConfig := map[string]any{
		"routes": []any{route},
	}

	appConfig := s.gatewayAdapterAppConfig(gw, safeID, "webhook", adapterConfig, values)
	return configbuilder.AppConfig(gw.ID, appConfig)
}

func (s *YAMLService) buildAgentLinkGatewayConfig(gw *store.Gateway, values map[string]any) map[string]any {
	adapterConfig := map[string]any{
		agentLinkPlatformManagedKey: true,
		agentLinkAgentsKey:          []any{},
	}
	if def, ok := platpkg.LookupGatewayType(platpkg.AgentLinkGatewayType); ok {
		for _, f := range def.Fields {
			if f.Type != gatewayFieldTypeNumber {
				continue
			}
			if n, set := wholeNumberValue(values[f.Name]); set {
				adapterConfig[f.RuntimeKey] = n
			}
		}
	}
	appConfig := map[string]any{
		"namespace":       s.resolvedNamespace(),
		"gateway_id":      gatewayWireID(gw),
		"gateway_adapter": map[string]any{"type": platpkg.AgentLinkGatewayType, "adapter_config": adapterConfig},
	}
	applyClusterAuth(appConfig, values)
	return configbuilder.AppConfig(gw.ID, appConfig)
}

const (
	agentLinkPlatformManagedKey = "platform_managed"
	agentLinkAgentsKey          = "agents"
	gatewayFieldTypeNumber      = "number"
)

func wholeNumberValue(raw any) (int, bool) {
	n, ok := toFloat64(raw)
	if !ok || n != math.Trunc(n) {
		return 0, false
	}
	return int(n), true
}

func mergeCustomHeaders(values map[string]any, params map[string]any) {
	customHeaders, ok := values["custom_headers"].(map[string]any)
	if !ok || len(customHeaders) == 0 {
		return
	}
	headers, _ := params["headers"].(map[string]string)
	if headers == nil {
		headers = make(map[string]string, len(customHeaders))
	}
	for k, v := range customHeaders {
		if s, ok := v.(string); ok {
			headers[k] = s
		}
	}
	params["headers"] = headers
}
