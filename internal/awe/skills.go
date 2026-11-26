// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillsecret"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

const connTypeStdioName = "stdio"

func LoadAndRegisterSkills(ag *agent.Agent, cfg *agent.AgentConfig, svc runtime.Services, remoteExec *remote.Executor) error {
	logger := slog.Default().With("logger_name", "awe-skills", "agentName", cfg.Name)

	if len(cfg.SkillRefs) == 0 {
		return nil
	}

	mgr := skill.NewManager()

	configuredSkills := make(map[string]bool, len(cfg.SkillRefs))
	for _, ref := range cfg.SkillRefs {
		configuredSkills[ref.Name] = true
		mgr.AddAvailable(&skill.SkillInfo{Name: ref.Name})
	}

	if remoteExec == nil {
		namespace := ag.Namespace()
		remoteExec = remote.NewExecutor(svc.Broker(), namespace, ag.Name())
		ag.AddSubComponent(remoteExec)
	}

	remoteExec.SetSkillInitCallback(func(params *remotetool.SkillInitParams) {
		if !configuredSkills[params.SkillName] {
			return
		}
		info := skill.SkillInfoFromInit(params)
		if mgr.AddAvailable(info) {
			logger.Debug("discovered skill from STR", "skill", info.Name,
				"bundledTools", len(info.BundledTools),
				"hasReferences", info.HasReferences)
		}
		propagateBundledToolUpdates(remoteExec, params.BundledTools)
	})

	loader := &strSkillLoader{executor: remoteExec}

	toolConfigBySkill := make(map[string]map[string]map[string]any, len(cfg.SkillRefs))
	for _, ref := range cfg.SkillRefs {
		if len(ref.ToolConfig) > 0 {
			toolConfigBySkill[ref.Name] = ref.ToolConfig
		}
	}

	registrar := newSkillToolRegistrar(ag, remoteExec, toolConfigBySkill, svc.FeatureRegistry(), logger)

	loadTool := skill.NewLoadSkillTool(mgr, loader, registrar)
	unloadTool := skill.NewUnloadSkillTool()
	if err := ag.Tools().Register(loadTool); err != nil {
		logger.Warn("failed to register load_skill tool", "error", err)
	} else {
		recordSkillToolProvenance(ag, loadTool.Name())
	}
	if err := ag.Tools().Register(unloadTool); err != nil {
		logger.Warn("failed to register unload_skill tool", "error", err)
	} else {
		recordSkillToolProvenance(ag, unloadTool.Name())
	}

	for _, spec := range skillruntime.ResourceTools() {
		toolName := spec.Name
		rt := remote.NewTool(&remote.ToolConfig{
			ToolName:          toolName,
			Description:       spec.Description,
			Parameters:        remoteParams(spec.Parameters),
			TimeoutSeconds:    30,
			ArgGuard:          configuredSkillsOnly(mgr),
			ArgumentErrorHint: resourceToolArgumentHint(mgr, spec.Usage),
		}, remoteExec)
		remoteExec.RegisterTool(rt)
		if err := ag.Tools().Register(rt); err != nil {
			logger.Warn("failed to register resource tool", "tool", toolName, "error", err)
		} else {
			recordSkillToolProvenance(ag, toolName)
		}
	}

	for _, toolName := range toolinit.TemplateToolNames() {
		rt := remote.NewTool(&remote.ToolConfig{
			ToolName:       toolName,
			Description:    fmt.Sprintf("Template tool: %s", toolName),
			TimeoutSeconds: 60,
			ArgGuard:       configuredSkillsOnly(mgr),
		}, remoteExec)
		remoteExec.RegisterTool(rt)
		if err := ag.Tools().Register(rt); err != nil {
			logger.Warn("failed to register template tool", "tool", toolName, "error", err)
		} else {
			recordSkillToolProvenance(ag, toolName)
		}
	}

	for _, toolName := range toolinit.TemplateAuthoringToolNames() {
		rt := remote.NewToolWithOptions(&remote.ToolConfig{
			ToolName:       toolName,
			Description:    fmt.Sprintf("Template authoring tool: %s", toolName),
			TimeoutSeconds: 60,
			ArgGuard:       configuredSkillsOnly(mgr),
		}, remoteExec, []string{"sidecar"})
		remoteExec.RegisterTool(rt)
		if err := ag.Tools().Register(rt); err != nil {
			logger.Warn("failed to register templatize tool", "tool", toolName, "error", err)
		} else {
			recordSkillToolProvenance(ag, toolName)
		}
	}

	ag.SetSkillManager(mgr)

	ag.SetSkillRehydrator(&skillRehydratorImpl{
		manager:   mgr,
		loader:    loader,
		registrar: registrar,
		logger:    logger,
	})

	logger.Info("skill system initialized", "configuredSkills", len(cfg.SkillRefs))

	return nil
}

