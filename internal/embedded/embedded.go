// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	platformadmin "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/proxy/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/runner"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sandbox"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

const localhostIP = "localhost"

const (
	defaultNetworkBrokerPort = 55554
	allInterfacesBindHost    = ""
)

const strRuntimeSubdir = "str-runtime"

type Config struct {
	ConfigPaths []string

	SkipPatterns []string

	Logger *slog.Logger

	LogWriter io.Writer

	GatewayPort int

	PlatformPort int

	GatewayBindHost string

	StaticFS fs.FS

	SystemEnv bool

	NetworkBrokerPort int

	Extensions ExtensionProvider

	TestBrokerDecorator func(broker.Broker) broker.Broker

	DefaultConfigs map[string][]byte

	AdditionalConfigs map[string][]byte

	SandboxMode settings.SandboxMode

	ContainerRuntime *sandbox.Runtime

	SandboxDir string

	ProgressCallback func(msg string)

	HealthAddr string

	DebugSubscribeTopics []string

	Activation entrypoint.ActivationGate

	DebugSubscribeFile string
}

type Instance struct {
	cfg    Config
	logger *slog.Logger

	sharedBroker broker.Broker

	devBroker *broker.DevBroker

	usingDevBroker bool

	netBroker *broker.DevBrokerServer

	rt *runtime.Runtime

	featureRegistryOpts []features.RegistryOption

	healthServer *runtime.ManagementServer

	sandboxOrch *sandbox.Orchestrator

	launchContainerSTRWorkersFn func(ctx context.Context, strEntries []samapp.AppEntry) error

	gatewayAddr atomic.Value

	platformAddr atomic.Value

	cancel context.CancelFunc
	done   chan struct{}

	pprofShutdown func(context.Context)

	diagStop func()

	runtimeStopperSpawned bool
	bindings              *bindings.BindingsRegistry
	leader                *leader.Elector
}

func New(cfg Config) *Instance {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Instance{
		cfg:    cfg,
		logger: logger.With("logger_name", "embedded"),
		done:   make(chan struct{}),
	}
}

func (inst *Instance) progress(msg string) {
	inst.logger.Info(msg)
	if inst.cfg.ProgressCallback != nil {
		inst.cfg.ProgressCallback(msg)
	}
}

func (inst *Instance) loadAllConfigs(ctx context.Context) (map[string]any, error) {
	var configFiles []string
	if len(inst.cfg.ConfigPaths) > 0 {
		discovered, err := runner.DiscoverConfigFiles(inst.cfg.ConfigPaths, inst.cfg.SkipPatterns)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) || len(inst.cfg.DefaultConfigs) == 0 {
				return nil, fmt.Errorf("discover config files: %w", err)
			}
			inst.logger.Info("config path not found, falling back to built-in defaults", "error", err)
		} else {
			configFiles = discovered
		}
	}

	loader := &config.YAMLLoader{}
	var fullConfig map[string]any

	if len(inst.cfg.DefaultConfigs) > 0 {
		inst.logger.Info("loading built-in default configs", "count", len(inst.cfg.DefaultConfigs))
		for name, data := range inst.cfg.DefaultConfigs {
			cfg, loadErr := loader.LoadBytes(ctx, data)
			if loadErr != nil {
				return nil, fmt.Errorf("loading default config %s: %w", name, loadErr)
			}
			fullConfig = mergeLoadedConfig(fullConfig, cfg)
		}
	}

	if len(configFiles) > 0 {
		inst.logger.Info("discovered config files", "count", len(configFiles))
		for _, cfgFile := range configFiles {
			cfg, loadErr := loader.Load(ctx, cfgFile)
			if loadErr != nil {
				return nil, fmt.Errorf("loading config %s: %w", cfgFile, loadErr)
			}
			fullConfig = mergeLoadedConfig(fullConfig, cfg)
		}
	}

	if len(configFiles) == 0 && len(inst.cfg.DefaultConfigs) == 0 && len(inst.cfg.AdditionalConfigs) == 0 {
		return nil, fmt.Errorf("no configuration files found")
	}

	if len(inst.cfg.AdditionalConfigs) > 0 {
		inst.logger.Info("loading additional configs", "count", len(inst.cfg.AdditionalConfigs))
		for name, data := range inst.cfg.AdditionalConfigs {
			if _, dup := inst.cfg.DefaultConfigs[name]; dup {
				continue
			}
			cfg, loadErr := loader.LoadBytes(ctx, data)
			if loadErr != nil {
				return nil, fmt.Errorf("loading additional config %s: %w", name, loadErr)
			}
			fullConfig = mergeLoadedConfig(fullConfig, cfg)
		}
	}

	if fullConfig == nil {
		return nil, fmt.Errorf("no configuration loaded")
	}

	dedupeAppsByName(fullConfig)
	return fullConfig, nil
}

