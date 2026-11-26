// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	openapipkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func nlog() *slog.Logger { return slog.Default().With("logger_name", "awe-config") }

func sanitizeAgentName(name string) string {
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

func intWithAliases(cfg config.Config, def int, keys ...string) int {
	for _, k := range keys {
		if cfg.Has(k) {
			return cfg.GetInt(k)
		}
	}
	return def
}

func boolPtr(cfg config.Config, key string) *bool {
	if cfg.Has(key) {
		v := cfg.GetBool(key)
		return &v
	}
	return nil
}

func deploymentIDFromConfig(cfg config.Config) string {
	sec := cfg.GetSection("deployment")
	if sec == nil {
		return ""
	}
	return sec.GetString("id")
}

func stringDefault(cfg config.Config, key string, def string) string {
	if v := cfg.GetString(key); v != "" {
		return v
	}
	return def
}

func parseAutoSummarization(cfg config.Config) *agent.CompactionConfig {
	enabled := true
	pct := 0.25
	maxTokens := agent.DefaultSummaryMaxTokens
	if sumCfg := cfg.GetSection("auto_summarization"); sumCfg != nil {
		if sumCfg.Has("enabled") {
			enabled = sumCfg.GetBool("enabled")
		}
		if raw := sumCfg.GetRaw("compaction_percentage"); raw != nil {
			switch v := raw.(type) {
			case float64:
				pct = v
			case int:
				pct = float64(v)
			}
		}
		if raw := sumCfg.GetRaw("summary_max_tokens"); raw != nil {
			switch v := raw.(type) {
			case int:
				maxTokens = v
			case float64:
				maxTokens = int(v)
			}
		}
	}
	return &agent.CompactionConfig{
		Enabled:              enabled,
		CompactionPercentage: pct,
		SummaryMaxTokens:     maxTokens,
	}
}

type parseOptions struct {
	modelAliases    map[string]any
	dynamicBindings bool
}

type ParseOption func(*parseOptions)

func WithModelAliases(aliases map[string]any) ParseOption {
	return func(o *parseOptions) { o.modelAliases = aliases }
}

func WithDynamicBindings(enabled bool) ParseOption {
	return func(o *parseOptions) { o.dynamicBindings = enabled }
}

func applyInterAgentCommunication(ac *agent.AgentConfig, cfg config.Config) {
	iacCfg := cfg.GetSection("inter_agent_communication")
	if iacCfg == nil {
		return
	}
	ac.PeerAllowList = iacCfg.GetStringSlice("allow_list")
	ac.PeerDenyList = iacCfg.GetStringSlice("deny_list")
	ac.PeerRequestTimeoutSeconds = intDefault(iacCfg, "request_timeout_seconds",
		config.DefaultPeerRequestTimeoutSecs)

	workflowFallback, subTaskFallback :=
		config.DefaultWorkflowRequestTimeoutSecs, config.DefaultSubTaskTimeoutSecs
	if iacCfg.Has("request_timeout_seconds") {
		aliased := aliasedTimeoutFlooredAtNodeBudget(ac.PeerRequestTimeoutSeconds)
		workflowFallback, subTaskFallback = aliased, aliased
	}
	ac.WorkflowRequestTimeoutSeconds = intDefault(iacCfg, "workflow_request_timeout_seconds", workflowFallback)
	ac.SubTaskTimeoutSeconds = intDefault(iacCfg, "sub_task_timeout_seconds", subTaskFallback)
}

func aliasedTimeoutFlooredAtNodeBudget(peerSecs int) int {
	return max(peerSecs, int(workflow.DefaultNodeTimeout/time.Second))
}

func warnOnAgentLevelHIL(cfg config.Config, agentName string) {
	if !cfg.Has("hil") {
		return
	}
	nlog().Warn("agent-level 'hil' block ignored: human-in-the-loop is configured per tool, so no tool is gated by this",
		"agentName", agentName)
}

func warnOnMaxConcurrentTasks(cfg config.Config, agentName string) {
	if cfg.Has("max_concurrent_tasks") {
		nlog().Warn("max_concurrent_tasks is ignored; agents no longer cap concurrent tasks", "agentName", agentName)
	}
}

func applyAgentCard(ac *agent.AgentConfig, cfg config.Config) {
	cardCfg := cfg.GetSection("agent_card")
	if cardCfg == nil {
		return
	}
	if desc := cardCfg.GetString("description"); desc != "" {
		ac.Description = desc
	}
	if v := cardCfg.GetString("version"); v != "" {
		ac.Version = v
	}
	ac.Priority = cardCfg.GetInt("priority")
	ac.Skills = parseSkills(cardCfg)
	ac.DefaultInputModes = cardCfg.GetStringSlice("defaultInputModes")
	ac.DefaultOutputModes = cardCfg.GetStringSlice("defaultOutputModes")
	ac.DocumentationURL = cardCfg.GetString("documentationUrl")
	if provMap, ok := cardCfg.GetRaw("provider").(map[string]any); ok {
		ac.Provider = &a2a.AgentProvider{
			Organization: stringVal(provMap, "organization"),
			URL:          stringVal(provMap, "url"),
		}
	}
	if welcomeCfg := cardCfg.GetSection("welcome"); welcomeCfg != nil {
		ac.Welcome = parseWelcome(welcomeCfg)
	}
}

func parseWelcome(welcomeCfg config.Config) *agent.WelcomeConfig {
	wc := &agent.WelcomeConfig{
		Message: welcomeCfg.GetString("message"),
	}
	sugList, _ := welcomeCfg.GetRaw("suggestions").([]any)
	for _, item := range sugList {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sug := agent.WelcomeSuggestion{
			Label:  stringVal(m, "label"),
			Prompt: stringVal(m, "prompt"),
		}
		if v, ok := m["auto_send"].(bool); ok {
			sug.AutoSend = v
		}
		wc.Suggestions = append(wc.Suggestions, sug)
	}
	return wc
}

func ParseAgentConfig(cfg config.Config, opts ...ParseOption) (*agent.AgentConfig, error) {
	var po parseOptions
	for _, opt := range opts {
		opt(&po)
	}
	name := cfg.GetString(config.KeyAgentName)
	if name == "" {
		return nil, fmt.Errorf("app_config missing required 'agent_name'")
	}

	sanitized := sanitizeAgentName(name)
	if sanitized != name {
		nlog().Warn("agent name sanitized: invalid characters replaced with underscores",
			"agentName", name, "sanitized", sanitized)
		name = sanitized
	}

	warnOnAgentLevelHIL(cfg, name)

	ac := &agent.AgentConfig{
		Name:         name,
		DisplayName:  cfg.GetString("display_name"),
		AgentType:    stringDefault(cfg, "agent_type", "standard"),
		Internal:     cfg.GetBool("internal"),
		ShowSessions: cfg.GetBool("show_sessions"),
		Ephemeral:    cfg.GetBool("ephemeral"),
		Namespace:    cfg.GetString(config.KeyNamespace),
		Description:  cfg.GetString("description"),
		Version:      cfg.GetString("version"),
		DeploymentID: deploymentIDFromConfig(cfg),

		SupportsStreaming: cfg.GetBool("supports_streaming"),
		MaxTokens:         cfg.GetInt("max_tokens"),
		MaxLLMCalls:       intDefault(cfg, "max_llm_calls_per_task", agent.DefaultMaxLLMCallsPerTask),

		SystemPrompt:         cfg.GetString("instruction"),
		GlobalInstruction:    cfg.GetString("global_instruction"),
		InjectSystemPurpose:  cfg.GetBool("inject_system_purpose"),
		InjectResponseFormat: cfg.GetBool("inject_response_format"),
		InjectUserProfile:    cfg.GetBool("inject_user_profile"),

		StreamBatchingThresholdBytes:  intDefault(cfg, "stream_batching_threshold_bytes", 1024),
		StreamBatchingFlushIntervalMs: intDefault(cfg, "stream_batching_flush_interval_ms", 300),
		MaxMessageSizeBytes:           intDefault(cfg, "max_message_size_bytes", 10_000_000),

		ArtifactHandlingMode:         stringDefault(cfg, "artifact_handling_mode", "ignore"),
		TextArtifactContentMaxLength: intDefault(cfg, "text_artifact_content_max_length", 32768),
		ToolOutputSaveThresholdBytes: intWithAliases(cfg, agent.DefaultToolOutputSaveThresholdBytes,
			"tool_result_auto_artifact_threshold_bytes",
			"tool_output_save_threshold_bytes"),
		ToolOutputLLMReturnMaxBytes: intWithAliases(cfg, agent.DefaultToolOutputLLMReturnMaxBytes,
			"tool_result_inline_truncation_bytes",
			"tool_output_llm_return_max_bytes"),
		ToolResultPreviewLengthChars: intDefault(cfg, "tool_result_preview_length_chars", 500),
		ToolResultProcessingEnabled:  boolPtr(cfg, "tool_result_processing_enabled"),
		SchemaMaxKeys:                intDefault(cfg, "schema_max_keys", 200),
		SchemaInferenceDepth:         intDefault(cfg, "schema_inference_depth", 4),

		EnableInlineVision:    cfg.GetBool("enable_inline_vision"),
		MaxInlineVisionImages: intDefault(cfg, "max_inline_vision_images", 5),
		MaxInlineVisionBytes:  intDefault(cfg, "max_inline_vision_bytes", 20_971_520),

		EnableInlinePDF:   cfg.GetBool("enable_inline_pdf"),
		MaxInlinePDFBytes: intDefault(cfg, "max_inline_pdf_bytes", 20_971_520),
		MaxInlinePDFPages: intDefault(cfg, "max_inline_pdf_pages", 100),
		MaxInlinePDFCount: intDefault(cfg, "max_inline_pdf_count", 5),

		MaxInlineArtifactBytes: intDefault(cfg, "max_inline_artifact_bytes", 2048),

		MCPToolResponseSaveThresholdBytes: intDefault(cfg, "mcp_tool_response_save_threshold_bytes", mcp.DefaultSaveThresholdBytes),
		MCPToolLLMReturnMaxBytes:          intDefault(cfg, "mcp_tool_llm_return_max_bytes", mcp.DefaultLLMReturnMaxBytes),

		MaxCallDepth: intDefault(cfg, "max_call_depth", 3),

		MaxSubTaskFanout: intDefault(cfg, "max_subtask_fanout", 16),

		MaxMessageRedelivery: intDefault(cfg, "max_message_redelivery", 1),

		RespectMessageTTL: boolDefault(cfg, "respect_message_ttl", true),
	}

	ac.AutoSummarization = parseAutoSummarization(cfg)

	if raw := cfg.GetRaw("mcp_intelligent_processing"); raw != nil {
		if m, ok := raw.(map[string]any); ok {
			ac.MCPIntelligentProcessing = m
		}
	}

	ac.EnableAutoContinuation = boolPtr(cfg, "enable_auto_continuation")
	ac.InjectCurrentTime = boolPtr(cfg, "inject_current_time")
	ac.EnableEmbedResolution = boolPtr(cfg, "enable_embed_resolution")
	ac.EnableArtifactContentInstruction = boolPtr(cfg, "enable_artifact_content_instruction")
	ac.EnableTemplateInstructions = boolPtr(cfg, "enable_template_instructions")
	ac.Headless = boolPtr(cfg, "headless")
	ac.EnableExamples = boolPtr(cfg, "enable_examples")
	ac.AutoInjectArtifactTools = boolPtr(cfg, "auto_inject_artifact_tools")
	ac.AutoInjectDataAnalysisTools = boolPtr(cfg, "auto_inject_data_analysis_tools")
	agent.ApplyDefaultPromptFlags(ac)

	modelRaw, err := resolveModelRaw(cfg, name, po.modelAliases, po.dynamicBindings)
	if err != nil {
		return nil, err
	}
	if modelRaw != nil {
		ac.Model = extractModelName(modelRaw)
		ac.OriginalModel = extractOriginalModelName(modelRaw)
		ac.ModelAPIBase = extractModelAPIBase(modelRaw)
		ac.CacheStrategy = extractCacheStrategy(modelRaw)
		ac.ReasoningBudgetTokens = extractReasoningBudgetTokens(modelRaw)
		ac.ReasoningEffort = extractReasoningEffort(modelRaw)
	}
	ac.ModelAlias = firstModelProviderAlias(cfg)

	if raw := cfg.GetRaw("input_schema"); raw != nil {
		if m, ok := raw.(map[string]any); ok {
			ac.InputSchema = m
		}
	}
	if raw := cfg.GetRaw("output_schema"); raw != nil {
		if m, ok := raw.(map[string]any); ok {
			ac.OutputSchema = m
		}
	}
	ac.ValidationMaxRetries = intDefault(cfg, "validation_max_retries", 2)

	applyAgentCard(ac, cfg)

	ac.SkillsBasePath = cfg.GetString(config.KeySkillsBasePath)
	ac.SkillsCachePath = cfg.GetString("skills_cache_path")
	if skillsRaw := cfg.GetRaw("skills"); skillsRaw != nil {
		if skillsList, ok := skillsRaw.([]any); ok {
			ac.SkillRefs = parseSkillRefs(skillsList)
		}
	}

	ac.CardPublishIntervalSeconds = config.DefaultCardPublishIntervalSecs
	if pubCfg := cfg.GetSection("agent_card_publishing"); pubCfg != nil {
		if pubCfg.Has("interval_seconds") {
			ac.CardPublishIntervalSeconds = pubCfg.GetInt("interval_seconds")
		}
	}

	applyInterAgentCommunication(ac, cfg)

	if discCfg := cfg.GetSection("agent_discovery"); discCfg != nil {
		ac.DiscoveryEnabled = boolPtr(discCfg, "enabled")
		ac.HealthCheckTTLSeconds = intDefault(discCfg, "health_check_ttl_seconds", 300)
		ac.HealthCheckIntervalSeconds = intDefault(discCfg, "health_check_interval_seconds", 60)
	}

	if volumesRaw := cfg.GetRaw("volumes"); volumesRaw != nil {
		if volumesList, ok := volumesRaw.([]any); ok {
			slots, err := parseVolumeSlots(volumesList)
			if err != nil {
				return nil, fmt.Errorf("agent %q volumes config: %w", name, err)
			}
			ac.VolumeSlots = slots
		}
	}

	if networksRaw := cfg.GetRaw("networks"); networksRaw != nil {
		if networksList, ok := networksRaw.([]any); ok {
			policies, err := parseNetworkPolicies(networksList)
			if err != nil {
				return nil, fmt.Errorf("agent %q networks config: %w", name, err)
			}
			ac.NetworkPolicies = policies
		}
	}

	ac.RequiredScopes = cfg.GetStringSlice("required_scopes")

	warnOnMaxConcurrentTasks(cfg, name)

	if cfg.Has("agent_init_function") {
		nlog().Warn("agent_init_function is a Python-specific config option and is ignored in the Go agent runtime", "agentName", name)
	}
	if cfg.Has("agent_cleanup_function") {
		nlog().Warn("agent_cleanup_function is a Python-specific config option and is ignored in the Go agent runtime", "agentName", name)
	}

	if len(ac.SkillRefs) > 0 && len(ac.Skills) == 0 {
		nlog().Warn("agent has loadable skills but no agent_card.skills capabilities; it will publish no discoverable or routable A2A capabilities",
			"agentName", name, "loadableSkills", len(ac.SkillRefs))
	}

	return ac, nil
}

type hilEntryRecord struct {
	toolMap   map[string]any
	toolNames []string
	degraded  bool
}

type agentToolinitHost struct{ *agent.Agent }

func (h agentToolinitHost) AddSubComponent(sc toolinit.Startable) {
	h.Agent.AddSubComponent(sc)
}

func agentToolinitOptions(svc runtime.Services) toolinit.Options {
	var specStore openapipkg.SpecStore
	if svc != nil {
		if s := svc.ConnectorSpecStore(); s != nil {
			specStore = s
		}
	}
	return toolinit.Options{
		ApplyToolDefaults:      applyToolDefaults,
		AdditionalBuiltin:      lookupAdditionalBuiltin,
		AdditionalBuiltinGroup: lookupAdditionalBuiltinGroup,
		ConnectorSpecStore:     specStore,
	}
}

func RegisterTools(ag *agent.Agent, toolsCfg []any, svc runtime.Services) (*remote.Executor, error) {
	host := agentToolinitHost{Agent: ag}
	opts := agentToolinitOptions(svc)
	var entries []toolinit.EntryRecord
	opts.OnLateMCPRegistration = func(recordIndex int, rec toolinit.EntryRecord) {
		ag.ReplaceToolRegistration(registrationIndex(entries, recordIndex), toToolRegistrations([]toolinit.EntryRecord{rec})[0])
		recordToolProvenance(ag, []toolinit.EntryRecord{rec})
	}
	remoteExec, entries, err := toolinit.RegisterTools(host, toolsCfg, svc, opts)
	if err != nil {
		return remoteExec, err
	}

	if regs := toToolRegistrations(entries); len(regs) > 0 {
		ag.SetToolRegistrations(regs)
	}

	recordToolProvenance(ag, entries)
	for _, ig := range ignoredDuplicateToolConfigs(entries) {
		nlog().Warn("tool_config ignored: an earlier tools entry already registered this tool; set it on the first entry that includes the tool",
			"agentName", ag.Name(),
			"tool", ig.tool,
			"ignoredKeys", ig.keys)
	}

	hilEntries := toHILEntries(entries)
	if err := applyHILConfigs(ag, hilEntries); err != nil {
		return remoteExec, err
	}
	if err := applyEffectOverrides(ag, hilEntries); err != nil {
		return remoteExec, err
	}
	if bindings := extractVolumeBindings(hilEntries); len(bindings) > 0 {
		ag.SetToolVolumeBindings(bindings)
	}

	if bindings := extractNetworkBindings(hilEntries); len(bindings) > 0 {
		ag.SetToolNetworkBindings(bindings)
	}

	logger := nlog().With("agentName", ag.Name())
	if ag.Tools().GetTool(indexSearchToolName) == nil {
		if regErr := ag.Tools().Register(&tools.IndexSearchTool{}); regErr != nil {
			logger.Warn("failed to auto-register index_search tool", "error", regErr)
		} else {
			ag.RecordToolProvenance(indexSearchToolName, provenanceOfInjectedBuiltin(indexSearchToolName))
		}
	}

	return remoteExec, nil
}

const indexSearchToolName = "index_search"

func provenanceOfInjectedBuiltin(toolName string) agent.ToolProvenance {
	entry := map[string]any{"tool_type": toolinit.ToolTypeBuiltin, "tool_name": toolName}
	return toAgentProvenance(toolinit.ProvenanceOf(entry, toolName))
}

func toToolRegistrations(entries []toolinit.EntryRecord) []agent.ToolEntryRegistration {
	var regs []agent.ToolEntryRegistration
	for _, e := range entries {
		if e.Status == "" {
			continue
		}
		toolType, _ := e.ToolMap["tool_type"].(string)
		regs = append(regs, agent.ToolEntryRegistration{
			ToolType:    toolType,
			ConnectorID: toolinit.SourceConnectorID(e.ToolMap),
			Name:        toolinit.DescribeToolEntry(e.ToolMap),
			Status:      e.Status,
			Error:       e.Error,
			ToolCount:   len(e.ToolNames),
			ToolNames:   e.ToolNames,
		})
	}
	return regs
}

func registrationIndex(entries []toolinit.EntryRecord, recordIndex int) int {
	index := 0
	for _, e := range entries[:recordIndex] {
		if e.Status != "" {
			index++
		}
	}
	return index
}

type ignoredToolConfig struct {
	tool string
	keys []string
}

func ignoredDuplicateToolConfigs(entries []toolinit.EntryRecord) []ignoredToolConfig {
	seen := make(map[string]bool)
	var ignored []ignoredToolConfig
	for _, e := range entries {
		cfg, _ := e.ToolMap["tool_config"].(map[string]any)
		toolType, _ := e.ToolMap["tool_type"].(string)
		carriesBuiltinConfig := len(cfg) > 0 &&
			(toolType == toolinit.ToolTypeBuiltin || toolType == toolinit.ToolTypeBuiltinGroup)
		for _, name := range e.ToolNames {
			if carriesBuiltinConfig && seen[name] {
				ignored = append(ignored, ignoredToolConfig{tool: name, keys: slices.Sorted(maps.Keys(cfg))})
			}
			seen[name] = true
		}
	}
	return ignored
}

func recordToolProvenance(ag *agent.Agent, entries []toolinit.EntryRecord) {
	for _, e := range entries {
		for _, name := range e.ToolNames {
			ag.RecordToolProvenance(name, toAgentProvenance(toolinit.ProvenanceOf(e.ToolMap, name)))
		}
	}
}

func toAgentProvenance(p toolinit.Provenance) agent.ToolProvenance {
	return agent.ToolProvenance{
		Category:          p.Category,
		ConnectorCategory: p.ConnectorCategory,
		BuiltinCategory:   p.BuiltinCategory,
	}
}

func toHILEntries(in []toolinit.EntryRecord) []hilEntryRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]hilEntryRecord, len(in))
	for i, e := range in {
		out[i] = hilEntryRecord{toolMap: e.ToolMap, toolNames: e.ToolNames, degraded: e.Degraded()}
	}
	return out
}