func recordSkillToolProvenance(ag *agent.Agent, toolName string) {
	ag.RecordToolProvenance(toolName, toAgentProvenance(toolinit.SkillProvenance()))
}

type strSkillLoader struct {
	executor skillInvoker
}

type skillInvoker interface {
	Invoke(ctx context.Context, corrID string, req *remote.InvokeRequest, statusCb remote.StatusCallback) (*remote.InvokeResponse, error)
}

func (l *strSkillLoader) LoadSkill(ctx context.Context, skillName string, toolCtx *tool.Context) (map[string]any, error) {
	req := &remote.InvokeRequest{
		ToolName: "load_skill",
		Args: map[string]any{
			"skill_name": skillName,
		},
		AppName:        toolCtx.AgentName,
		UserID:         toolCtx.UserID,
		SessionID:      toolCtx.SessionID,
		TimeoutSeconds: 30,
	}
	if toolCtx.Identity != nil {
		req.AuthToken = toolCtx.Identity.AuthToken
	}
	if toolCtx.A2AUserConfig != "" {
		req.A2AUserConfig = toolCtx.A2AUserConfig
	}

	resp, err := l.executor.Invoke(ctx, "skill-load-"+idgen.New(), req, nil)
	if err != nil {
		return nil, err
	}

	if resp.Result.ToolResult != nil {
		return resp.Result.ToolResult, nil
	}
	return nil, fmt.Errorf("load_skill returned empty result")
}

func propagateBundledToolUpdates(remoteExec *remote.Executor, tools []remotetool.SkillBundledTool) {
	for _, bt := range tools {
		existing := remoteExec.LookupTool(bt.ToolName)
		if existing == nil {
			continue
		}
		if bt.Parameters == nil && bt.Instructions == "" && len(bt.ArtifactParams) == 0 {
			continue
		}
		existing.ApplyInitParamsKeepingEffect(&remotetool.InitParams{
			ToolName:       bt.ToolName,
			Parameters:     bt.Parameters,
			Instructions:   bt.Instructions,
			ArtifactParams: bt.ArtifactParams,
		})
	}
}

type skillToolRegistrar struct {
	agent             *agent.Agent
	executor          *remote.Executor
	tools             *tool.Set
	toolConfigBySkill map[string]map[string]map[string]any

	vault config.VaultFetcher

	allowStdioMCP bool

	builtinMu     sync.Mutex
	skillBuiltins map[string]bool

	logger *slog.Logger
}

func isStdioMCPEntry(toolMap map[string]any) bool {
	if t, _ := toolMap["tool_type"].(string); t != "mcp" {
		return false
	}
	params, ok := toolMap["connection_params"].(map[string]any)
	if !ok {
		return false
	}
	connType, present := params["type"].(string)
	return !present || connType == "" || strings.EqualFold(connType, connTypeStdioName)
}