func dedupeAppsByName(cfg map[string]any) {
	apps, _ := cfg["apps"].([]any)
	if len(apps) < 2 {
		return
	}
	seen := make(map[string]struct{}, len(apps))
	keep := make([]bool, len(apps))
	for i := len(apps) - 1; i >= 0; i-- {
		m, ok := apps[i].(map[string]any)
		if !ok {
			keep[i] = true
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			keep[i] = true
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		keep[i] = true
	}
	out := apps[:0]
	for i, a := range apps {
		if keep[i] {
			out = append(out, a)
		}
	}
	cfg["apps"] = out
}

func mergeLoadedConfig(full map[string]any, cfg config.Config) map[string]any {
	cfgMap := config.AsMap(cfg)
	if cfgMap == nil {
		cfgMap = cfgToMap(cfg)
	}
	if cfgMap == nil {
		return full
	}
	if full == nil {
		return cfgMap
	}
	return mergeConfig(full, cfgMap)
}

func (inst *Instance) appendDiscoveredSTRTools(classified *EmbeddedApps, namespace string) {
	if v := os.Getenv("SAM_DISABLE_EMBEDDED_STR"); v == "1" || strings.EqualFold(v, "true") {
		inst.logger.Info("embedded STR disabled via SAM_DISABLE_EMBEDDED_STR")
		return
	}

	toolsDir := os.Getenv("SAM_TOOLS_DIR")
	skillsDir := os.Getenv("SAM_SKILLS_DIR")
	dataDir := os.Getenv("SAM_DATA_DIR")
	platformPackagesDir := embeddedPlatformPackagesDir(namespace)
	platformAlwaysBroughtUp := true

	envSkillsDir := skillsDir
	platformSkillsDir := embeddedPlatformSkillsDir(namespace)

	var discoveredPaths []string
	var toolCount, skillToolCount int

	if toolsDir != "" {
		paths := str.EnumerateToolManifests(toolsDir, inst.logger)
		discoveredPaths = append(discoveredPaths, paths...)
		toolCount = len(paths)
	}

	generatedManifestDir := embeddedGeneratedManifestDir(namespace)
	for _, dir := range uniqueNonEmpty(envSkillsDir, platformSkillsDir) {
		discovered := str.DiscoverSkills(dir, generatedManifestDir, namespace, inst.logger)
		if discovered.ManifestPath != "" {
			discoveredPaths = append(discoveredPaths, discovered.ManifestPath)
			skillToolCount++
		}
	}

	builtinSkillsDir := ""
	if platformSkillsDir != "" {
		if envSkillsDir != "" && envSkillsDir != platformSkillsDir {
			builtinSkillsDir = envSkillsDir
		}
		skillsDir = platformSkillsDir
	}

	if len(discoveredPaths) == 0 && skillsDir == "" && !platformAlwaysBroughtUp {
		return
	}

	if len(classified.STR) > 0 {
		augmentSTRAppSources(inst.logger, classified.STR[0].AppConfig, discoveredPaths, skillsDir)
		if cur, ok := classified.STR[0].AppConfig[config.KeyBuiltinToolsDir].(string); toolsDir != "" && (!ok || cur == "") {
			classified.STR[0].AppConfig[config.KeyBuiltinToolsDir] = toolsDir
		}
		if builtinSkillsDir != "" {
			if cur, ok := classified.STR[0].AppConfig["builtin_skills_dir"].(string); !ok || cur == "" {
				classified.STR[0].AppConfig["builtin_skills_dir"] = builtinSkillsDir
			}
		}
		if platformAlwaysBroughtUp && platformPackagesDir != "" {
			injectEmbeddedToolsetsDir(inst.logger, classified.STR[0].AppConfig, platformPackagesDir)
		}
		inst.logger.Info("merged discovered STR tools into existing worker",
			"worker", classified.STR[0].Name,
			"toolSources", toolCount,
			"skillToolSources", skillToolCount,
			"skillsDir", skillsDir,
			"toolsetsDir", classified.STR[0].AppConfig["toolsets_dir"])
		return
	}

	appConfig := map[string]any{
		"namespace": namespace,
		"worker_id": "tools-worker",
		"sandbox":   map[string]any{"mode": "direct"},
	}
	if len(discoveredPaths) > 0 {
		appConfig["manifest_paths"] = stringSliceToAny(discoveredPaths)
	}
	if toolsDir != "" {
		appConfig[config.KeyBuiltinToolsDir] = toolsDir
	}
	if skillsDir != "" {
		appConfig["skills_dir"] = skillsDir
	}
	if builtinSkillsDir != "" {
		appConfig["builtin_skills_dir"] = builtinSkillsDir
	}
	if platformAlwaysBroughtUp && platformPackagesDir != "" {
		injectEmbeddedToolsetsDir(inst.logger, appConfig, platformPackagesDir)
	}
	if dataDir != "" {
		appConfig["artifact_service"] = map[string]any{
			"type":      "filesystem",
			"base_path": filepath.Join(dataDir, "artifacts"),
		}
	}
	if root := resolveEmbeddedVolumeRoot(); root != "" {
		appConfig["volume_root"] = root
	}

	classified.STR = append(classified.STR, samapp.AppEntry{
		Name:      "str_tools",
		AppConfig: appConfig,
	})

	inst.logger.Info("discovered STR tools",
		"toolSources", toolCount,
		"skillToolSources", skillToolCount,
		"skillsDir", skillsDir,
		"toolsetsDir", appConfig["toolsets_dir"])
}

func embeddedDataBase() string {
	if base := os.Getenv("SAM_DATA_DIR"); base != "" {
		return base
	}
	abs, err := filepath.Abs("./sam-data")
	if err != nil {
		return ""
	}
	return abs
}

func embeddedPlatformSkillsDir(namespace string) string {
	base := embeddedDataBase()
	if base == "" {
		return ""
	}
	if namespace == "" {
		return filepath.Join(base, strRuntimeSubdir, "skills")
	}
	return filepath.Join(base, namespace, strRuntimeSubdir, "skills")
}

func embeddedGeneratedManifestDir(namespace string) string {
	base := embeddedDataBase()
	if base == "" {
		return ""
	}
	if namespace == "" {
		return filepath.Join(base, strRuntimeSubdir, "skill-manifests")
	}
	return filepath.Join(base, namespace, strRuntimeSubdir, "skill-manifests")
}

func embeddedPlatformPackagesDir(namespace string) string {
	base := embeddedDataBase()
	if base == "" {
		return ""
	}
	if namespace == "" {
		return filepath.Join(base, strRuntimeSubdir, "toolsets")
	}
	return filepath.Join(base, namespace, strRuntimeSubdir, "toolsets")
}

func injectEmbeddedToolsetsDir(logger *slog.Logger, appConfig map[string]any, dir string) {
	if appConfig == nil || dir == "" {
		return
	}
	for _, key := range []string{"toolsets_dir", "packages_dir", "tools_dir"} {
		cur, ok := appConfig[key].(string)
		if ok && cur != "" && filepath.Clean(cur) != filepath.Clean(dir) && logger != nil {
			logger.Warn("embedded: overriding STR toolset directory with the platform write path",
				"configKey", key, "configValue", cur, "using", dir)
		}
		delete(appConfig, key)
	}
	appConfig["toolsets_dir"] = dir
}

func resolveEmbeddedVolumeRoot() string {
	if v := os.Getenv("SAM_VOLUME_ROOT"); v != "" {
		return v
	}
	if dataDir := os.Getenv("SAM_DATA_DIR"); dataDir != "" {
		return filepath.Join(dataDir, "volumes")
	}
	if home, err := settings.ResolveHomeDir(); err == nil {
		return filepath.Join(home, "volumes")
	}
	return ""
}

func embeddedVolumeRootDefault() string {
	if os.Getenv("SAM_VOLUME_ROOT") != "" {
		return ""
	}
	return resolveEmbeddedVolumeRoot()
}

func shouldDefaultEmbeddedDevMode() bool {
	return os.Getenv("SOLACE_DEV_MODE") == "" && os.Getenv("SOLACE_BROKER_URL") == ""
}

func augmentSTRAppSources(logger *slog.Logger, appConfig map[string]any, discoveredPaths []string, skillsDir string) {
	if appConfig == nil {
		return
	}

	seen := map[string]struct{}{}
	existing := extractStringSlice(appConfig["manifest_paths"])
	for _, p := range existing {
		seen[p] = struct{}{}
	}

	for _, p := range discoveredPaths {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		existing = append(existing, p)
		seen[p] = struct{}{}
	}

	if len(existing) > 0 {
		appConfig["manifest_paths"] = stringSliceToAny(existing)
	}

	if skillsDir != "" {
		if cur, ok := appConfig["skills_dir"].(string); ok && cur != "" && filepath.Clean(cur) != filepath.Clean(skillsDir) && logger != nil {
			logger.Warn("embedded: overriding STR skills directory with the platform write path",
				"configValue", cur, "using", skillsDir)
		}
		appConfig["skills_dir"] = skillsDir
	}
}

func stringSliceToAny(in []string) []any {
	out := make([]any, len(in))
	for i, p := range in {
		out[i] = p
	}
	return out
}

func extractStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return append([]string(nil), s...)
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func (inst *Instance) startSharedBroker(ctx context.Context, classified EmbeddedApps) error {
	brokerCfg := classified.firstBrokerConfig()
	if samapp.IsDevMode(brokerCfg) {
		devBrk := broker.NewDevBroker()
		if err := devBrk.Connect(ctx); err != nil {
			return fmt.Errorf("connect dev broker: %w", err)
		}
		inst.devBroker = devBrk
		inst.sharedBroker = observability.NewInstrumentedBroker(devBrk)
		inst.usingDevBroker = true
		inst.logger.Info("using in-memory dev broker")
	} else {
		extBroker, err := samapp.CreateBrokerFromConfig(brokerCfg, inst.logger, "sam-embedded")
		if err != nil {
			return fmt.Errorf("create external broker: %w", err)
		}
		if err := extBroker.Connect(ctx); err != nil {
			return fmt.Errorf("connect external broker: %w", err)
		}
		inst.sharedBroker = extBroker
		redactedURL := "[redacted]"
		if raw, ok := brokerCfg["broker_url"].(string); ok && raw != "" {
			if u, err := neturl.Parse(raw); err == nil {
				redactedURL = u.Redacted()
			}
		}
		inst.logger.Info("using external Solace broker", "url", redactedURL)
	}

	if inst.cfg.TestBrokerDecorator != nil {
		inst.sharedBroker = inst.cfg.TestBrokerDecorator(inst.sharedBroker)
	}

	if inst.usingDevBroker {
		if err := inst.startNetworkBroker(); err != nil {
			return fmt.Errorf("start network broker: %w", err)
		}
	}
	return nil
}

func (inst *Instance) registerSTRWorkers(ctx context.Context, strEntries []samapp.AppEntry) error {
	useContainer := inst.cfg.SandboxMode == settings.SandboxModeContainer && len(strEntries) > 0
	if useContainer {
		inst.progress("Preparing sandbox container...")
		if err := inst.setupContainerSTR(ctx, strEntries); err != nil {
			inst.logger.Warn("container STR setup failed, falling back to builtin mode", "error", err)
			useContainer = false
		}
	}
	if useContainer {
		return nil
	}
	for i, strEntry := range strEntries {
		worker, err := inst.newSTRWorker(strEntry)
		if err != nil {
			return fmt.Errorf("create STR worker %q: %w", strEntry.Name, err)
		}
		inst.rt.Register(fmt.Sprintf("str-%d", i), worker)
	}
	return nil
}

func (inst *Instance) newSTRWorker(entry samapp.AppEntry) (*str.Worker, error) {
	worker, err := bootstrap.NewSTRWorker(inst.rt.Services(), config.FromMap(entry.AppConfig))
	if err != nil {
		return nil, err
	}
	return worker, nil
}

func (inst *Instance) newAWEExecutor(fullConfig map[string]any) *awe.Executor {
	return bootstrap.NewAWEExecutor(inst.rt.Services(), bootstrap.WithAWEBaseConfig(fullConfig))
}

func portCheckCommand(addr string) string {
	return portCheckCommandFor(goruntime.GOOS, addr)
}

func portCheckCommandFor(goos, addr string) string {
	if goos == "windows" {
		return "'netstat -ano | findstr " + addr + "'"
	}
	return "'lsof -i " + addr + "'"
}

func isLoopbackListenHost(listenHost string) bool {
	if listenHost == "" {
		return true
	}
	if listenHost == "127.0.0.1" || listenHost == "::1" || listenHost == "localhost" {
		return true
	}
	ip := net.ParseIP(listenHost)
	return ip != nil && ip.IsLoopback()
}

func (inst *Instance) startHealthServer() {
	healthAddr := cmp.Or(inst.cfg.HealthAddr, os.Getenv("HEALTH_ADDR"), net.JoinHostPort(inst.embeddedBindHost(), "8090"))
	if healthAddr == "disabled" {
		return
	}
	ms := runtime.NewManagementServer(inst.rt, healthAddr, inst.logger)
	bootstrap.MountMetricsOnManagement(ms, inst.rt, inst.logger)
	addr, err := ms.Start()
	if err != nil {
		inst.logger.Error(
			"management endpoint failed to bind; a stale sam process may be holding the port. Continuing startup with health probes disabled",
			"addr", healthAddr,
			"error", err,
			"hint", fmt.Sprintf("run %s to check what is bound to %s", portCheckCommand(healthAddr), healthAddr))
		return
	}
	inst.healthServer = ms
	inst.logger.Info("management server listening", "addr", addr)
}

func (inst *Instance) initAndStartRuntime(runCtx context.Context, cancel context.CancelFunc) error {
	cleanup := func() {
		cancel()
		if inst.healthServer != nil {
			_ = inst.healthServer.Stop(context.Background())
		}
	}
	if err := inst.rt.Init(runCtx); err != nil {
		cleanup()
		return fmt.Errorf("init: %w", err)
	}
	if err := inst.rt.Start(runCtx); err != nil {
		cleanup()
		return fmt.Errorf("start: %w", err)
	}
	return nil
}

func (inst *Instance) buildRuntime(ctx context.Context, fullConfig map[string]any, classified EmbeddedApps, namespace string) error {
	var runtimeOpts []runtime.Option
	var aweSessionStore session.Store
	var err error

	sweepGatewayVolumes := false

	switch {
	case runtimeNeedsAWEExecutor(classified):
		runtimeOpts, aweSessionStore, err = bootstrap.BuildAWERuntimeOptions(ctx, fullConfig, classified.AWE, inst.logger)
		if err != nil {
			return fmt.Errorf("build AWE runtime options: %w", err)
		}

		runtimeOpts = append(runtimeOpts, bootstrap.EmbeddedUsageReporterOptions(ctx, classified.Gateway, inst.logger)...)
		if aweSessionStore == nil {
			aweSessionStore = session.NewMemoryStore()
			runtimeOpts = append(runtimeOpts, runtime.WithSessions(aweSessionStore))
			inst.logger.Warn("no session_service configured; using in-memory session store (history will not survive restart)")
		}
		for _, gw := range classified.Gateway {
			if gw.ModelRaw == nil {
				continue
			}
			llmOpts, llmErr := bootstrap.BuildLLMOptions(ctx, gw.ModelRaw, inst.logger)
			if llmErr != nil {
				return fmt.Errorf("build gateway LLM options for shared runtime: %w", llmErr)
			}
			runtimeOpts = append(runtimeOpts, llmOpts...)
			break
		}
		if !anyAppHasArtifactService(classified.AWE) {
			for _, gw := range classified.Gateway {
				svcCfg, ok := gw.AppConfig["artifact_service"].(map[string]any)
				if !ok {
					continue
				}
				if svc := samapp.CreateArtifactService(svcCfg, gw.AppConfig); svc != nil {
					runtimeOpts = append(runtimeOpts, runtime.WithArtifacts(svc))
					inst.logger.Info("shared runtime artifact service sourced from gateway config (no static AWE app declared one)")
				}
				break
			}
		}
		sweepGatewayVolumes = true
	case len(classified.Gateway) > 0:
		runtimeOpts, err = bootstrap.BuildGatewayRuntimeOptions(ctx, fullConfig, classified.Gateway, inst.logger,
			bootstrap.WithFeatureRegistryOptions(inst.featureRegistryOpts...))
		if err != nil {
			return fmt.Errorf("build gateway runtime options: %w", err)
		}
	default:
		runtimeOpts = []runtime.Option{runtime.WithLogger(inst.logger)}
	}

	aweRegisteredVolumes := false
	for _, app := range classified.AWE {
		if _, ok := app.AppConfig["volume_service"].(map[string]any); ok {
			aweRegisteredVolumes = true
			break
		}
	}
	if sweepGatewayVolumes && !aweRegisteredVolumes {
		for _, app := range classified.Gateway {
			svcCfg, ok := app.AppConfig["volume_service"].(map[string]any)
			if !ok {
				continue
			}
			if !samapp.VolumeServiceConfigEnabled(svcCfg) {
				break
			}
			svc, err := samapp.CreateVolumeService(svcCfg)
			if err != nil {
				return fmt.Errorf("create volume service for gateway app %q: %w", app.Name, err)
			}
			runtimeOpts = append(runtimeOpts, runtime.WithVolumes(svc))
			break
		}
	}

	var extensionComp runtime.Component
	if inst.cfg.Extensions != nil {
		entOpts, comp, entErr := inst.cfg.Extensions.Bootstrap(ctx, fullConfig, aweSessionStore, inst.sharedBroker, inst.logger)
		if entErr != nil {
			return fmt.Errorf("extensions bootstrap: %w", entErr)
		}
		runtimeOpts = append(runtimeOpts, entOpts...)
		extensionComp = comp
		inst.logger.Info("extensions initialized")
	}
	runtimeOpts = append(runtimeOpts, bootstrap.BudgetEnforcement(inst.sharedBroker, namespace, inst.logger))

	bindingsReg := bootstrap.NewBindingsRegistry(inst.sharedBroker, namespace, inst.logger)
	inst.bindings = bindingsReg
	runtimeOpts = append(runtimeOpts, runtime.WithBindings(bindingsReg))

	inst.rt = runtime.NewRuntime(inst.sharedBroker, nil, runtimeOpts...)

	if extensionComp != nil {
		inst.rt.Register("extensions", extensionComp)
	}
	inst.rt.Register("control", control.NewService(inst.rt, namespace))
	inst.leader = bootstrap.NewLeader(inst.rt.Services(), namespace, leader.RoleGateway, inst.logger)
	inst.rt.Register("model-bindings", bindings.NewWatcher(bindingsReg, inst.sharedBroker, namespace, inst.logger))
	return nil
}

func resolveBindHost(override string, httpCfg *samapp.HTTPAdapterConfig) string {
	host := override
	if host == "" && httpCfg != nil {
		host = httpCfg.Host
	}
	if host == "" || host == "localhost" {
		return "127.0.0.1"
	}
	return host
}

func (inst *Instance) embeddedBindHost() string {
	return resolveBindHost(inst.cfg.GatewayBindHost, nil)
}

func (inst *Instance) registerGatewayPlatformProxy(classified EmbeddedApps, namespace string) (*entrypoint.GatewayProxy, error) {
	if inst.cfg.Activation != nil {
		if app, ok := gatewayRequiringSignIn(classified.Gateway); ok {
			return nil, fmt.Errorf("gateway %q has frontend_use_authorization on, which the activation gate would refuse before anyone could sign in to activate", app.Name)
		}
	}
	platformPort := cmp.Or(inst.cfg.PlatformPort, 8001)

	gweExe, err := inst.registerGatewayExecutor(classified)
	if err != nil {
		return nil, err
	}

	platformCore, platformComponent, platformAppConfig := inst.buildPlatformCore(classified, namespace, platformPort, gweExe)
	if gweExe != nil && platformCore != nil {
		gweExe.SetDeployedEntrypoints(platformadmin.EntrypointReaders(platformCore.DB, namespace, inst.logger))
		gweExe.SetTargetLookup(platformadmin.TargetLookup(platformCore.DB, inst.logger))
	}
	var notifyCore *notify.NotifyCore
	if inst.notifyEnabled() {
		notifyCore = inst.buildNotifyCore(classified, namespace)
	}

	var gweProxy *entrypoint.GatewayProxy
	if gweExe != nil {
		httpCfg := samapp.FirstHTTPAdapterConfig(classified.Gateway)
		yamlPort := 0
		if httpCfg != nil {
			yamlPort = httpCfg.Port
		}
		gatewayPort := cmp.Or(inst.cfg.GatewayPort, yamlPort, 8800)
		bindHost := resolveBindHost(inst.cfg.GatewayBindHost, httpCfg)
		proxyOpts := bootstrap.ProxyOpts{
			ListenAddr: net.JoinHostPort(bindHost, strconv.Itoa(gatewayPort)),
			StaticFS:   inst.cfg.StaticFS,
		}
		if certFile, keyFile, keyPassword, tlsAddr := samapp.ResolveProxyTLS(httpCfg); certFile != "" {
			proxyOpts.TLS = &entrypoint.ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile, Password: keyPassword}
			if tlsAddr != "" {
				if _, tlsPort, err := net.SplitHostPort(tlsAddr); err == nil {
					tlsAddr = net.JoinHostPort(bindHost, tlsPort)
				}
			}
			proxyOpts.TLSAddr = tlsAddr
		}
		inst.configureToolAuthRedirectURI(bindHost, gatewayPort, proxyOpts.TLS != nil, proxyOpts.TLSAddr != "")
		gweProxy = bootstrap.NewGatewayProxy(proxyOpts, inst.logger)
		if inst.cfg.Activation != nil {
			gweProxy.SetRequestGate(inst.cfg.Activation.Gate)
		}

		if err := entrypoint.MountExecutor(gweProxy, gweExe); err != nil {
			return nil, fmt.Errorf("mount gateway executor: %w", err)
		}

		for _, gwCore := range gweExe.Gateways() {
			inst.registerMCPMetadataRoutes(gweProxy, gwCore)
		}

		inst.wireRuntimeMCPMetadata(gweProxy, gweExe)

		bootstrap.MountPlatform(gweProxy, platformCore)
		if notifyCore != nil {
			bootstrap.MountNotify(gweProxy, notifyCore)
		}

		for _, gwCore := range gweExe.Gateways() {
			gwCore.SetPlatformMounted(true)
			gwCore.SetNotifyMounted(notifyCore != nil)
		}
	}

	inst.rt.Register("platform", platformComponent)
	if notifyCore != nil {
		inst.rt.Register("notify", notifyCore)
	}
	withheld, withheldErr := bootstrap.WithheldComponents(bootstrap.WithheldDeps{
		PlatformAppConfig: platformAppConfig, Registry: inst.rt.Services().FeatureRegistry(),
		Metrics: inst.rt.Services().Metrics(), Broker: inst.rt.Services().Broker(), Gateway: gweExe,
		Namespace: namespace, Logger: inst.logger, Platform: platformCore, Leader: inst.leader,
	})
	if withheldErr != nil {
		return nil, withheldErr
	}
	for _, c := range withheld {
		inst.rt.Register(c.Name, c.Component)
	}
	if gweProxy != nil {
		inst.rt.Register("proxy", gweProxy)
	}
	return gweProxy, nil
}