func registerOneToolEntry(
	ag *agent.Agent,
	toolMap map[string]any,
	remoteExec *remote.Executor,
	logger *slog.Logger,
) (toolinit.EntryResult, error) {
	host := agentToolinitHost{Agent: ag}
	result, err := toolinit.RegisterOneToolEntry(host, toolMap, remoteExec, agentToolinitOptions(nil), logger)
	for _, name := range result.Added {
		ag.RecordToolProvenance(name, toAgentProvenance(toolinit.SkillProvenance()))
	}
	return result, err
}

func describeToolEntry(toolMap map[string]any) string {
	return toolinit.DescribeToolEntry(toolMap)
}

func lookupBuiltinTool(name string, toolMap map[string]any) tool.BuiltinTool {
	return toolinit.LookupBuiltinTool(name, toolMap, agentToolinitOptions(nil))
}

const artifactManagementGroup = "artifact_management"

const dataAnalysisGroupName = "data_analysis"

func EnsureArtifactManagementGroup(tools []any, autoInject *bool, agentName string, logger *slog.Logger) []any {
	return ensureBuiltinGroup(tools, artifactManagementGroup, autoInject, agentName, logger)
}

func EnsureDataAnalysisGroup(tools []any, autoInject *bool, agentName string, logger *slog.Logger) []any {
	return ensureBuiltinGroup(tools, dataAnalysisGroupName, autoInject, agentName, logger)
}