func newSkillToolRegistrar(
	ag *agent.Agent,
	remoteExec *remote.Executor,
	toolConfigBySkill map[string]map[string]map[string]any,
	featureRegistry *features.Registry,
	logger *slog.Logger,
) *skillToolRegistrar {
	return &skillToolRegistrar{
		agent:             ag,
		executor:          remoteExec,
		tools:             ag.Tools(),
		toolConfigBySkill: toolConfigBySkill,
		vault:             config.EnvVaultFetcher{Timeout: config.AgentVaultTimeout},
		allowStdioMCP:     featureRegistry != nil && featureRegistry.IsEnabled("skill_mcp_stdio"),
		logger:            logger,
	}
}

func (r *skillToolRegistrar) ToolInstructions(toolName string) string {
	t := r.tools.GetTool(toolName)
	if t == nil {
		return ""
	}
	return t.Instructions()
}

func (r *skillToolRegistrar) RegisterSkillTools(skillName string, configs []map[string]any) (registered []string, retry []map[string]any) {
	if len(configs) == 0 {
		return nil, nil
	}
	resolver := newSkillToolResolver(r.vault)
	logger := r.logger.With("skill", skillName)
	for _, toolMap := range configs {
		missing, vaultErrs := resolveOneSkillTool(resolver, toolMap)
		if docKeys := resolver.takeInlineDocRefs(); len(docKeys) > 0 {
			logger.Warn("reference in an inline document is not resolved",
				"entry", describeToolEntry(toolMap),
				"keys", docKeys)
		}
		if len(missing) > 0 {
			logger.Warn("skipping skill tool entry: unset environment variables",
				"entry", describeToolEntry(toolMap),
				"variables", missing)
		}
		if len(vaultErrs) > 0 {
			logger.Warn("skipping skill tool entry: unresolved vault references",
				"entry", describeToolEntry(toolMap),
				"vaultRefs", vaultRefMessages(vaultErrs))
		}
		if len(missing) > 0 {
			continue
		}
		if len(vaultErrs) > 0 {
			retry = append(retry, toolMap)
			continue
		}
		if !r.allowStdioMCP && isStdioMCPEntry(toolMap) {
			logger.Warn("skipping skill tool entry: stdio mcp transport is not permitted from a skill",
				"entry", describeToolEntry(toolMap),
				"enableWith", "SAM_FEATURE_SKILL_MCP_STDIO=true")
			continue
		}
		toolMap = withoutSkillTrustAnnotations(toolMap, logger)
		if _, effectErr := tool.ParseEffectOverrides(nil, []tool.HILEntry{{ToolMap: toolMap}}); effectErr != nil {
			logger.Warn("skipping skill tool entry: invalid effect",
				"entry", describeToolEntry(toolMap),
				"valueLength", len(fmt.Sprint(toolMap[tool.EffectConfigKey])))
			continue
		}
		names, err := r.registerEntry(toolMap, logger)
		r.applyEffectOverride(toolMap, names, err != nil, logger)
		registered = append(registered, names...)
		if err != nil {
			logger.Warn("skipping skill tool entry",
				"entry", describeToolEntry(toolMap),
				"retryLater", toolinit.IsUnavailable(err),
				"error", logging.RedactURLQueries(err.Error()))
			if toolinit.IsUnavailable(err) {
				retry = append(retry, toolMap)
			}
		}
	}
	if paths := resolver.fetchedPaths(); len(paths) > 0 {
		logger.Info("resolved vault references for skill tools", "vaultPaths", paths)
	}
	return registered, retry
}

func withoutSkillTrustAnnotations(toolMap map[string]any, logger *slog.Logger) map[string]any {
	if _, present := toolMap[mcppkg.TrustAnnotationsKey]; !present {
		return toolMap
	}
	logger.Warn("skill tool entry sets trust_annotations; the key is ignored, because trusting a server's annotations is an operator choice made on the agent's own tools entry",
		"entry", describeToolEntry(toolMap))
	stripped := maps.Clone(toolMap)
	delete(stripped, mcppkg.TrustAnnotationsKey)
	return stripped
}

