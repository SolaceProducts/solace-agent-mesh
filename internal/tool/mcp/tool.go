// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/common/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/maximhq/bifrost/core/schemas"
)

type MCPTool struct {
	name         string
	bifrostName  string
	description  string
	parameters   *tool.JSONSchema
	resultSchema *tool.JSONSchema
	hints        ToolHints

	trustAnnotations bool
	manager          *Manager
	scopes           []string
	authHandler      *AuthHandler
	logger           *slog.Logger
}

var _ tool.BuiltinTool = (*MCPTool)(nil)

func newMCPTool(
	samName, bifrostName string,
	ct *schemas.ChatTool,
	mgr *Manager,
	cfg *ToolsetConfig,
	authHandler *AuthHandler,
	logger *slog.Logger,
) *MCPTool {
	t := &MCPTool{
		name:        samName,
		bifrostName: bifrostName,
		manager:     mgr,
		scopes:      cfg.RequiredScopes,
		authHandler: authHandler,
		logger:      logger.With("tool", samName, "type", "mcp"),

		trustAnnotations: cfg.TrustAnnotations,
	}

	if ct.Function != nil {
		if ct.Function.Description != nil {
			t.description = *ct.Function.Description
		}
		t.parameters = convertParameters(ct.Function.Parameters)
	}
	t.hints = hintsFromBifrost(ct.Annotations)

	return t
}

func newDirectMCPTool(
	samName, bifrostName string,
	mcpTool mcpgo.Tool,
	mgr *Manager,
	cfg *ToolsetConfig,
	authHandler *AuthHandler,
	logger *slog.Logger,
) *MCPTool {
	t := &MCPTool{
		name:         samName,
		bifrostName:  bifrostName,
		description:  mcpTool.Description,
		manager:      mgr,
		scopes:       cfg.RequiredScopes,
		parameters:   mcpGoInputSchemaToJSON(mcpTool.InputSchema),
		resultSchema: mcpResultEnvelope(mcpGoDeclaredOutputSchema(mcpTool.OutputSchema)),
		hints:        hintsFromMCPGo(mcpTool.Annotations),
		authHandler:  authHandler,
		logger:       logger.With("tool", samName, "type", "mcp"),

		trustAnnotations: cfg.TrustAnnotations,
	}

	return t
}

func newManifestMCPTool(
	samName, bifrostName string,
	mt ManifestTool,
	mgr *Manager,
	cfg *ToolsetConfig,
	authHandler *AuthHandler,
	logger *slog.Logger,
) *MCPTool {
	raw := mt.InputSchema
	if raw == nil {
		raw = map[string]any{"type": "object"}
	}
	schemaType, _ := raw["type"].(string)
	if schemaType == "" {
		schemaType = "object"
	}
	props, _ := raw["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	var required []string
	if req, ok := raw["required"].([]any); ok {
		for _, r := range req {
			if rs, ok := r.(string); ok {
				required = append(required, rs)
			}
		}
	}

	t := &MCPTool{
		name:        samName,
		bifrostName: bifrostName,
		description: mt.Description,
		manager:     mgr,
		scopes:      cfg.RequiredScopes,
		parameters: &tool.JSONSchema{
			Type:       schemaType,
			Properties: props,
			Required:   required,
			Raw:        raw,
		},
		resultSchema: mcpResultEnvelope(manifestDeclaredOutputSchema(mt.OutputSchema)),
		hints:        mt.Annotations,
		authHandler:  authHandler,
		logger:       logger.With("tool", samName, "type", "mcp"),

		trustAnnotations: cfg.TrustAnnotations,
	}

	return t
}

func (t *MCPTool) ResultSchema() *tool.JSONSchema { return t.resultSchema }

func mcpResultEnvelope(declared map[string]any) *tool.JSONSchema {
	if declared == nil {
		return nil
	}
	return &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]any{structuredContentKey: declared},
	}
}

func mcpGoDeclaredOutputSchema(out mcpgo.ToolOutputSchema) map[string]any {
	if len(out.Properties) == 0 {
		return nil
	}
	schemaType := out.Type
	if schemaType == "" {
		schemaType = "object"
	}
	declared := map[string]any{
		"type":       schemaType,
		"properties": out.Properties,
	}
	if len(out.Required) > 0 {
		declared["required"] = out.Required
	}
	if len(out.Defs) > 0 {
		declared["$defs"] = out.Defs
	}
	return declared
}