func (inst *Instance) configureToolAuthRedirectURI(bindHost string, port int, tlsEnabled, dualPortTLS bool) {
	if os.Getenv("OAUTH_TOOL_REDIRECT_URI") != "" || os.Getenv("FRONTEND_REDIRECT_URI") != "" {
		return
	}
	if !isLoopbackListenHost(bindHost) {
		return
	}
	scheme := "http"
	if tlsEnabled && !dualPortTLS {
		scheme = "https"
	}
	base := scheme + "://" + net.JoinHostPort(bindHost, strconv.Itoa(port))
	_ = os.Setenv("FRONTEND_REDIRECT_URI", base)
	inst.logger.Info("tool OAuth loopback callback pinned to embedded gateway", "redirectBaseURL", base)
}

func (inst *Instance) wireRuntimeMCPMetadata(proxy *entrypoint.GatewayProxy, exe *entrypoint.GatewayExecutor) {
	baseReady := exe.OnGatewayReady
	exe.OnGatewayReady = func(name string, h http.Handler) {
		if baseReady != nil {
			baseReady(name, h)
		}
		for _, gwCore := range exe.Gateways() {
			if gwCore.GatewayID() == name {
				inst.registerMCPMetadataRoutes(proxy, gwCore)
				return
			}
		}
		inst.logger.Warn("gateway ready but no core matched its id; MCP metadata route not registered",
			"gatewayID", name)
	}
	baseRemoved := exe.OnGatewayRemoved
	exe.OnGatewayRemoved = func(name string) {
		proxy.UnregisterMCPMetadataRoutes(name)
		if baseRemoved != nil {
			baseRemoved(name)
		}
	}
}