func ensureBuiltinGroup(tools []any, groupName string, autoInject *bool, agentName string, logger *slog.Logger) []any {
	if autoInject != nil && !*autoInject {
		if logger != nil {
			logger.Debug("skipping builtin-group auto-injection", "group", groupName, "agentName", agentName)
		}
		return tools
	}
	if builtinGroupAlreadyMaterialized(tools, groupName) {
		return tools
	}
	if logger != nil {
		logger.Info("auto-injecting builtin-group (not declared in YAML)", "group", groupName, "agentName", agentName)
	}
	injected := make([]any, 0, len(tools)+1)
	injected = append(injected, map[string]any{
		"tool_type":  toolinit.ToolTypeBuiltinGroup,
		"group_name": groupName,
	})
	return append(injected, tools...)
}

func builtinGroupAlreadyMaterialized(tools []any, groupName string) bool {
	canonical := schema.CanonicalBuiltinGroup(groupName)
	members := make(map[string]struct{})
	for _, name := range lookupBuiltinGroup(groupName) {
		members[name] = struct{}{}
	}

	for _, item := range tools {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch t, _ := m["tool_type"].(string); t {
		case toolinit.ToolTypeBuiltinGroup:
			if g, _ := m["group_name"].(string); schema.CanonicalBuiltinGroup(g) == canonical {
				return true
			}
		case toolinit.ToolTypeBuiltin:
			if n, _ := m["tool_name"].(string); n != "" {
				if _, isMember := members[n]; isMember {
					return true
				}
			}
		}
	}
	return false
}