func manifestDeclaredOutputSchema(raw map[string]any) map[string]any {
	props, _ := raw["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	declared := maps.Clone(raw)
	if schemaType, _ := declared["type"].(string); schemaType == "" {
		declared["type"] = "object"
	}
	return declared
}

func mcpGoInputSchemaToJSON(in mcpgo.ToolInputSchema) *tool.JSONSchema {
	schemaType := in.Type
	if schemaType == "" {
		schemaType = "object"
	}
	props := in.Properties
	if props == nil {
		props = map[string]any{}
	}
	raw := map[string]any{"type": schemaType}
	if len(props) > 0 {
		raw["properties"] = props
	}
	if len(in.Required) > 0 {
		raw["required"] = in.Required
	}
	if len(in.Defs) > 0 {
		raw["$defs"] = in.Defs
	}
	return &tool.JSONSchema{
		Type:       schemaType,
		Properties: props,
		Required:   in.Required,
		Raw:        raw,
	}
}

func (t *MCPTool) Name() string { return t.name }

func (t *MCPTool) Description() string { return t.description }

func (t *MCPTool) Parameters() *tool.JSONSchema { return t.parameters }

func (t *MCPTool) Instructions() string { return "" }

func (t *MCPTool) IsLongRunning() bool { return false }

func (t *MCPTool) RequiredScopes() []string { return t.scopes }

func (t *MCPTool) RawStringArgs() []string { return nil }

func (t *MCPTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *MCPTool) ContextParam() string { return "" }

func (t *MCPTool) AuthHandler() *AuthHandler { return t.authHandler }

func (t *MCPTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	return t.ExecuteResolved(ctx, args, toolCtx, t.EffectClass())
}

func (t *MCPTool) ExecuteResolved(ctx context.Context, args map[string]any, toolCtx *tool.Context, effect tool.EffectClass) (res *tool.Result, retErr error) {
	auditID := auditIdentityFor(t.name, toolCtx)
	logCall(t.logger, auditID)
	start := time.Now()
	defer func() {
		res = res.RedactCredentialsInURLs()
		emitAuditResult(t.logger, auditID, time.Since(start), res, retErr)
	}()

	args = resolveLateEmbedsInArgs(ctx, args, toolCtx, t.logger)

	var result *rawCallToolResult
	var err error
	var sentAccessToken string

	if t.authHandler != nil {
		var pendingAuth *tool.Result
		pendingAuth, result, sentAccessToken, err = t.executeWithAuth(ctx, args, toolCtx)
		if err != nil {
			return t.transportFailure(err), nil
		}
		if pendingAuth != nil {
			return pendingAuth, nil
		}
	} else {
		result, err = t.manager.ExecuteToolRaw(ctx, t.bifrostName, args)
		if err != nil {
			return t.transportFailure(err), nil
		}
	}

	if result.IsError {
		if t.authHandler != nil && (IsAuthError(result) || IsScopeError(result)) {
			pa, triggerErr := t.startAuthFlow(ctx, args, toolCtx, extractErrorText(result), sentAccessToken)
			if triggerErr != nil {
				t.logger.Error("failed to trigger auth flow on auth/scope error",
					"error", triggerErr,
					"traceID", toolCtx.TraceID,
					"taskID", toolCtx.ParentTaskID,
					"toolCallID", toolCtx.ToolCallID,
					"toolName", t.name,
				)
				return tool.Error(fmt.Sprintf("MCP tool %q: trigger auth flow: %v", t.name, triggerErr)), nil
			}
			return pa, nil
		}
		return tool.Error(extractErrorText(result)), nil
	}

	cfg := buildHandlerConfig(toolCtx)
	cfg.Logger = t.logger

	if cfg.EnableIntelligentProcessing != nil && !*cfg.EnableIntelligentProcessing {
		return HandleRawFallback(ctx, result, t.name, cfg, toolCtx)
	}

	items := convertCallToolResult(result, t.name)
	if !cfg.EnableTextFormatDetection {
		items = forcePlainTextFormat(items)
	}
	if effect.ReadOnly() {
		items = withFreshFilenames(items, t.name)
	}
	return HandleMCPResponse(ctx, items, t.name, cfg, toolCtx, result)
}

func (t *MCPTool) transportFailure(err error) *tool.Result {
	return tool.Error(fmt.Sprintf("MCP tool %q: %v", t.name, err),
		tool.WithErrorCode(tool.DispatchErrorCode(tool.ClassifyDispatchError(err))))
}

func buildHandlerConfig(toolCtx *tool.Context) HandlerConfig {
	cfg := DefaultHandlerConfig()
	if toolCtx == nil || toolCtx.ToolConfig == nil {
		return cfg
	}
	if v, ok := toolCtx.ToolConfig["mcp_tool_response_save_threshold_bytes"]; ok {
		if n := toInt(v, 0); n > 0 {
			cfg.SaveThresholdBytes = n
		}
	}
	if v, ok := toolCtx.ToolConfig["mcp_tool_llm_return_max_bytes"]; ok {
		if n := toInt(v, 0); n > 0 {
			cfg.LLMReturnMaxBytes = n
		}
	}
	if v, ok := toolCtx.ToolConfig["schema_inference_depth"]; ok {
		if n := toInt(v, 0); n > 0 {
			cfg.SchemaInferenceDepth = n
		}
	}
	ipCfg, _ := toolCtx.ToolConfig["mcp_intelligent_processing"].(map[string]any)
	if ipCfg == nil {
		return cfg
	}
	if enable, ok := ipCfg["enable_intelligent_processing"].(bool); ok {
		cfg.EnableIntelligentProcessing = &enable
	}
	if v, ok := ipCfg["enable_text_format_detection"].(bool); ok {
		cfg.EnableTextFormatDetection = v
	}
	if v, ok := ipCfg["enable_content_parsing"].(bool); ok {
		cfg.EnableContentParsing = v
	}
	if v, ok := ipCfg["fallback_to_raw_on_error"].(bool); ok {
		cfg.FallbackToRawOnError = v
	}
	if v, ok := ipCfg["save_raw_alongside_intelligent"].(bool); ok {
		cfg.SaveRawAlongsideIntelligent = v
	}
	if v, ok := ipCfg["max_content_items"]; ok {
		cfg.MaxContentItems = clampCapInt(toInt(v, cfg.MaxContentItems), cfg.MaxContentItems, maxContentItemsCeiling)
	}
	if v, ok := ipCfg["max_single_item_size_mb"]; ok {
		cfg.MaxSingleItemSizeMB = clampCapInt(toInt(v, cfg.MaxSingleItemSizeMB), cfg.MaxSingleItemSizeMB, maxSingleItemSizeMBCeiling)
	}
	if v, ok := ipCfg["response_skeleton"].(bool); ok {
		cfg.ResponseSkeleton = v
	}
	if v, ok := ipCfg["skeleton_depth"]; ok {
		if n := toInt(v, 0); n > 0 {
			cfg.SkeletonDepth = n
		}
	}
	if v, ok := ipCfg["skeleton_max_scalar_bytes"]; ok {
		if n := toInt(v, 0); n > 0 {
			cfg.SkeletonMaxScalarBytes = n
		}
	}
	if v, ok := ipCfg["skeleton_max_object_keys"]; ok {
		if n := toInt(v, 0); n > 0 {
			cfg.SkeletonMaxObjectKeys = n
		}
	}
	if v, ok := ipCfg["extract_fields"].(map[string]any); ok {
		fields := make(map[string]string, len(v))
		for name, expr := range v {
			if s, ok := expr.(string); ok && s != "" {
				fields[name] = s
			}
		}
		if len(fields) > 0 {
			cfg.ExtractFields = fields
		}
	}
	return cfg
}

const (
	maxContentItemsCeiling     = 10_000
	maxSingleItemSizeMBCeiling = 1024
)

func clampCapInt(v, defaultVal, ceiling int) int {
	if v <= 0 || v > ceiling {
		return defaultVal
	}
	return v
}

func forcePlainTextFormat(items []ContentItem) []ContentItem {
	for i := range items {
		if items[i].Type != ContentTypeText {
			continue
		}
		items[i].MIMEType = "text/plain"
		if items[i].Metadata == nil {
			items[i].Metadata = map[string]string{}
		}
		items[i].Metadata["detected_format"] = string(TextFormatPlain)
	}
	return items
}

func (t *MCPTool) executeWithAuth(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, *rawCallToolResult, string, error) {
	for retried := false; ; retried = true {
		headers, early, err := t.authHeaders(ctx, args, toolCtx)
		if early != nil || err != nil {
			return early, nil, "", err
		}
		sentAccessToken := toolauth.BearerAccessToken(headers)

		result, err := t.manager.ExecuteToolRawWithHeaders(ctx, t.bifrostName, args, headers, toolCtx)
		if !retried && t.rejectedTokenWasReplaced(ctx, toolCtx, result, err, sentAccessToken) {
			continue
		}
		if errors.Is(err, toolauth.ErrAuthRequired) {
			pa, triggerErr := t.startAuthFlow(ctx, args, toolCtx, "", sentAccessToken)
			if triggerErr != nil {
				return nil, nil, "", fmt.Errorf("trigger auth flow: %w", triggerErr)
			}
			return pa, nil, "", nil
		}
		return nil, result, sentAccessToken, err
	}
}

func (t *MCPTool) authHeaders(ctx context.Context, args map[string]any, toolCtx *tool.Context) (http.Header, *tool.Result, error) {
	headers, err := t.authHandler.GetAuthHeaders(ctx, toolCtx)
	switch {
	case errors.Is(err, toolauth.ErrAuthRequired):
		pa, triggerErr := t.triggerAuthFlow(ctx, args, toolCtx, "")
		if triggerErr != nil {
			return nil, nil, fmt.Errorf("trigger auth flow: %w", triggerErr)
		}
		return nil, pa, nil
	case errors.Is(err, toolauth.ErrTransientRefresh):
		return nil, toolauth.NewSignInProviderUnavailableResult(t.name, err), nil
	case err != nil:
		return nil, nil, fmt.Errorf("auth error: %w", err)
	}
	return headers, nil, nil
}

func (t *MCPTool) rejectedTokenWasReplaced(ctx context.Context, toolCtx *tool.Context, result *rawCallToolResult, err error, sentAccessToken string) bool {
	rejected := errors.Is(err, toolauth.ErrAuthRequired) || (err == nil && result != nil && result.IsError && IsAuthError(result))
	if !rejected || !t.authHandler.StoredTokenReplaces(ctx, toolCtx, sentAccessToken) {
		return false
	}
	t.logger.Debug("tool server rejected a token another writer has replaced; retrying with the stored token",
		"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID, "toolName", t.name)
	return true
}

func (t *MCPTool) triggerAuthFlow(ctx context.Context, args map[string]any, toolCtx *tool.Context, toolMessage string) (*tool.Result, error) {
	return t.startAuthFlow(ctx, args, toolCtx, toolMessage, "")
}

func (t *MCPTool) startAuthFlow(ctx context.Context, args map[string]any, toolCtx *tool.Context, toolMessage, rejectedAccessToken string) (*tool.Result, error) {
	credKey := t.authHandler.CredentialKey()

	if r := toolauth.PreAuthShortCircuit(toolCtx, t.name, t.logger); r != nil {
		return r, nil
	}

	if toolCtx.AuthGaveUp != nil && toolCtx.AuthGaveUp(toolCtx.UserID, toolCtx.TraceID, credKey) {
		t.logger.Warn("auth flow given up — failing tool without re-prompting",
			"traceID", toolCtx.TraceID,
			"taskID", toolCtx.ParentTaskID,
			"credentialKey", credKey,
			"toolName", t.name,
		)
		return toolauth.NewAuthGaveUpResult(t.name, toolMessage), nil
	}

	if toolCtx.BeginAuthFlow != nil && !toolCtx.BeginAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey) {
		t.logger.Info("auth flow coalesced (follower)",
			"traceID", toolCtx.TraceID,
			"taskID", toolCtx.ParentTaskID,
			"credentialKey", credKey,
			"toolCallID", toolCtx.ToolCallID,
			"toolName", t.name,
		)
		return toolauth.WithToolMessage(toolauth.NewPendingAuthFollowerResult(toolCtx.ToolCallID, args, credKey), toolMessage), nil
	}

	leaderActive := true
	defer func() {
		if leaderActive && toolCtx.EndAuthFlow != nil {
			toolCtx.EndAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey)
		}
	}()

	if clearErr := t.authHandler.ClearCredentialForReauth(ctx, toolCtx, rejectedAccessToken); clearErr != nil {
		t.logger.Debug("failed to clear credential before auth flow", "error", clearErr)
	}

	signal, err := t.authHandler.BuildAuthSignal(ctx, toolCtx)
	if err != nil {
		return nil, fmt.Errorf("build auth signal: %w", err)
	}

	if toolCtx.SendSignal == nil {
		t.logger.Warn("SendSignal not available, cannot publish authentication_required",
			"traceID", toolCtx.TraceID,
			"taskID", toolCtx.ParentTaskID,
		)
		return tool.AuthPromptChannelMissing(), nil
	}
	if sigErr := toolCtx.SendSignal(signal); sigErr != nil {
		return tool.AuthPromptUndelivered(sigErr), nil
	}

	t.logger.Info("auth flow paused",
		"traceID", toolCtx.TraceID,
		"taskID", toolCtx.ParentTaskID,
		"credentialKey", credKey,
		"toolCallID", toolCtx.ToolCallID,
		"toolName", t.name,
	)

	leaderActive = false
	return toolauth.WithToolMessage(toolauth.NewPendingAuthResult(toolCtx.ToolCallID, args, credKey), toolMessage), nil
}