func (inst *Instance) registerMCPMetadataRoutes(proxy *entrypoint.GatewayProxy, core *entrypoint.GatewayCore) {
	mcpAdapter, ok := core.Adapter().(*mcp.Adapter)
	if !ok {
		return
	}
	proxy.RegisterMCPMetadataRoutes(core.GatewayID(), mcpAdapter.MetadataHandler())
	inst.logger.Info("registered MCP metadata routes",
		"gatewayID", core.GatewayID(),
		"protectedResource", mcpauth.PathProtectedResource+"/gw/"+core.GatewayID()+"/...",
		"authorizationServer", mcpauth.PathWellKnown+"/gw/"+core.GatewayID(),
	)
}

func (inst *Instance) registerGatewayExecutor(classified EmbeddedApps) (*entrypoint.GatewayExecutor, error) {
	if len(classified.Gateway) == 0 {
		return nil, nil
	}
	for i := range classified.Gateway {
		gweEntry := &classified.Gateway[i]
		if i == 0 && inst.cfg.GatewayPort != 0 {
			gweEntry.AppConfig["fastapi_port"] = inst.cfg.GatewayPort
		}
		if _, present := gweEntry.AppConfig["volumes_allow_host_paths"]; !present {
			httpCfg := samapp.FirstHTTPAdapterConfig(classified.Gateway)
			loopback := isLoopbackListenHost(resolveBindHost(inst.cfg.GatewayBindHost, httpCfg))
			gweEntry.AppConfig["volumes_allow_host_paths"] = loopback
			inst.logger.Info("volume host-path posture",
				"volumesAllowHostPaths", loopback,
				"loopbackListen", loopback,
				"explanation", "embedded default — non-loopback bind requires explicit YAML opt-in")
		}
		if inst.cfg.SandboxMode == settings.SandboxModeContainer {
			if _, present := gweEntry.AppConfig["volumes_require_container_mount_for_linked"]; !present {
				gweEntry.AppConfig["volumes_require_container_mount_for_linked"] = true
			}
		}
	}

	gweExe, err := bootstrap.NewGatewayExecutor(inst.rt.Services(), classified.Gateway)
	if err != nil {
		return nil, fmt.Errorf("create gateway executor: %w", err)
	}
	var hooks *entrypoint.GatewayHooks
	if inst.cfg.Extensions != nil {
		for _, gwCore := range gweExe.Gateways() {
			if err := inst.cfg.Extensions.ConfigureGateway(gwCore, gwCore.RawConfig(), inst.logger); err != nil {
				return nil, fmt.Errorf("enterprise gateway setup: %w", err)
			}
		}
		hooks = inst.cfg.Extensions.GatewayHooks()
	}
	if inst.cfg.Activation != nil {
		if hooks == nil {
			hooks = &entrypoint.GatewayHooks{}
		}
		hooks.Activation = inst.cfg.Activation
		inst.fileProductAnalyticsUnderAcceptedAccount()
	}
	if hooks != nil {
		gweExe.SetGatewayHooks(hooks)
	}
	inst.rt.Register("gateway", gweExe)
	return gweExe, nil
}

