// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

const aweInternalKeyModelAliases = "_model_aliases"

func NewAWEExecutor(svc runtime.Services, opts ...AWEOption) *awe.Executor {
	exe, _ := newAWEExecutor(svc, opts...)
	return exe
}

type AWEOption func(*aweOptions)

type aweOptions struct {
	workflowDatabaseURL string
}

func WithAWEBaseConfig(rootCfg map[string]any) AWEOption {
	return func(o *aweOptions) {
		if ss, ok := rootCfg["session_service"].(map[string]any); ok {
			o.workflowDatabaseURL, _ = ss["database_url"].(string)
		}
	}
}

func newAWEExecutor(svc runtime.Services, opts ...AWEOption) (*awe.Executor, *spendPublishers) {
	var o aweOptions
	for _, opt := range opts {
		opt(&o)
	}
	exe := awe.NewExecutor(svc)

	if b := svc.Bindings(); b != nil {
		if reg, ok := b.(*bindings.BindingsRegistry); ok {
			exe.SetBindings(reg)
		}
	}

	spendPubs := newSpendPublishers(svc)
	exe.OnStop(spendPubs.close)
	peerDirs := newPeerDirectories(svc)
	exe.OnStop(peerDirs.close)
	exe.RegisterKind("agent", func(cfg config.Config) (awe.Instance, error) {
		return newAgentInstance(svc, spendPubs, peerDirs, cfg)
	})

	exe.RegisterKind("workflow", func(cfg config.Config) (awe.Instance, error) {
		return newWorkflowInstance(spendPubs, peerDirs, cfg, o.workflowDatabaseURL)
	})

	exe.RegisterKind("proxy", func(cfg config.Config) (awe.Instance, error) {
		return proxy.NewInstance(cfg)
	})

	return exe, spendPubs
}

func newAgentInstance(svc runtime.Services, spendPubs *spendPublishers, peerDirs *peerDirectories, cfg config.Config) (awe.Instance, error) {
	appCfg := cfg.GetSection(config.KeyAppConfig)
	if appCfg == nil {
		return nil, fmt.Errorf("missing '%s' section", config.KeyAppConfig)
	}

	var parseOpts []awe.ParseOption
	if aliases, ok := cfg.GetRaw(aweInternalKeyModelAliases).(map[string]any); ok && len(aliases) > 0 {
		parseOpts = append(parseOpts, awe.WithModelAliases(aliases))
	}
	if svc.Bindings() != nil {
		parseOpts = append(parseOpts, awe.WithDynamicBindings(true))
	}

	agentCfg, err := awe.ParseAgentConfig(appCfg, parseOpts...)
	if err != nil {
		return nil, err
	}
	if err := a2a.ValidateNamespace(agentCfg.Namespace); err != nil {
		return nil, fmt.Errorf("agent %q: %w", agentCfg.Name, err)
	}

	ag := agent.New(*agentCfg)
	ag.SetSpendPublisher(spendPubs.forNamespace(agentCfg.Namespace))
	ag.SetContextSavingsCounting(spend.Enabled(svc))
	if dir := peerDirs.forNamespace(agentCfg.Namespace); dir != nil {
		ag.SetPeerDirectory(dir)
	}

	var toolsList []any
	if toolsRaw := appCfg.GetRaw("tools"); toolsRaw != nil {
		if list, ok := toolsRaw.([]any); ok {
			toolsList = list
		}
	}
	toolsList = withFileTools(toolsList, agentCfg, svc.Logger().With("logger_name", "awe-config"))

	if err := awe.LoadAndRegisterConnectors(context.Background(), ag, appCfg, svc); err != nil {
		return nil, fmt.Errorf("register connectors: %w", err)
	}

	var remoteExec *remote.Executor
	if len(toolsList) > 0 {
		var err error
		remoteExec, err = awe.RegisterTools(ag, toolsList, svc)
		if err != nil {
			return nil, fmt.Errorf("register tools: %w", err)
		}
	}

	if !agentCfg.Internal {
		agentCfg.SkillRefs = withHorizontalSkills(agentCfg.SkillRefs, svc.FeatureRegistry())
	}

	if err := ag.ValidateVolumeConfig(); err != nil {
		return nil, fmt.Errorf("volume config: %w", err)
	}
	if err := ag.ValidateNetworkConfig(); err != nil {
		return nil, fmt.Errorf("network config: %w", err)
	}

	if len(agentCfg.SkillRefs) > 0 {
		if err := awe.LoadAndRegisterSkills(ag, agentCfg, svc, remoteExec); err != nil {
			return nil, fmt.Errorf("load skills: %w", err)
		}
	}

	return ag, nil
}