func lookupBuiltinGroup(group string) []string {
	return toolinit.LookupBuiltinGroup(group, agentToolinitOptions(nil))
}

func firstModelProviderAlias(cfg config.Config) string {
	if cfg.GetRaw("model") != nil {
		return ""
	}
	list, ok := cfg.GetRaw("model_provider").([]any)
	if !ok {
		return ""
	}
	for _, item := range list {
		if alias, ok := item.(string); ok && alias != "" {
			return alias
		}
	}
	return ""
}

func resolveModelRaw(cfg config.Config, agentName string, modelAliases map[string]any, bindingsEnabled bool) (any, error) {
	modelRaw := cfg.GetRaw("model")
	providerRaw := cfg.GetRaw("model_provider")

	hasProviderList := false
	var providerAliases []string
	if providerRaw != nil {
		list, ok := providerRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("agent %q: model_provider must be a list of alias strings, got %T", agentName, providerRaw)
		}
		for _, item := range list {
			alias, ok := item.(string)
			if !ok || alias == "" {
				return nil, fmt.Errorf("agent %q: model_provider entries must be non-empty strings, got %v", agentName, item)
			}
			providerAliases = append(providerAliases, alias)
		}
		hasProviderList = len(providerAliases) > 0
	}

	switch {
	case modelRaw != nil && hasProviderList:
		nlog().Warn("agent config has both 'model' and 'model_provider'; using 'model' and ignoring 'model_provider'",
			"agentName", agentName, "modelProvider", providerAliases)
		return modelRaw, nil
	case modelRaw != nil:
		return modelRaw, nil
	case hasProviderList:
		if bindingsEnabled {
			nlog().Debug("agent config uses 'model_provider'; deferring resolution to BindingsRegistry",
				"agentName", agentName, "modelProvider", providerAliases)
			return nil, nil
		}
		if modelAliases == nil {
			nlog().Warn("agent config uses 'model_provider' but no shared-config model aliases are available; model not resolved",
				"agentName", agentName, "modelProvider", providerAliases)
			return nil, nil
		}
		for _, alias := range providerAliases {
			if block, ok := modelAliases[alias]; ok {
				return block, nil
			}
		}
		return nil, fmt.Errorf("agent %q: no alias in model_provider %v resolves to a shared-config model block", agentName, providerAliases)
	default:
		if modelAliases != nil {
			return nil, fmt.Errorf("agent %q: neither 'model' nor 'model_provider' is set", agentName)
		}
		return nil, nil
	}
}