func gatewayRequiringSignIn(apps []samapp.AppEntry) (samapp.AppEntry, bool) {
	for _, app := range apps {
		if on, _ := app.AppConfig["frontend_use_authorization"].(bool); on {
			return app, true
		}
	}
	return samapp.AppEntry{}, false
}

func (inst *Instance) fileProductAnalyticsUnderAcceptedAccount() {
	usage, ok := inst.rt.Services().UsageReporter().(*productanalytics.Service)
	if !ok {
		return
	}
	gate := inst.cfg.Activation
	usage.FileUnderAccounts(func() (productanalytics.Account, bool) {
		subject, email, accepted := gate.AcceptedAccount()
		return productanalytics.Account{Subject: subject, Email: email}, accepted
	})
}

func (inst *Instance) buildPlatformCore(classified EmbeddedApps, namespace string, platformPort int, gweExe *entrypoint.GatewayExecutor) (*platform.PlatformCore, runtime.Component, map[string]any) {
	platformAppConfig := map[string]any{
		"namespace":    namespace,
		"fastapi_host": localhostIP,
		"fastapi_port": platformPort,
	}
	if len(classified.Platform) > 0 {
		platformAppConfig = classified.Platform[0].AppConfig
		if platformAppConfig["namespace"] == nil {
			platformAppConfig["namespace"] = namespace
		}
	}
	var platformExt platform.ExtensionPoints
	if inst.cfg.Extensions != nil {
		if gr, ok := inst.cfg.Extensions.(GatewayReceiver); ok && gweExe != nil {
			gr.SetInProcessGateway(gweExe)
		}
		if pp, ok := inst.cfg.Extensions.(PlatformProvider); ok {
			platformExt = pp.PlatformExtensions(platformAppConfig, inst.logger)
		}
		if pc, ok := inst.cfg.Extensions.(PlatformConfigurer); ok {
			platformExt.AuthMiddleware = pc.ConfigurePlatformAuth(platformAppConfig, inst.logger)
		}
	}
	core := bootstrap.NewPlatformCore(inst.rt.Services(), platformAppConfig, platformExt)
	core.AllowLoopbackGrantEndpoints()
	retireFreeModel(core)

	var component runtime.Component = core
	if inst.cfg.Extensions != nil {
		if pw, ok := inst.cfg.Extensions.(PlatformWrapper); ok {
			component = pw.WrapPlatformCore(core, inst.logger)
		}
	}
	return core, component, platformAppConfig
}