func toInt(v any, defaultVal int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return defaultVal
	}
}

func convertParameters(params *schemas.ToolFunctionParameters) *tool.JSONSchema {
	if params == nil {
		return &tool.JSONSchema{
			Type:       "object",
			Properties: map[string]any{},
		}
	}

	raw := make(map[string]any)
	raw["type"] = params.Type

	if params.Properties != nil {
		props := make(map[string]any)
		for _, key := range params.Properties.Keys() {
			val, _ := params.Properties.Get(key)
			props[key] = val
		}
		raw["properties"] = props
	}

	if len(params.Required) > 0 {
		raw["required"] = params.Required
	}

	if params.Defs != nil {
		defs := make(map[string]any)
		for _, key := range params.Defs.Keys() {
			val, _ := params.Defs.Get(key)
			defs[key] = val
		}
		raw["$defs"] = defs
	}
	if params.Definitions != nil {
		defs := make(map[string]any)
		for _, key := range params.Definitions.Keys() {
			val, _ := params.Definitions.Get(key)
			defs[key] = val
		}
		raw["definitions"] = defs
	}

	properties := make(map[string]any)
	if params.Properties != nil {
		for _, key := range params.Properties.Keys() {
			val, _ := params.Properties.Get(key)
			properties[key] = val
		}
	}

	return &tool.JSONSchema{
		Type:       params.Type,
		Properties: properties,
		Required:   params.Required,
		Raw:        raw,
	}
}