func extractModelName(raw any) string {
	var model string
	switch v := raw.(type) {
	case string:
		model = v
	case map[string]any:
		model, _ = v["model"].(string)
	}
	_, bare := samapp.ParseModelProvider(model)
	return bare
}

func extractOriginalModelName(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		if m, ok := v["model"].(string); ok {
			return m
		}
	}
	return ""
}

func extractModelAPIBase(raw any) string {
	if m, ok := raw.(map[string]any); ok {
		base, _ := m["api_base"].(string)
		return base
	}
	return ""
}

const defaultCacheStrategy = "5m"

func extractCacheStrategy(raw any) string {
	valid := map[string]bool{"none": true, "5m": true, "1h": true}
	if m, ok := raw.(map[string]any); ok {
		if s, ok := m["cache_strategy"].(string); ok {
			if valid[s] {
				return s
			}
			nlog().Warn("invalid cache_strategy, defaulting to 5m", "value", s)
		}
	}
	return defaultCacheStrategy
}

func extractReasoningEffort(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	thinking, ok := m["thinking"].(map[string]any)
	if !ok {
		return ""
	}
	value, present := thinking["effort"]
	if !present || value == nil {
		return ""
	}
	effort, isString := value.(string)
	if !isString {
		nlog().Warn("invalid thinking.effort, ignoring", "value", fmt.Sprintf("%v", value))
		return ""
	}
	if strings.TrimSpace(effort) == "" {
		return ""
	}
	normalized := llm.NormalizeReasoningEffort(effort)
	if normalized == "" {
		nlog().Warn("invalid thinking.effort, ignoring", "value", effort)
	}
	return normalized
}