func (inst *Instance) notifyEnabled() bool {
	reg := inst.rt.Services().FeatureRegistry()
	return reg != nil && reg.IsEnabled("notify_service")
}

func firstNotifyConfig(classified EmbeddedApps) map[string]any {
	for _, app := range classified.Gateway {
		if nc, ok := app.AppConfig["notify"].(map[string]any); ok {
			return nc
		}
	}
	for _, app := range classified.Platform {
		if nc, ok := app.AppConfig["notify"].(map[string]any); ok {
			return nc
		}
	}
	return nil
}

func (inst *Instance) buildNotifyCore(classified EmbeddedApps, namespace string) *notify.NotifyCore {
	dataDir := cmp.Or(os.Getenv("SAM_DATA_DIR"), os.TempDir())
	appConfig := map[string]any{
		"namespace":    namespace,
		"database_url": "sqlite:///" + filepath.Join(dataDir, "sam-notify.db"),
	}
	if nc := firstNotifyConfig(classified); nc != nil {
		appConfig["notify"] = nc
		if dbURL, ok := nc["database_url"].(string); ok && dbURL != "" {
			appConfig["database_url"] = dbURL
		}
		if rd, ok := nc["retention_days"]; ok {
			appConfig["retention_days"] = rd
		}
	}
	var ext notify.NotifyExtensions
	if inst.cfg.Extensions != nil {
		if pc, ok := inst.cfg.Extensions.(PlatformConfigurer); ok {
			authCfg := map[string]any{"namespace": namespace}
			if len(classified.Platform) > 0 {
				if v, has := classified.Platform[0].AppConfig["frontend_use_authorization"]; has {
					authCfg["frontend_use_authorization"] = v
				}
			} else if len(classified.Gateway) > 0 {
				if v, has := classified.Gateway[0].AppConfig["frontend_use_authorization"]; has {
					authCfg["frontend_use_authorization"] = v
				}
			}
			ext.AuthMiddleware = pc.ConfigurePlatformAuth(authCfg, inst.logger)
		}
	}
	return bootstrap.NewNotifyCore(inst.rt.Services(), appConfig, ext)
}

func (inst *Instance) Start(ctx context.Context) error {
	inst.progress("Loading configuration...")

	if !inst.cfg.SystemEnv {
		if envPath := runner.LoadDotEnv(inst.logger); envPath != "" {
			inst.logger.Info("loaded environment", "path", envPath)
		}
	}

	if os.Getenv("SOLACE_BROKER_URL") == "" {
		brokerPort := inst.cfg.NetworkBrokerPort
		if brokerPort == 0 {
			brokerPort = defaultNetworkBrokerPort
		}
		inst.setDevBrokerEnv(brokerPort)
	}

	if root := embeddedVolumeRootDefault(); root != "" {
		_ = os.Setenv("SAM_VOLUME_ROOT", root)
		inst.logger.Info("embedded: defaulting SAM_VOLUME_ROOT to host-writable path", "volumeRoot", root)
	}

	if shouldDefaultEmbeddedDevMode() {
		_ = os.Setenv("SOLACE_DEV_MODE", "true")
		inst.logger.Info("embedded: defaulting SOLACE_DEV_MODE=true (no broker configured)")
	}

	fullConfig, cfgErr := inst.loadAllConfigs(ctx)
	if cfgErr != nil {
		return cfgErr
	}

	logger, cleanup, logErr := samlog.SetupFromConfig(config.FromMap(fullConfig))
	if logErr != nil {
		return fmt.Errorf("setup logging: %w", logErr)
	}
	_ = cleanup
	if inst.cfg.LogWriter != nil {
		extra := samlog.NewHandler(inst.cfg.LogWriter, &slog.HandlerOptions{Level: slog.LevelInfo}, "text")
		logger = slog.New(samlog.NewMultiHandler(logger.Handler(), extra))
	}
	logger = logger.With("component", "sam-embedded")
	inst.logger = logger.With("logger_name", "embedded")
	slog.SetDefault(logger)

	inst.pprofShutdown = runtime.StartPprofServer(inst.logger)
	inst.diagStop = runtime.StartDiagnosticsSignalHandler(inst.logger)

	appsRaw, _ := fullConfig["apps"].([]any)
	classified := ClassifyForEmbedded(appsRaw)

	namespace := inst.resolveNamespace(classified)

	inst.progress("Discovering tools...")
	inst.appendDiscoveredSTRTools(&classified, namespace)

	inst.logger.Info("classified apps",
		"platform", len(classified.Platform),
		"awe", len(classified.AWE),
		"gateway", len(classified.Gateway),
		"str", len(classified.STR),
		"python", len(classified.Python))

	if len(classified.Gateway) == 0 && len(classified.AWE) == 0 && len(classified.STR) == 0 && len(classified.Platform) == 0 {
		return fmt.Errorf("no application configurations found")
	}

	inst.progress("Starting message broker...")
	if brkErr := inst.startSharedBroker(ctx, classified); brkErr != nil {
		return brkErr
	}

	if err := inst.buildRuntime(ctx, fullConfig, classified, namespace); err != nil {
		return err
	}

	gweProxy, err := inst.registerGatewayPlatformProxy(classified, namespace)
	if err != nil {
		return err
	}

	if runtimeNeedsAWEExecutor(classified) {
		exe := inst.newAWEExecutor(fullConfig)
		if _, loadErr := bootstrap.LoadAWEApps(exe, classified.AWE, inst.logger); loadErr != nil {
			return fmt.Errorf("load AWE apps: %w", loadErr)
		}
		inst.rt.Register("awe", exe)
		if len(classified.Platform) > 0 {
			inst.rt.OnStarted(func() { exe.FollowPlatformAgents(ctx, namespace) })
		}
	}

	if err := inst.registerSTRWorkers(ctx, classified.STR); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	inst.cancel = cancel

	inst.startHealthServer()

	if len(inst.cfg.DebugSubscribeTopics) > 0 {
		sub := newDebugSubscriber(
			inst.sharedBroker,
			inst.cfg.DebugSubscribeTopics,
			inst.cfg.DebugSubscribeFile,
			inst.logger,
		)
		inst.rt.Register("debug-subscribe", sub)
	}

	if err := bootstrap.RegisterLeader(inst.rt, inst.leader); err != nil {
		cancel()
		return err
	}

	inst.progress("Starting agents...")
	if err := inst.initAndStartRuntime(runCtx, cancel); err != nil {
		return err
	}

	if gweProxy != nil {
		inst.gatewayAddr.Store(gweProxy.Addr())
		inst.platformAddr.Store(gweProxy.Addr())
	}

	inst.runtimeStopperSpawned = true
	go func() {
		defer close(inst.done)
		<-runCtx.Done()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := inst.rt.Stop(stopCtx); err != nil {
			inst.logger.Warn("runtime stop error", "error", err)
		}
	}()

	if err := inst.launchContainerSTRWorkersIfConfigured(ctx, classified.STR); err != nil {
		return err
	}

	inst.progress("Ready")

	inst.logger.Info("embedded SAM started",
		"gateway", inst.GatewayAddr(),
		"platform", inst.PlatformAddr(),
		"sandboxMode", inst.effectiveSandboxMode(),
		"aweApps", len(classified.AWE),
		"strWorkers", len(classified.STR))

	return nil
}