func (r *skillToolRegistrar) applyEffectOverride(toolMap map[string]any, names []string, degraded bool, logger *slog.Logger) {
	overrides, err := tool.ParseEffectOverrides(logger, []tool.HILEntry{{ToolMap: toolMap, ToolNames: names, Degraded: degraded}})
	if err != nil {
		return
	}
	r.tools.AddEffectOverrides(overrides)
}

func (r *skillToolRegistrar) registerEntry(toolMap map[string]any, logger *slog.Logger) ([]string, error) {
	switch toolMap["tool_type"] {
	case "builtin", toolinit.ToolTypeBuiltinGroup:
	default:
		result, err := registerOneToolEntry(r.agent, toolMap, r.executor, logger)
		return result.Added, err
	}
	r.builtinMu.Lock()
	defer r.builtinMu.Unlock()
	result, err := registerOneToolEntry(r.agent, toolMap, r.executor, logger)
	owned := slices.Clone(result.Added)
	if r.skillBuiltins == nil {
		r.skillBuiltins = make(map[string]bool)
	}
	for _, name := range result.Added {
		r.skillBuiltins[name] = true
	}
	for _, name := range result.Existing {
		if r.skillBuiltins[name] {
			owned = append(owned, name)
			continue
		}
		logger.Info("skill does not own a builtin registered by the agent's config or by another tool of the same name",
			"tool", name)
	}
	return owned, err
}

var skillToolConfigKeys = skillsecret.ToolConfigKeys()

func resolveSkillToolRefs(toolMap map[string]any, vault config.VaultFetcher) ([]string, []config.VaultRefError) {
	return resolveOneSkillTool(newSkillToolResolver(vault), toolMap)
}

func resolveOneSkillTool(r *skillToolResolver, toolMap map[string]any) ([]string, []config.VaultRefError) {
	varMark, vaultMark := len(r.unresolved), len(r.vaultErrs)
	r.resolveEntry(toolMap)
	return stableUnique(r.unresolved[varMark:], r.vaultErrs[vaultMark:])
}

func (r *skillToolResolver) takeInlineDocRefs() []string {
	keys := r.inlineDocRefs
	r.inlineDocRefs = nil
	slices.Sort(keys)
	return slices.Compact(keys)
}

func newSkillToolResolver(vault config.VaultFetcher) *skillToolResolver {
	r := &skillToolResolver{}
	if vault != nil {
		r.vault = &memoVaultFetcher{inner: vault}
	}
	return r
}

func (r *skillToolResolver) resolveEntry(toolMap map[string]any) {
	for _, key := range skillToolConfigKeys {
		switch section := toolMap[key].(type) {
		case string:
			toolMap[key] = r.leaf(section)
		case map[string]any:
			r.walkMap(section)
		case []any:
			r.walkSlice(section)
		}
	}
	r.noteRefsInInlineDocuments(toolMap)
}

var skillToolInlineDocumentKeys = []string{"manifest", "specification"}

func (r *skillToolResolver) noteRefsInInlineDocuments(toolMap map[string]any) {
	for _, key := range skillToolInlineDocumentKeys {
		v, ok := toolMap[key]
		if !ok {
			continue
		}
		if ref, found := firstReference(v); found {
			r.inlineDocRefs = append(r.inlineDocRefs, key+": "+ref)
		}
	}
}

func firstReference(node any) (string, bool) {
	switch typed := node.(type) {
	case string:
		if tokens := config.VaultRefTokens(typed); len(tokens) > 0 {
			return tokens[0], true
		}
		if i := strings.Index(typed, "${"); i >= 0 {
			if end := strings.Index(typed[i:], "}"); end >= 0 {
				return typed[i : i+end+1], true
			}
			return "${", true
		}
	case map[string]any:
		for _, v := range typed {
			if ref, ok := firstReference(v); ok {
				return ref, true
			}
		}
	case []any:
		for _, v := range typed {
			if ref, ok := firstReference(v); ok {
				return ref, true
			}
		}
	}
	return "", false
}