func extractReasoningBudgetTokens(raw any) int {
	m, ok := raw.(map[string]any)
	if !ok {
		return 0
	}
	thinking, ok := m["thinking"]
	if !ok {
		return 0
	}
	thinkingMap, ok := thinking.(map[string]any)
	if !ok {
		return 0
	}
	switch v := thinkingMap["budget_tokens"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

func parseSkills(cardCfg config.Config) []a2a.AgentSkill {
	raw := cardCfg.GetRaw("skills")
	skillList, ok := raw.([]any)
	if !ok || len(skillList) == 0 {
		return nil
	}

	var skills []a2a.AgentSkill
	for _, item := range skillList {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		skill := a2a.AgentSkill{
			ID:          stringVal(m, "id"),
			Name:        stringVal(m, "name"),
			Description: stringVal(m, "description"),
			Tags:        []string{},
		}
		if tags, ok := m["tags"].([]any); ok {
			for _, t := range tags {
				if s, ok := t.(string); ok {
					skill.Tags = append(skill.Tags, s)
				}
			}
		}
		if examples, ok := m["examples"].([]any); ok {
			for _, ex := range examples {
				if s, ok := ex.(string); ok {
					skill.Examples = append(skill.Examples, s)
				}
			}
		}
		if modes, ok := m["inputModes"].([]any); ok {
			for _, mo := range modes {
				if s, ok := mo.(string); ok {
					skill.InputModes = append(skill.InputModes, s)
				}
			}
		}
		if modes, ok := m["outputModes"].([]any); ok {
			for _, mo := range modes {
				if s, ok := mo.(string); ok {
					skill.OutputModes = append(skill.OutputModes, s)
				}
			}
		}
		skills = append(skills, skill)
	}
	return skills
}

func parseSkillRefs(raw []any) []agent.SkillRef {
	var refs []agent.SkillRef
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ref := agent.SkillRef{
			Name: stringVal(m, "name"),
			Path: stringVal(m, "path"),
		}
		if tcRaw, ok := m["tool_config"].(map[string]any); ok && len(tcRaw) > 0 {
			tc := make(map[string]map[string]any, len(tcRaw))
			for toolName, vals := range tcRaw {
				inner, ok := vals.(map[string]any)
				if !ok {
					continue
				}
				cp := make(map[string]any, len(inner))
				for k, v := range inner {
					cp[k] = v
				}
				tc[toolName] = cp
			}
			if len(tc) > 0 {
				ref.ToolConfig = tc
			}
		}
		if ref.Name != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func stringVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolVal(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func applyHILConfigs(ag *agent.Agent, entries []hilEntryRecord) error {
	hilConfigs, err := parseHILConfigsFromEntries(entries)
	if err != nil {
		return err
	}
	ag.SetHILToolConfigs(hilConfigs)
	return nil
}

func applyEffectOverrides(ag *agent.Agent, entries []hilEntryRecord) error {
	overrides, err := tool.ParseEffectOverrides(nlog().With("agentName", ag.Name()), sharedToolEntries(entries))
	if err != nil {
		return err
	}
	ag.Tools().SetEffectOverrides(overrides)
	return nil
}

func extractVolumeBindings(entries []hilEntryRecord) map[string]map[string]string {
	return extractStringBindings(entries, "volume_bindings")
}

func extractNetworkBindings(entries []hilEntryRecord) map[string]map[string]string {
	return extractStringBindings(entries, "network_bindings")
}

func extractStringBindings(entries []hilEntryRecord, blockKey string) map[string]map[string]string {
	out := make(map[string]map[string]string)
	for _, entry := range entries {
		raw, ok := entry.toolMap[blockKey].(map[string]any)
		if !ok || len(raw) == 0 {
			continue
		}
		for _, toolName := range entry.toolNames {
			bindings := make(map[string]string, len(raw))
			for paramName, valRaw := range raw {
				if val, ok := valRaw.(string); ok {
					bindings[paramName] = val
				}
			}
			if len(bindings) > 0 {
				out[toolName] = bindings
			}
		}
	}
	return out
}

func parseHILConfigsFromEntries(entries []hilEntryRecord) (map[string]agent.HILToolConfig, error) {
	return tool.ParseHILConfigs(nlog(), sharedToolEntries(entries))
}

func sharedToolEntries(entries []hilEntryRecord) []tool.HILEntry {
	shared := make([]tool.HILEntry, 0, len(entries))
	for _, entry := range entries {
		shared = append(shared, tool.HILEntry{ToolMap: entry.toolMap, ToolNames: entry.toolNames, Degraded: entry.degraded})
	}
	return shared
}

func parseHILToolConfig(m map[string]any, toolName string) (agent.HILToolConfig, error) {
	return tool.ParseHILToolConfig(m, toolName)
}

func mergeHILToolConfig(base agent.HILToolConfig, override map[string]any, toolName string) (agent.HILToolConfig, error) {
	return tool.MergeHILToolConfig(base, override, toolName)
}

func parseApprovalRules(raw any, toolName string) ([]agent.ApprovalRule, error) {
	return tool.ParseApprovalRules(raw, toolName)
}

func parseVolumeSlots(raw []any) ([]agent.VolumeSlot, error) {
	var slots []agent.VolumeSlot
	seen := make(map[string]bool)
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		slot := agent.VolumeSlot{
			Name:          stringVal(m, "name"),
			Provision:     stringVal(m, "provision"),
			VolumeID:      stringVal(m, "volume_id"),
			TTL:           stringVal(m, "ttl"),
			Description:   stringVal(m, "description"),
			PromptMessage: stringVal(m, "prompt_message"),
			AllowExec:     boolVal(m, "allow_exec"),
		}
		if slot.Name == "" {
			return nil, fmt.Errorf("volume slot missing required 'name'")
		}
		if seen[slot.Name] {
			return nil, fmt.Errorf("duplicate volume slot name %q", slot.Name)
		}
		seen[slot.Name] = true

		switch slot.Provision {
		case "static":
			if slot.VolumeID == "" {
				return nil, fmt.Errorf("volume slot %q: static provision requires 'volume_id'", slot.Name)
			}
		case "auto_create":
			if slot.TTL == "" {
				return nil, fmt.Errorf("volume slot %q: auto_create provision requires 'ttl'", slot.Name)
			}
		case "prompt_user":
		default:
			return nil, fmt.Errorf("volume slot %q: unknown provision strategy %q (must be static, auto_create, or prompt_user)", slot.Name, slot.Provision)
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

func parseNetworkPolicies(raw []any) ([]agent.NetworkPolicy, error) {
	var policies []agent.NetworkPolicy
	seen := make(map[string]bool)
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		policy := agent.NetworkPolicy{
			Name:        stringVal(m, "name"),
			Description: stringVal(m, "description"),
		}
		if policy.Name == "" {
			return nil, fmt.Errorf("network policy missing required 'name'")
		}
		if seen[policy.Name] {
			return nil, fmt.Errorf("duplicate network policy name %q", policy.Name)
		}
		seen[policy.Name] = true

		if alRaw, ok := m["allowlist"].([]any); ok {
			for _, entry := range alRaw {
				if s, ok := entry.(string); ok {
					policy.Allowlist = append(policy.Allowlist, s)
				}
			}
		}
		policies = append(policies, policy)
	}
	return policies, nil
}