func (inst *Instance) Stop(ctx context.Context) error {
	if inst.cancel != nil {
		inst.cancel()
	}

	var ctxErr error
	if inst.runtimeStopperSpawned {
		select {
		case <-inst.done:
		case <-ctx.Done():
			ctxErr = ctx.Err()
		}
	}

	cleanupCtx := ctx
	if cleanupCtx.Err() != nil {
		var cancel context.CancelFunc
		cleanupCtx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
	}

	if !inst.runtimeStopperSpawned && inst.rt != nil {
		if err := inst.rt.Stop(cleanupCtx); err != nil {
			inst.logger.Warn("runtime stop error", "error", err)
		}
	}

	if inst.bindings != nil {
		inst.bindings.Close()
	}

	if inst.healthServer != nil {
		_ = inst.healthServer.Stop(cleanupCtx)
	}

	if inst.sandboxOrch != nil {
		if err := inst.sandboxOrch.StopAll(cleanupCtx); err != nil {
			inst.logger.Warn("sandbox STR stop error", "error", err)
		}
	}

	if inst.cfg.Extensions != nil {
		if err := inst.cfg.Extensions.Stop(cleanupCtx); err != nil {
			inst.logger.Warn("enterprise stop error", "error", err)
		}
	}

	if inst.netBroker != nil {
		if err := inst.netBroker.Stop(); err != nil {
			inst.logger.Warn("network broker stop error", "error", err)
		}
	}

	if inst.sharedBroker != nil {
		_ = inst.sharedBroker.Disconnect(cleanupCtx)
	}

	if inst.pprofShutdown != nil {
		inst.pprofShutdown(cleanupCtx)
	}
	if inst.diagStop != nil {
		inst.diagStop()
	}

	inst.logger.Info("embedded SAM stopped")
	return ctxErr
}