func newWorkflowInstance(spendPubs *spendPublishers, peerDirs *peerDirectories, cfg config.Config, baseDatabaseURL string) (awe.Instance, error) {
	appCfg := cfg.GetSection(config.KeyAppConfig)
	if appCfg == nil {
		return nil, fmt.Errorf("missing '%s' section", config.KeyAppConfig)
	}

	name := appCfg.GetString("name")
	if name == "" {
		name = cfg.GetString("name")
	}
	namespace := appCfg.GetString(config.KeyNamespace)
	if err := a2a.ValidateNamespace(namespace); err != nil {
		return nil, fmt.Errorf("workflow %q: %w", name, err)
	}
	if namespace == "" {
		namespace = config.DefaultNamespace
	}

	wfRaw := appCfg.GetRaw("workflow")
	wfMap, ok := wfRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("workflow %q: missing or invalid 'workflow' section", name)
	}

	wfCfg, err := workflow.ParseWorkflowConfig(name, namespace, wfMap)
	if err != nil {
		return nil, err
	}

	if pubCfg := appCfg.GetSection("agent_card_publishing"); pubCfg != nil {
		wfCfg.CardPublishIntervalSeconds = pubCfg.GetInt("interval_seconds")
	}

	var appTools, appConnectors []any
	if list, ok := appCfg.GetRaw("tools").([]any); ok {
		appTools = list
	}
	if list, ok := appCfg.GetRaw("connectors").([]any); ok {
		appConnectors = list
	}
	wfCfg.MergeAppConfigToolsAndConnectors(appTools, appConnectors)

	if decisionRaw, ok := appCfg.GetRaw("decision").(map[string]any); ok {
		provider, err := workflow.MergeOperatorDecision(wfCfg.Decision, decisionRaw)
		if err != nil {
			return nil, fmt.Errorf("workflow %q: %w", name, err)
		}
		wfCfg.Decision = provider
	}
	for _, tsRaw := range []any{appCfg.GetRaw("toolsets"), wfMap["toolsets"]} {
		list, ok := tsRaw.([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			if name, ok := item.(string); ok && name != "" {
				wfCfg.Toolsets = append(wfCfg.Toolsets, name)
			}
		}
	}

	wfCfg.HILDatabaseURL = baseDatabaseURL
	if ss := appCfg.GetSection("session_service"); ss != nil {
		wfCfg.HILDatabaseURL = ss.GetString("database_url")
	}

	instance := workflow.NewWorkflowInstance(wfCfg)
	instance.SetSpendPublisher(spendPubs.forNamespace(namespace))
	if dir := peerDirs.forNamespace(namespace); dir != nil {
		instance.SetPeerDirectory(dir)
	}
	return instance, nil
}