func stableUnique(vars []string, vaultErrs []config.VaultRefError) ([]string, []config.VaultRefError) {
	vars, vaultErrs = slices.Clone(vars), slices.Clone(vaultErrs)
	slices.Sort(vars)
	slices.SortFunc(vaultErrs, func(a, b config.VaultRefError) int {
		return cmp.Or(strings.Compare(a.Ref.URI, b.Ref.URI), strings.Compare(a.Error(), b.Error()))
	})
	return slices.Compact(vars), slices.CompactFunc(vaultErrs, func(a, b config.VaultRefError) bool {
		return a.Ref.URI == b.Ref.URI && a.Error() == b.Error()
	})
}

type skillToolResolver struct {
	vault      config.VaultFetcher
	unresolved []string
	vaultErrs  []config.VaultRefError

	inlineDocRefs []string
}

func (r *skillToolResolver) leaf(s string) string {
	expanded, missing := config.ExpandVarsCollect(s, r.lookupNonEmptyEnv)
	r.unresolved = append(r.unresolved, missing...)

	if !strings.Contains(expanded, "vault://") {
		return expanded
	}
	if ref, ok := config.FirstVaultRefWithEmptySegment(expanded); ok {
		r.vaultErrs = append(r.vaultErrs, config.VaultRefError{
			Ref: config.VaultRef{URI: ref},
			Err: errEmptyPathSegment,
		})
		return expanded
	}
	resolved, errs := config.ResolveVaultURIs(expanded, r.vault)
	r.vaultErrs = append(r.vaultErrs, errs...)
	return resolved
}

var errEmptyPathSegment = errors.New(
	"path has an empty segment — a ${VAR} inside the reference expanded to nothing")

func (r *skillToolResolver) lookupNonEmptyEnv(name string) (string, bool) {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

func (r *skillToolResolver) walkMap(node map[string]any) {
	for key, value := range node {
		switch typed := value.(type) {
		case string:
			node[key] = r.leaf(typed)
		case map[string]any:
			r.walkMap(typed)
		case []any:
			r.walkSlice(typed)
		}
	}
}

func (r *skillToolResolver) walkSlice(items []any) {
	for i, item := range items {
		switch typed := item.(type) {
		case string:
			items[i] = r.leaf(typed)
		case map[string]any:
			r.walkMap(typed)
		case []any:
			r.walkSlice(typed)
		}
	}
}

type memoVaultFetcher struct {
	inner   config.VaultFetcher
	secrets map[string]map[string]any
	errs    map[string]error
}

func (r *skillToolResolver) fetchedPaths() []string {
	memo, ok := r.vault.(*memoVaultFetcher)
	if !ok || len(memo.secrets) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(memo.secrets))
}

func (m *memoVaultFetcher) Fetch(path string) (map[string]any, error) {
	if secret, ok := m.secrets[path]; ok {
		return secret, nil
	}
	if err, ok := m.errs[path]; ok {
		return nil, err
	}
	secret, err := m.inner.Fetch(path)
	if err != nil {
		if m.errs == nil {
			m.errs = map[string]error{}
		}
		m.errs[path] = err
		return nil, err
	}
	if m.secrets == nil {
		m.secrets = map[string]map[string]any{}
	}
	m.secrets[path] = secret
	return secret, nil
}

func vaultRefMessages(errs []config.VaultRefError) []string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return msgs
}

var skillNameArgKeys = []string{"skill_name", "skill"}

func configuredSkillsOnly(mgr *skill.Manager) func(args map[string]any) error {
	return func(args map[string]any) error {
		for _, key := range skillNameArgKeys {
			raw, present := args[key]
			if !present || raw == nil {
				continue
			}
			name, ok := raw.(string)
			if !ok {
				return fmt.Errorf("%s must be a skill name", key)
			}
			if name != "" && mgr.GetAvailable(name) == nil {
				return fmt.Errorf("skill %q is not available to this agent", name)
			}
		}
		return nil
	}
}