func (inst *Instance) GatewayAddr() string {
	if v := inst.gatewayAddr.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (inst *Instance) PlatformAddr() string {
	if v := inst.platformAddr.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (inst *Instance) Ready() bool {
	return inst.rt != nil && inst.rt.Ready()
}

func (inst *Instance) setDevBrokerEnv(port int) {
	portStr := strconv.Itoa(port)
	_ = os.Setenv("DEV_BROKER_HOST", localhostIP)
	_ = os.Setenv("DEV_BROKER_PORT", portStr)
	_ = os.Setenv("SAM_DEV_BROKER_HOST", localhostIP)
	_ = os.Setenv("SAM_DEV_BROKER_PORT", portStr)
}

func (inst *Instance) networkBrokerBindAddr() string {
	bindHost := inst.embeddedBindHost()
	if inst.cfg.SandboxMode == settings.SandboxModeContainer {
		bindHost = allInterfacesBindHost
	}
	return net.JoinHostPort(bindHost, strconv.Itoa(inst.cfg.NetworkBrokerPort))
}

func (inst *Instance) startNetworkBroker() error {
	if inst.devBroker == nil {
		return fmt.Errorf("network broker requires in-memory DevBroker")
	}
	inst.netBroker = broker.NewDevBrokerServer(inst.devBroker, inst.logger)
	addr := inst.networkBrokerBindAddr()
	actualAddr, err := inst.netBroker.Start(addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	if _, portStr, splitErr := net.SplitHostPort(actualAddr); splitErr == nil {
		if p, atoiErr := strconv.Atoi(portStr); atoiErr == nil && p > 0 {
			inst.cfg.NetworkBrokerPort = p
		}
	}

	inst.setDevBrokerEnv(inst.cfg.NetworkBrokerPort)

	inst.logger.Info("network broker started", "addr", actualAddr)
	return nil
}

func (inst *Instance) resolveNamespace(classified EmbeddedApps) string {
	for _, entries := range [][]samapp.AppEntry{classified.Platform, classified.AWE, classified.Gateway, classified.STR} {
		for _, entry := range entries {
			if ns, ok := entry.AppConfig["namespace"].(string); ok && ns != "" {
				return ns
			}
		}
	}
	return "solace-agent-mesh"
}

func cfgToMap(cfg config.Config) map[string]any {
	result := make(map[string]any)
	for _, key := range []string{
		"apps", "log", "shared_config",
		"trust_manager", "agent_identity", "audit_log",
		"providers",
		"session_service",
	} {
		if v := cfg.GetRaw(key); v != nil {
			result[key] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (inst *Instance) effectiveSandboxMode() string {
	if inst.cfg.SandboxMode == settings.SandboxModeContainer {
		return "container"
	}
	return "builtin"
}

func (inst *Instance) setupContainerSTR(ctx context.Context, strEntries []samapp.AppEntry) error {
	rt := inst.cfg.ContainerRuntime
	if rt == nil {
		rt = sandbox.DetectRuntime()
	}
	if rt == nil {
		return fmt.Errorf("sandbox mode is 'container' but no container runtime (docker/podman) was found")
	}

	inst.logger.Info("using container runtime for STR",
		"runtime", rt.Name, "version", rt.Version, "workers", len(strEntries))

	if err := sandbox.EnsurePodmanMachineRunning(ctx, rt, inst.logger); err != nil {
		return fmt.Errorf("ensure podman machine running: %w", err)
	}

	orch := sandbox.NewOrchestrator(rt, inst.logger)

	sandboxDir := inst.cfg.SandboxDir
	if sandboxDir == "" {
		var err error
		sandboxDir, err = sandbox.ResolveSandboxDir()
		if err != nil {
			return fmt.Errorf("resolve sandbox directory: %w (set SAM_SANDBOX_DIR or build with 'make build-sandbox-binaries')", err)
		}
	}
	inst.logger.Info("using sandbox directory", "path", sandboxDir)

	if err := orch.EnsureImage(ctx, sandboxDir, func(msg string) {
		inst.progress("Building Secure Tool Sandbox\n" + msg)
	}); err != nil {
		return fmt.Errorf("ensure STR image: %w", err)
	}

	inst.sandboxOrch = orch
	return nil
}

func (inst *Instance) launchContainerSTRWorkersIfConfigured(ctx context.Context, strEntries []samapp.AppEntry) error {
	if inst.sandboxOrch == nil {
		return nil
	}
	inst.progress("Launching Secure Tool Sandbox\nStarting tool containers...")
	launch := inst.launchContainerSTRWorkers
	if inst.launchContainerSTRWorkersFn != nil {
		launch = inst.launchContainerSTRWorkersFn
	}
	if err := launch(ctx, strEntries); err != nil {
		return fmt.Errorf("launch container STR workers: %w", err)
	}
	return nil
}

func (inst *Instance) launchContainerSTRWorkers(ctx context.Context, strEntries []samapp.AppEntry) error {
	toolsLinuxDir := os.Getenv("SAM_TOOLS_LINUX_DIR")
	if toolsLinuxDir == "" {
		inst.logger.Warn("SAM_TOOLS_LINUX_DIR not set, container STR will have no tools")
	}

	skillsLinuxDir := os.Getenv("SAM_SKILLS_LINUX_DIR")

	hostArtifactDir := ""
	if dataDir := os.Getenv("SAM_DATA_DIR"); dataDir != "" {
		hostArtifactDir = filepath.Join(dataDir, "artifacts")
		_ = os.MkdirAll(hostArtifactDir, 0755)
	}

	hostVolumeRoot := ""
	for _, e := range strEntries {
		if vr, ok := e.AppConfig["volume_root"].(string); ok && vr != "" {
			hostVolumeRoot = vr
			_ = os.MkdirAll(hostVolumeRoot, 0755)
			break
		}
	}

	namespace := inst.resolveNamespace(ClassifyForEmbedded(nil))
	for _, e := range strEntries {
		if ns, ok := e.AppConfig["namespace"].(string); ok && ns != "" {
			namespace = ns
			break
		}
	}

	var manifestPaths []string
	if toolsLinuxDir != "" {
		for _, hostPath := range str.EnumerateToolManifests(toolsLinuxDir, inst.logger) {
			rel, err := filepath.Rel(toolsLinuxDir, hostPath)
			if err != nil {
				continue
			}
			manifestPaths = append(manifestPaths, "/tools/"+filepath.ToSlash(rel))
		}
	}

	appConfig := map[string]any{
		"namespace": namespace,
		"worker_id": "tools-worker",
		"sandbox": map[string]any{
			"mode": "direct",
		},
		"artifact_service": map[string]any{
			"type":      "filesystem",
			"base_path": "/data",
		},
	}
	if len(manifestPaths) > 0 {
		appConfig["manifest_paths"] = manifestPaths
		appConfig[config.KeyBuiltinToolsDir] = "/tools"
	}

	if skillsLinuxDir != "" {
		appConfig["skills_dir"] = "/skills"
	}

	if hostVolumeRoot != "" {
		appConfig["volume_root"] = "/volumes"
	}

	entry := samapp.AppEntry{
		Name:      "str_combined",
		AppConfig: appConfig,
	}

	cfgData, err := marshalSTRConfig(entry)
	if err != nil {
		return fmt.Errorf("marshal combined STR config: %w", err)
	}

	containerCfg := sandbox.STRContainerConfig{
		Name:       "sam-str-0",
		BrokerPort: inst.cfg.NetworkBrokerPort,
		ConfigData: cfgData,
		ToolsDir:   toolsLinuxDir,
		EnvVars: map[string]string{
			"SAM_TOOLS_DIR": "/tools",
			"SAM_DATA_DIR":  "/tmp/sam-str-data",
		},
	}

	if skillsLinuxDir != "" {
		containerCfg.ExtraVolumes = append(containerCfg.ExtraVolumes,
			fmt.Sprintf("%s:/skills", skillsLinuxDir))
		containerCfg.EnvVars["SAM_SKILLS_DIR"] = "/skills"
	}

	if hostArtifactDir != "" {
		containerCfg.ExtraVolumes = append(containerCfg.ExtraVolumes,
			fmt.Sprintf("%s:/data/artifacts", hostArtifactDir))
	}

	if hostVolumeRoot != "" {
		containerCfg.ExtraVolumes = append(containerCfg.ExtraVolumes,
			fmt.Sprintf("%s:/volumes", hostVolumeRoot))
	}

	if _, err := inst.sandboxOrch.LaunchSTR(ctx, containerCfg); err != nil {
		return fmt.Errorf("launch STR container: %w", err)
	}

	inst.logger.Info("launched single STR container",
		"toolsDir", toolsLinuxDir,
		"skillsDir", skillsLinuxDir,
		"namespace", namespace)

	return nil
}

func marshalSTRConfig(entry samapp.AppEntry) ([]byte, error) {
	cfgMap := map[string]any{
		"apps": []any{
			map[string]any{
				"name":       entry.Name,
				"app_config": entry.AppConfig,
			},
		},
	}
	return yaml.Marshal(cfgMap)
}

func mergeConfig(dst, src map[string]any) map[string]any {
	for k, v := range src {
		if k == "apps" {
			dstApps, _ := dst["apps"].([]any)
			srcApps, _ := v.([]any)
			dst["apps"] = append(dstApps, srcApps...)
		} else {
			dst[k] = v
		}
	}
	return dst
}

func uniqueNonEmpty(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