const embedOpen = "\u00ab"

const mcpArgsEmbedMaxRecursionDepth = 10

func resolveLateEmbedsInArgs(ctx context.Context, args map[string]any, toolCtx *tool.Context, logger *slog.Logger) map[string]any {
	if toolCtx == nil || toolCtx.ArtifactService == nil {
		return args
	}
	if !containsEmbedMarker(args) {
		return args
	}

	loader := &facadeArtifactLoader{facade: toolCtx.Facade()}
	resolver := embed.NewResolver(loader, embed.Config{MaxRecursionDepth: mcpArgsEmbedMaxRecursionDepth})

	return resolveArgsMap(ctx, args, resolver, logger)
}

func containsEmbedMarker(args map[string]any) bool {
	for _, v := range args {
		switch val := v.(type) {
		case string:
			if strings.Contains(val, embedOpen) {
				return true
			}
		case map[string]any:
			if containsEmbedMarker(val) {
				return true
			}
		case []any:
			for _, item := range val {
				if s, ok := item.(string); ok && strings.Contains(s, embedOpen) {
					return true
				}
				if m, ok := item.(map[string]any); ok && containsEmbedMarker(m) {
					return true
				}
			}
		}
	}
	return false
}

func resolveArgsMap(ctx context.Context, args map[string]any, resolver *embed.Resolver, logger *slog.Logger) map[string]any {
	result := make(map[string]any, len(args))
	for k, v := range args {
		result[k] = resolveArgValue(ctx, v, resolver, logger)
	}
	return result
}