func resourceToolArgumentHint(mgr *skill.Manager, usage string) func(args map[string]any) string {
	return func(args map[string]any) string {
		name, _ := args["skill_name"].(string)
		loaded := mgr.GetLoaded(name)
		if name == "" || loaded == nil || len(loaded.ResourceFiles) == 0 {
			return usage
		}
		return usage + " " + skillruntime.DescribeResourcePaths(name, loaded.ResourceFiles, loaded.ResourceCount)
	}
}

func remoteParams(schema map[string]any) map[string]remote.ParamConfig {
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	params := make(map[string]remote.ParamConfig, len(props))
	for name, raw := range props {
		prop, _ := raw.(map[string]any)
		typ, _ := prop["type"].(string)
		description, _ := prop["description"].(string)
		params[name] = remote.ParamConfig{
			Type:        typ,
			Description: description,
			Required:    slices.Contains(required, any(name)),
		}
	}
	return params
}

const skillBundledToolSep = "__"

func splitSkillToolName(prefixed string) (skill, bare string, ok bool) {
	i := strings.Index(prefixed, skillBundledToolSep)
	if i <= 0 || i+len(skillBundledToolSep) >= len(prefixed) {
		return "", "", false
	}
	return prefixed[:i], prefixed[i+len(skillBundledToolSep):], true
}

func (r *skillToolRegistrar) RegisterSkillBundledTool(bt remotetool.SkillBundledTool) {
	cfg := &remote.ToolConfig{
		ToolName:    bt.ToolName,
		Description: bt.Description,
	}
	skillName, bareName, ok := splitSkillToolName(bt.ToolName)
	if ok {
		if perTool, hit := r.toolConfigBySkill[skillName]; hit {
			if vals, hasVals := perTool[bareName]; hasVals && len(vals) > 0 {
				cfg.ToolPassthroughConfig = vals
			}
		}
	}
	rt := remote.NewTool(cfg, r.executor)

	if bt.Parameters != nil || bt.Instructions != "" {
		initParams := &remotetool.InitParams{
			ToolName:     bt.ToolName,
			Parameters:   bt.Parameters,
			Instructions: bt.Instructions,
		}
		if len(bt.ArtifactParams) > 0 {
			initParams.ArtifactParams = bt.ArtifactParams
		}
		rt.ApplyInitParamsKeepingEffect(initParams)
	}

	r.executor.RegisterTool(rt)
	if err := r.tools.Register(rt); err != nil {
		r.logger.Warn("failed to register bundled tool", "tool", bt.ToolName, "error", err)
	} else {
		recordSkillToolProvenance(r.agent, bt.ToolName)
		r.logger.Info("registered skill-bundled tool", "tool", bt.ToolName)
	}
}

type skillRehydratorImpl struct {
	manager   *skill.Manager
	loader    skill.SkillLoader
	registrar skill.ToolRegistrar
	logger    *slog.Logger
}

func (r *skillRehydratorImpl) RehydrateSkills(ctx context.Context, toolCtx *tool.Context, names []string) []string {
	logger := r.logger.With("sessionID", toolCtx.SessionID, "traceID", toolCtx.TraceID)
	var failed []string
	for _, name := range names {
		if r.manager.GetAvailable(name) == nil {
			logger.Info("not rehydrating a skill the agent is no longer configured with", "skill", name)
			failed = append(failed, name)
			continue
		}
		if r.manager.IsHeld(name) {
			r.manager.RetryTools(name, r.registrar)
			continue
		}
		result, err := r.loader.LoadSkill(ctx, name, toolCtx)
		if err != nil {
			logger.Warn("failed to rehydrate skill", "skill", name, "error", err)
			failed = append(failed, name)
			continue
		}
		ls, err := r.manager.Hold(ctx, name, skill.ParseLoadSkillResult(name, result), r.registrar)
		if err != nil {
			logger.Warn("stopped waiting for another task to register a skill's tools", "skill", name, "error", err)
			failed = append(failed, name)
			continue
		}
		logger.Info("rehydrated skill", "skill", name,
			"hasInstructions", ls.InstructionContent != "",
			"bundledTools", len(ls.BundledTools))
	}
	return failed
}