func LoadAWEApps(exe *awe.Executor, entries []samapp.AppEntry, logger *slog.Logger) ([]string, error) {
	var names []string
	for _, entry := range entries {
		kind := "agent"
		if _, hasWorkflow := entry.AppConfig["workflow"]; hasWorkflow {
			kind = "workflow"
		} else if _, hasProxied := entry.AppConfig["proxied_agents"]; hasProxied {
			kind = "proxy"
		}

		if kind == "proxy" {
			expanded := proxy.ExpandProxyConfig(entry.AppConfig)
			for _, ep := range expanded {
				cfgMap := map[string]any{
					"kind":              "proxy",
					"name":              ep.Name,
					config.KeyAppConfig: ep.AppConfig,
				}
				if err := exe.LoadConfig(config.FromMap(cfgMap)); err != nil {
					return names, fmt.Errorf("load proxy %q: %w", ep.Name, err)
				}
				logger.Info("loaded instance", "name", ep.Name, "kind", "proxy")
				names = append(names, ep.Name)
			}
			continue
		}

		cfgMap := map[string]any{
			"kind":              kind,
			"name":              entry.Name,
			config.KeyAppConfig: entry.AppConfig,
		}
		if len(entry.ModelAliases) > 0 {
			cfgMap[aweInternalKeyModelAliases] = entry.ModelAliases
		}
		if err := exe.LoadConfig(config.FromMap(cfgMap)); err != nil {
			return names, fmt.Errorf("load %s %q: %w", kind, entry.Name, err)
		}
		logger.Info("loaded instance", "name", entry.Name, "kind", kind)
		names = append(names, entry.Name)
	}
	return names, nil
}

func BuildAWERuntimeOptions(ctx context.Context, rootCfg map[string]any, apps []samapp.AppEntry, logger *slog.Logger) ([]runtime.Option, session.Store, error) {
	opts := []runtime.Option{
		runtime.WithLogger(logger),
	}

	for _, app := range apps {
		if app.ModelRaw == nil {
			continue
		}
		llmOpts, err := buildLLMOptions(ctx, app.ModelRaw, logger)
		if err != nil {
			return nil, nil, fmt.Errorf("LLM for %q: %w", app.Name, err)
		}
		opts = append(opts, llmOpts...)
		break
	}

	var sessionStore session.Store
	sessionSource := "app"
	sessionCfg := firstAppSessionService(apps)
	if sessionCfg == nil {
		sessionCfg, _ = rootCfg["session_service"].(map[string]any)
		sessionSource = "awe_base_top_level"
	}
	if sessionCfg != nil {
		store, err := samapp.CreateSessionStore(sessionCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("session store: %w", err)
		}
		sessionStore = store
		dbURL, _ := sessionCfg["database_url"].(string)
		sessType, _ := sessionCfg["type"].(string)
		logger.Info("session store initialized",
			"type", sessType,
			"databaseURL", dbutil.RedactDSN(dbURL),
			"source", sessionSource)
		opts = append(opts, runtime.WithSessions(sessionStore))
	}

	appConfigs := make([]map[string]any, len(apps))
	for i, app := range apps {
		appConfigs[i] = app.AppConfig
	}
	metrics := createMetricsRecorder(rootCfg, appConfigs, logger, "sam-awe")

	var primaryArtSvc *artifact.ScopedService
	var primaryAppName string
	var primaryType string
	for _, app := range apps {
		svcCfg, ok := app.AppConfig["artifact_service"].(map[string]any)
		if !ok {
			continue
		}
		svcType, _ := svcCfg["type"].(string)
		if primaryArtSvc == nil {
			primaryAppName = app.Name
			primaryType = svcType
			svc := samapp.CreateArtifactService(svcCfg, app.AppConfig)
			if svc != nil {
				primaryArtSvc = svc
				opts = append(opts, runtime.WithArtifacts(svc))
			}
			continue
		}
		if svcType != "" && svcType != primaryType {
			logger.Warn("artifact_service type mismatch across apps; runtime uses the first config",
				"usingApp", primaryAppName,
				"usingType", primaryType,
				"ignoredApp", app.Name,
				"ignoredType", svcType,
			)
		}
	}

	connectorSpecStore, err := objectstore.NewResourceStore(nil, objectstore.ResourceStoreOptions{
		YAMLKey:    "resource_store",
		Purpose:    "connector resources",
		SubdirName: "resources",
		EnvBucket:  objectstore.ResolveBucket("CONNECTOR_SPEC_BUCKET_NAME"),
		Logger:     logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build connector spec store: %w", err)
	}
	opts = append(opts, runtime.WithConnectorSpecStore(connectorSpecStore))

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		return nil, nil, fmt.Errorf("load feature flags: %w", err)
	}
	opts = append(opts, runtime.WithFeatureRegistry(flagRegistry))

	if volSvcCfg := resolveVolumeServiceConfig(rootCfg, apps, flagRegistry.IsEnabled(volumesFeatureKey)); volSvcCfg != nil {
		svc, err := samapp.CreateVolumeService(volSvcCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("create volume service: %w", err)
		}
		opts = append(opts, runtime.WithVolumes(svc))
	}

	if metrics != nil {
		opts = append(opts, runtime.WithMetrics(metrics))
	}

	return opts, sessionStore, nil
}