func resolveArgValue(ctx context.Context, v any, resolver *embed.Resolver, logger *slog.Logger) any {
	switch val := v.(type) {
	case string:
		if !strings.Contains(val, embedOpen) {
			return val
		}
		resolved, _, err := resolver.ResolveEmbedsInString(ctx, val, embed.RecursiveArtifactContent)
		if err != nil {
			logger.Debug("late embed resolution failed", "error", err)
			return val
		}
		return resolved
	case map[string]any:
		return resolveArgsMap(ctx, val, resolver, logger)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = resolveArgValue(ctx, item, resolver, logger)
		}
		return result
	default:
		return v
	}
}

type facadeArtifactLoader struct {
	facade *tool.Facade
}

func (l *facadeArtifactLoader) Load(ctx context.Context, filename string, version *int) ([]byte, string, error) {
	content, mimeType, err := l.facade.LoadArtifact(ctx, filename, version)
	if err != nil {
		return nil, "", err
	}
	if content == nil {
		return nil, "", fmt.Errorf("artifact %q not found", filename)
	}
	return content, mimeType, nil
}

func (l *facadeArtifactLoader) ListVersions(ctx context.Context, filename string) ([]int, error) {
	return l.facade.ListArtifactVersions(ctx, filename)
}

func (l *facadeArtifactLoader) GetMetadata(ctx context.Context, filename string, version *int) (map[string]any, error) {
	meta, err := l.facade.LoadArtifactMetadata(ctx, filename, version)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}
	result := map[string]any{
		"mime_type":  meta.MIMEType,
		"size_bytes": meta.SizeBytes,
	}
	maps.Copy(result, meta.Custom)
	return result, nil
}