func firstAppSessionService(apps []samapp.AppEntry) map[string]any {
	for _, app := range apps {
		if svcCfg, ok := app.AppConfig["session_service"].(map[string]any); ok {
			return svcCfg
		}
	}
	return nil
}

const volumesFeatureKey = "volumes"

const samTemplatesFeatureKey = "sam_templates"

func withFileTools(tools []any, cfg *agent.AgentConfig, logger *slog.Logger) []any {
	tools = awe.EnsureArtifactManagementGroup(tools, cfg.AutoInjectArtifactTools, cfg.Name, logger)
	return awe.EnsureDataAnalysisGroup(tools, cfg.AutoInjectDataAnalysisTools, cfg.Name, logger)
}

func horizontalSkillRefs(reg *features.Registry) []agent.SkillRef {
	refs := []agent.SkillRef{
		{Name: "sam-build-redirect"},
		{Name: "sam-scheduling"},
	}
	if reg != nil && reg.IsEnabled(samTemplatesFeatureKey) {
		refs = append(refs, agent.SkillRef{Name: "sam-templates"})
	}
	return refs
}

func withHorizontalSkills(refs []agent.SkillRef, reg *features.Registry) []agent.SkillRef {
	for _, h := range horizontalSkillRefs(reg) {
		if !slices.ContainsFunc(refs, func(r agent.SkillRef) bool { return r.Name == h.Name }) {
			refs = append(refs, h)
		}
	}
	return refs
}

func resolveVolumeServiceConfig(rootCfg map[string]any, apps []samapp.AppEntry, volumesEnabled bool) map[string]any {
	for _, app := range apps {
		if svcCfg, ok := app.AppConfig["volume_service"].(map[string]any); ok {
			if samapp.VolumeServiceConfigEnabled(svcCfg) {
				return withRootVolumeListDatabase(svcCfg, rootCfg)
			}
			return nil
		}
	}
	if svcCfg, ok := rootCfg["volume_service"].(map[string]any); ok {
		if samapp.VolumeServiceConfigEnabled(svcCfg) {
			return withRootVolumeListDatabase(svcCfg, rootCfg)
		}
		return nil
	}
	if volumesEnabled {
		if root, ok := rootCfg[config.KeyVolumeRoot].(string); ok && root != "" {
			return withRootVolumeListDatabase(map[string]any{"type": "filesystem", "root": root}, rootCfg)
		}
	}
	return nil
}

func withRootVolumeListDatabase(svcCfg, rootCfg map[string]any) map[string]any {
	if own, _ := svcCfg["database_url"].(string); strings.TrimSpace(own) != "" {
		return svcCfg
	}
	rootDatabaseURL, _ := rootCfg[config.KeyVolumeDBURL].(string)
	if rootDatabaseURL == "" {
		return svcCfg
	}
	withDatabase := maps.Clone(svcCfg)
	withDatabase["database_url"] = rootDatabaseURL
	return withDatabase
}

func MergeBundledAWEApps(ctx context.Context, loader *config.YAMLLoader, appConfigs []samapp.AppEntry, bundled map[string][]byte, logger *slog.Logger) ([]samapp.AppEntry, error) {
	merged, bundledToolDefaults, err := samapp.MergeBundledAppEntries(ctx, loader, appConfigs, bundled)
	if err != nil {
		return nil, fmt.Errorf("merging bundled configs: %w", err)
	}
	awe.MergeToolDefaults(bundledToolDefaults)

	if len(merged) == 0 {
		return nil, fmt.Errorf("no app configurations found in config files")
	}
	logger.Info("loaded app configs", "count", len(merged))
	return merged, nil
}
