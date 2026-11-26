// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	platformidentity "github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/builderapply"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samembed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sharing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type configFiles []string

func (c *configFiles) String() string { return fmt.Sprintf("%v", *c) }
func (c *configFiles) Set(value string) error {
	*c = append(*c, value)
	return nil
}

const componentName = "sam-gateway"

func main() {
	logger := samlog.NewStderrLogger(slog.LevelDebug)
	slog.SetDefault(logger)

	if err := run(); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var configs configFiles
	var platformConfigs configFiles
	var listenAddr string
	var staticDir string
	var healthAddr string
	flag.Var(&configs, "config", "YAML config file (can be specified multiple times)")
	flag.Var(&platformConfigs, "platform-config", "Platform YAML config file (optional, runs platform behind GWE proxy)")
	flag.StringVar(&listenAddr, "listen", "", "Proxy listen address (overrides fastapi_host/fastapi_port from YAML; defaults to :8080 when neither is set)")
	flag.StringVar(&staticDir, "static-dir", "", "Static frontend assets directory")
	flag.StringVar(&healthAddr, "health-addr", ":9090", "Address for early liveness/readiness probes (starts before Init)")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	samversion.PrintVersionAndExit(componentName, *showVersion)

	if len(configs) == 0 {
		return fmt.Errorf("at least one --config flag is required")
	}

	ctx := context.Background()
	loader := &config.YAMLLoader{}

	fullCfg, err := samapp.LoadFullConfig(ctx, loader, configs[0])
	if err != nil {
		return fmt.Errorf("loading config %s: %w", configs[0], err)
	}

	logger, cleanup, err := samlog.SetupFromConfig(fullCfg)
	if err != nil {
		return fmt.Errorf("setup logging: %w", err)
	}
	defer cleanup()
	logger = logger.With("component", componentName)
	slog.SetDefault(logger)

	pprofShutdown := runtime.StartPprofServer(logger)
	defer pprofShutdown(context.Background())

	samapp.WarnOnDroppedLogSection(ctx, loader, configs[1:])
	samapp.WarnOnDroppedLogSection(ctx, loader, platformConfigs)

	appConfigs, err := samapp.LoadAppEntriesFromConfig(fullCfg)
	if err != nil {
		return fmt.Errorf("loading apps from %s: %w", configs[0], err)
	}

	for _, path := range configs[1:] {
		var entries []samapp.AppEntry
		entries, err = samapp.LoadAppEntries(ctx, loader, path)
		if err != nil {
			return fmt.Errorf("loading %s: %w", path, err)
		}
		appConfigs = append(appConfigs, entries...)
	}

	if len(appConfigs) == 0 {
		return fmt.Errorf("no app configurations found in config files")
	}

	logger.Info("loaded gateway configs", "count", len(appConfigs))

	logger, registerOTLPLogs, err := bootstrap.InstallOTLPLogs(ctx, appConfigs, componentName, logger)
	if err != nil {
		return err
	}

	appEntry := appConfigs[0]

	brk, err := samapp.CreateBrokerFromConfig(samapp.FirstBrokerConfig(appConfigs), logger, componentName)
	if err != nil {
		return fmt.Errorf("create broker: %w", err)
	}

	healthAddr = bootstrap.ResolveHealthAddrFlag(config.AsMap(fullCfg), healthAddr)
	if connectErr := bootstrap.ConnectStandaloneBroker(ctx, brk, healthAddr, logger); connectErr != nil {
		return fmt.Errorf("connect broker: %w", connectErr)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	opts, err := bootstrap.BuildGatewayRuntimeOptions(ctx, config.AsMap(fullCfg), appConfigs, logger, bootstrap.SuppressUnknownFeatureWarn())
	if err != nil {
		return fmt.Errorf("build runtime options: %w", err)
	}

	gatewayID := ""
	if id, ok := appEntry.AppConfig["gateway_id"].(string); ok && id != "" {
		gatewayID = id
	}
	if gatewayID == "" {
		gatewayID = appEntry.Name
	}
	if gatewayID == "" {
		return fmt.Errorf("gateway config %q has neither a name nor gateway_id; one is required for trust card identity", appEntry.Name)
	}
	namespace := "solace-agent-mesh"
	if ns, ok := appEntry.AppConfig["namespace"].(string); ok && ns != "" {
		namespace = ns
	}

	ec, extensionOpts, err := bootstrap.Bootstrap(ctx, bootstrap.BootstrapConfig{
		TrustConfig:    bootstrap.LoadTrustConfig(fullCfg),
		IdentityConfig: bootstrap.LoadIdentityConfig(fullCfg),
		ComponentType:  "gateway",
		ComponentID:    gatewayID,
		Namespace:      namespace,
		Broker:         brk,
		AuditConfig:    bootstrap.LoadAuditConfig(fullCfg),
		SigningKey: trust.SigningKeyConfig{
			Seed:           gatewaySigningSeed(appEntry.AppConfig),
			PersistenceDir: gatewayKeyPersistenceDir(),
		},
		Logger: logger,
	})
	if err != nil {
		return fmt.Errorf("extensions bootstrap: %w", err)
	}
	opts = append(opts, extensionOpts...)

	logger.Info("extensions initialized",
		"gatewayID", gatewayID,
		"namespace", namespace,
	)

	providersCatalog, err := bootstrap.LoadProvidersCatalog(fullCfg, logger)
	if err != nil {
		return fmt.Errorf("load oidc providers catalog: %w", err)
	}

	applyEnterpriseAuthDefaults(appConfigs, len(providersCatalog) > 0)

	rt := runtime.NewRuntime(brk, nil, opts...)
	bootstrap.ReportVolumeList(rt.Services().Volumes(), rt.Services().FeatureRegistry(), logger)

	registerOTLPLogs(rt)

	rt.Register("extensions", &samembed.ExtensionComponent{EC: ec})

	ctrlSvc := control.NewService(rt, namespace)
	rt.Register("control", ctrlSvc)

	elector := bootstrap.NewLeader(rt.Services(), namespace, leader.RoleGateway, logger)

	identityLazy := platformidentity.NewLazyService()
	ec.IdentityService = identityLazy

	const profileCacheTTL = time.Hour
	profileCache := auth.NewProfileCache(profileCacheTTL)
	ec.ProfileCache = profileCache
	logger.Info("profile cache wired", "ttl", profileCacheTTL)

	hooks := &entrypoint.GatewayHooks{
		TaskSigner:              &samembed.TrustTaskSigner{Signer: ec.TrustManager.Signer()},
		IdentityPlatformService: identityLazy,
		ProfileCache:            profileCache,
		AuthExpected:            samembed.AuthExpectedFromEnv(),
		BuildSenderAuthorizer: func(gatewayID string) auth.SenderAuthorizer {
			if gatewayID == "" {
				return nil
			}
			return auth.NewBrokerSenderAuthorizer(auth.NewBrokerAuthorizationClient(brk, auth.BrokerAuthorizationClientConfig{
				Namespace:   namespace,
				GatewayID:   gatewayID,
				Timeout:     4 * time.Second,
				MaxAttempts: 1,
			}, logger), gatewayID)
		},
		BuildResolvers: func(gatewayID string, appConfig map[string]any) (entrypoint.ScopeResolver, entrypoint.IdentityResolver, error) {
			authzCfg := samembed.ParseGatewayAuthzConfig(appConfig)
			svc, buildErr := oidcauth.NewAuthorizationService(authzCfg, gatewayID, logger)
			if buildErr != nil {
				return nil, nil, fmt.Errorf("build authz service: %w", buildErr)
			}
			if gatewayID != "" {
				svc.SetAuthorizationClient(auth.NewBrokerAuthorizationClient(brk, auth.BrokerAuthorizationClientConfig{
					Namespace: namespace,
					GatewayID: gatewayID,
				}, logger))
			}
			return &samembed.AuthzScopeResolver{Svc: svc}, oidcauth.NewIdentityResolver(svc, profileCache), nil
		},
		ConfigureProjectSharing: func(core *entrypoint.GatewayCore) error {
			core.SetProjectSharingService(sharing.NewService(core.Store))
			return nil
		},
		ConfigurePromptSharing: func(core *entrypoint.GatewayCore) error {
			core.SetPromptSharingService(sharing.NewPromptService(core.Store, core.Services().AuditLogger()))
			return nil
		},
		WebuiSettingsRegistry: bootstrap.NewWebuiSettingsRegistry(brk, namespace, logger),
	}

	if gatewayAuthConfigured(appConfigs, len(providersCatalog) > 0) {
		hooks.BuildLazyEnrichmentLookup = func(gatewayID string, _ map[string]any) auth.LazyEnrichmentLookup {
			return auth.BuildBrokerLazyEnrichmentLookup(brk, namespace, gatewayID, logger)
		}
	}

	exe, err := bootstrap.NewGatewayExecutor(rt.Services(), appConfigs)
	if err != nil {
		return fmt.Errorf("create gateway executor: %w", err)
	}
	exe.SetGatewayHooks(hooks)

	dispatcherGwID, err := setupPerGatewaySigners(ctx, exe, appConfigs, providersCatalog, ec, brk, namespace, logger)
	if err != nil {
		return err
	}

	logger.Info("project sharing enabled")

	httpCfg := samapp.FirstHTTPAdapterConfig(appConfigs)
	proxyTimeouts := entrypoint.DefaultServerTimeouts
	proxyTimeouts.ShutdownTimeout = entrypoint.ShutdownTimeoutFromEnv(logger)
	proxyOpts := bootstrap.ProxyOpts{
		ListenAddr: samapp.ResolveListenAddr(listenAddr, httpCfg),
		Timeouts:   &proxyTimeouts,
	}
	if certFile, keyFile, keyPassword, tlsAddr := samapp.ResolveProxyTLS(httpCfg); certFile != "" {
		proxyOpts.TLS = &entrypoint.ProxyTLSConfig{
			CertFile: certFile,
			KeyFile:  keyFile,
			Password: keyPassword,
		}
		proxyOpts.TLSAddr = tlsAddr
	}
	if staticDir != "" {
		proxyOpts.StaticFS = os.DirFS(staticDir)
	}
	proxy := bootstrap.NewGatewayProxy(proxyOpts, logger)

	registerMCPMetadataRoutes(proxy, exe, logger)

	runtimeDeps := runtimeGatewayDeps{
		providersCatalog: providersCatalog,
		ec:               ec,
		proxy:            proxy,
		broker:           brk,
		namespace:        namespace,
		logger:           logger,
		DispatcherGwID:   dispatcherGwID,
	}
	hooks.OnGatewayBeforeInit = func(ctx context.Context, core *entrypoint.GatewayCore, appConfig map[string]any) error {
		return setupRuntimeGateway(ctx, core, appConfig, runtimeDeps)
	}
	hooks.OnGatewayBeforeRemove = func(ctx context.Context, core *entrypoint.GatewayCore, appConfig map[string]any) error {
		return teardownRuntimeGateway(ctx, core, appConfig, runtimeDeps)
	}
	exe.SetGatewayHooks(hooks)

	if err := entrypoint.MountExecutor(proxy, exe); err != nil {
		return fmt.Errorf("mount gateway executor: %w", err)
	}

	if len(platformConfigs) > 0 {
		type namedComponent struct {
			name      string
			component runtime.Component
		}
		var dependents []namedComponent
		platformComponent, notifyCore, err := createPlatformService(ctx, platformServiceParams{
			entrypoints:     exe,
			loader:          loader,
			platformConfigs: platformConfigs,
			svc:             rt.Services(),
			ec:              ec,
			authzSvc:        nil,
			gatewayAppEntry: appEntry,
			leader:          elector,
			namespace:       namespace,
			proxy:           proxy,
			identityLazy:    identityLazy,
			logger:          logger,
			registerDependent: func(name string, c runtime.Component) {
				dependents = append(dependents, namedComponent{name, c})
			},
		})
		if err != nil {
			return fmt.Errorf("create platform service: %w", err)
		}
		if notifyCore != nil {
			rt.Register("notify", notifyCore)
		}
		rt.Register("platform", platformComponent)
		for _, d := range dependents {
			rt.Register(d.name, d.component)
		}
		stampMountedServices(exe.Gateways(), notifyCore != nil)
	}
	disableKeysUnlessPlatformHosted(ec.KeyAuthority, len(platformConfigs) > 0, ec.FeatureRegistry, logger)

	rt.Register("gateway", exe)
	rt.Register("proxy", proxy)
	if err := bootstrap.RegisterLeader(rt, elector); err != nil {
		return err
	}

	registerGatewayMetrics(rt, brk, exe, appConfigs)

	if healthAddr != "" {
		ms := runtime.NewManagementServer(rt, healthAddr, logger)
		bootstrap.MountMetricsOnManagement(ms, rt, logger)
		if _, err := ms.Start(); err != nil {
			return fmt.Errorf("start management server: %w", err)
		}
		defer func() { _ = ms.Stop(ctx) }()
	}

	return rt.Run(ctx)
}

type platformServiceParams struct {
	entrypoints       *entrypoint.GatewayExecutor
	loader            *config.YAMLLoader
	platformConfigs   configFiles
	svc               runtime.Services
	ec                *bootstrap.Components
	authzSvc          *oidcauth.AuthorizationService
	gatewayAppEntry   samapp.AppEntry
	leader            *leader.Elector
	namespace         string
	proxy             *entrypoint.GatewayProxy
	identityLazy      *platformidentity.LazyService
	logger            *slog.Logger
	registerDependent func(name string, c runtime.Component)
}

func platformAuthzConfig(platformAppConfig, gatewayAppConfig map[string]any) oidcauth.AuthorizationServiceConfig {
	cfg := samembed.ParsePlatformAuthzConfig(platformAppConfig)
	cfg = samembed.MergePlatformAuthzFromGateway(cfg, samembed.ParseGatewayAuthzConfig(gatewayAppConfig))
	samembed.SeedPlatformAuthzDefaults(&cfg, platformAppConfig)
	samembed.SeedPlatformAuthzDefaults(&cfg, gatewayAppConfig)
	return cfg
}

func createPlatformService(ctx context.Context, p platformServiceParams) (runtime.Component, *notify.NotifyCore, error) {
	loader := p.loader
	platformConfigs := p.platformConfigs
	svc := p.svc
	ec := p.ec
	authzSvc := p.authzSvc
	gatewayAppEntry := p.gatewayAppEntry
	namespace := p.namespace
	proxy := p.proxy
	identityLazy := p.identityLazy
	logger := p.logger
	var platformEntry samapp.AppEntry
	for _, path := range platformConfigs {
		entries, err := samapp.LoadAppEntries(ctx, loader, path)
		if err != nil {
			return nil, nil, fmt.Errorf("loading platform config %s: %w", path, err)
		}
		if len(entries) > 0 {
			platformEntry = entries[0]
			break
		}
	}

	if platformEntry.AppConfig == nil {
		platformEntry.AppConfig = map[string]any{
			"namespace": namespace,
		}
	}

	fullAuthzCfg := platformAuthzConfig(platformEntry.AppConfig, gatewayAppEntry.AppConfig)

	platformAuthzSvc := authzSvc
	if platformAuthzSvc == nil {
		var err error
		platformAuthzSvc, err = oidcauth.NewAuthorizationService(fullAuthzCfg, "platform", logger)
		if err != nil {
			return nil, nil, fmt.Errorf("create platform authorization service: %w", err)
		}
	}

	rbacYAML, rbacYAMLErr := entplatform.BuildRBACYAMLState(fullAuthzCfg, logger)
	if rbacYAMLErr != nil {
		logger.Warn("rbac yaml state load failed; authz keeps only the embedded system users plus DB state, so operator-defined roles resolve to nothing — check the configured roles/users files",
			"error", rbacYAMLErr,
			"rolesPath", fullAuthzCfg.RoleToScopeDefinitionsPath,
			"usersPath", fullAuthzCfg.UserToRoleAssignmentsPath)
	}

	routeDeps := &entplatform.RouteDeps{
		EntrypointsStartedByGateway: true,
		FeatureRegistry:             ec.FeatureRegistry,
		InProcessGateway:            p.entrypoints,
		EvalTaskSigner:              entplatform.NewEvalRunnerSigner(ec.TrustManager.Signer(), logger),
		EvalGenerationDispatch:      true,
		IdentityTokenSigner:         entplatform.NewIdentityTokenSigner(ec.TrustManager.Signer()),
		RBACYAMLState:               rbacYAML,
		AuthorizationMode:           fullAuthzCfg.Type,
		KeyAuthority:                ec.KeyAuthority,
		IsRBACAuthority:             true,
		OnIdentityServiceReady: func(svc platformidentity.IdentityService) {
			if identityLazy != nil {
				identityLazy.SetDelegate(svc)
			}
		},
	}

	ext := platform.ExtensionPoints{
		RegisterRoutes: func(mux *http.ServeMux, core *platform.PlatformCore) {
			entplatform.RegisterRoutesWithDeps(mux, core, routeDeps)
			builderapply.RegisterRoutes(mux, routeDeps, core, logger)
			apiv2.RegisterRoutes(mux, routeDeps, core, logger)
		},
		BackgroundServices: func(_ *platform.PlatformCore) []platform.BackgroundService {
			return routeDeps.BackgroundServices
		},
		LeaderJobs: func(_ *platform.PlatformCore) []leader.Job {
			return routeDeps.LeaderJobs
		},
	}

	useAuth, _ := gatewayAppEntry.AppConfig["frontend_use_authorization"].(bool)
	if useAuth {
		routeDeps.EvalGenerationSigner = ec.TrustManager.Signer()
		ext.AuthMiddleware = entplatform.NewAuthMiddleware(
			ec.TrustManager.Authenticator(), platformAuthzSvc, ec.KeyAuthority, logger)
		logger.Info("platform auth middleware enabled")
	} else {
		ext.AuthMiddleware = auth.DevMiddleware()
		logger.Info("platform auth middleware disabled, using dev identity")
	}

	core := bootstrap.NewPlatformCore(svc, platformEntry.AppConfig, ext)
	bootstrap.MountPlatform(proxy, core)
	if p.entrypoints != nil {
		p.entrypoints.SetDeployedEntrypoints(entplatform.EntrypointReaders(core.DB, namespace, logger))
		p.entrypoints.SetTargetLookup(entplatform.TargetLookup(core.DB, logger))
	}

	notifyCore := mountNotify(ec.FeatureRegistry, proxy, svc, namespace, platformEntry.AppConfig, gatewayAppEntry.AppConfig, ext.AuthMiddleware)
	if notifyCore != nil {
		logger.Info("notify service mounted behind gateway proxy")
	}

	callSigner := ec.TrustManager.Signer()
	if !useAuth {
		callSigner = nil
	}
	withheld, err := bootstrap.WithheldComponents(bootstrap.WithheldDeps{
		PlatformAppConfig: platformEntry.AppConfig, Registry: ec.FeatureRegistry, Metrics: svc.Metrics(),
		Broker: svc.Broker(), CallSigner: callSigner, Gateway: p.entrypoints, Namespace: p.namespace,
		Logger: logger, Platform: core, Leader: p.leader,
	})
	if err != nil {
		return nil, nil, err
	}
	if p.registerDependent != nil {
		for _, c := range withheld {
			p.registerDependent(c.Name, c.Component)
		}
	}

	mp := entplatform.NewManageablePlatform(core, routeDeps, logger)
	return mp, notifyCore, nil
}

func mountNotify(registry *features.Registry, proxy *entrypoint.GatewayProxy, svc runtime.Services, namespace string,
	platformCfg, gatewayCfg map[string]any, authMiddleware func(http.Handler) http.Handler) *notify.NotifyCore {
	if registry == nil || !registry.IsEnabled(features.KeyNotifyService) {
		return nil
	}
	appConfig := notifyAppConfigFor(namespace, platformCfg, gatewayCfg)
	core := bootstrap.NewNotifyCore(svc, appConfig, notify.NotifyExtensions{AuthMiddleware: authMiddleware})
	bootstrap.MountNotify(proxy, core)
	return core
}

func stampMountedServices(gateways []*entrypoint.GatewayCore, notifyMounted bool) {
	for _, gw := range gateways {
		gw.SetPlatformMounted(true)
		gw.SetNotifyMounted(notifyMounted)
	}
}

func notifyAppConfigFor(namespace string, platformCfg, gatewayCfg map[string]any) map[string]any {
	appConfig := map[string]any{"namespace": namespace}
	nc := notifyConfigBlock(platformCfg, gatewayCfg)
	if nc != nil {
		appConfig["notify"] = nc
		if rd, ok := nc["retention_days"]; ok {
			appConfig["retention_days"] = rd
		}
	}
	dbURL, _ := nc["database_url"].(string)
	if dbURL == "" {
		dbURL = platform.DatabaseURL(platformCfg)
	}
	appConfig["database_url"] = dbURL
	return appConfig
}

func notifyConfigBlock(cfgs ...map[string]any) map[string]any {
	for _, c := range cfgs {
		if nc, ok := c["notify"].(map[string]any); ok {
			return nc
		}
	}
	return nil
}

func gatewayAuthConfigured(appConfigs []samapp.AppEntry, catalogPresent bool) bool {
	if catalogPresent {
		return true
	}
	for i := range appConfigs {
		if v, ok := appConfigs[i].AppConfig["external_auth_provider"].(string); ok && v != "" {
			return true
		}
		if useAuth, _ := appConfigs[i].AppConfig["frontend_use_authorization"].(bool); useAuth {
			return true
		}
	}
	return false
}

func applyEnterpriseAuthDefaults(appConfigs []samapp.AppEntry, _ bool) {
	for i := range appConfigs {
		if v, ok := appConfigs[i].AppConfig["external_auth_provider"].(string); ok && v != "" {
			continue
		}
		if useAuth, _ := appConfigs[i].AppConfig["frontend_use_authorization"].(bool); useAuth {
			appConfigs[i].AppConfig["external_auth_provider"] = "enterprise"
		}
	}
}

type gatewayDescriptor struct {
	GwID      string
	IsHTTPSSE bool
	IsMCP     bool
	AppConfig map[string]any
}

func resolveDispatcher(gws []gatewayDescriptor) (gatewayID, source string) {
	var explicit []string
	for _, gw := range gws {
		if d, ok := gw.AppConfig["mcp_oauth_dispatcher"].(map[string]any); ok {
			if enabled, _ := d["enabled"].(bool); enabled {
				explicit = append(explicit, gw.GwID)
			}
		}
	}
	if len(explicit) > 0 {
		slices.Sort(explicit)
		return explicit[0], "explicit_override"
	}
	for _, gw := range gws {
		if gw.IsHTTPSSE {
			return gw.GwID, "webui_default"
		}
	}
	return "", "none"
}

func runDispatcherSelfChecks(gws []gatewayDescriptor, chosen string, logger *slog.Logger) {
	var explicit []string
	for _, gw := range gws {
		if d, ok := gw.AppConfig["mcp_oauth_dispatcher"].(map[string]any); ok {
			if enabled, _ := d["enabled"].(bool); enabled {
				explicit = append(explicit, gw.GwID)
			}
		}
	}
	if len(explicit) > 1 {
		slices.Sort(explicit)
		logger.Warn("multiple gateways have mcp_oauth_dispatcher.enabled=true; first by lex order wins",
			"gatewayIDs", explicit, "chosen", chosen)
	}

	if chosen == "" {
		for _, gw := range gws {
			if !gw.IsMCP {
				continue
			}
			if stateRedirectionEnabledForApp(gw.AppConfig) {
				logger.Warn("MCP gateway has state_redirection_enabled=true but no OAuth dispatcher is configured in this deployment",
					"gatewayID", gw.GwID)
			}
		}
	}
}

func registerGatewayMetrics(rt *runtime.Runtime, brk broker.Broker, exe *entrypoint.GatewayExecutor, appConfigs []samapp.AppEntry) {
	reg, ok := rt.Services().Metrics().(*observability.Registry)
	if !ok {
		return
	}
	observability.RegisterInstanceUp(reg, "GWE")
	if bs, ok := brk.(observability.BrokerState); ok {
		observability.RegisterBrokerConnectionState(reg, "GWE", bs)
	}
	observability.RegisterComponentCount(reg, "gateway", exe.ListGatewayNames)
	observability.RegisterGatewayInFlightTasks(reg, func() int {
		total := 0
		for _, gw := range exe.Gateways() {
			total += gw.InFlightTaskCount()
		}
		return total
	})
	observability.RegisterCertExpiry(reg, samapp.ResolveGatewayCertFiles(appConfigs)...)
}

func setupPerGatewaySigners(
	ctx context.Context,
	exe *entrypoint.GatewayExecutor,
	appConfigs []samapp.AppEntry,
	providersCatalog map[string]*oidcauth.OIDCConfig,
	ec *bootstrap.Components,
	brk broker.Broker,
	namespace string,
	logger *slog.Logger,
) (string, error) {
	descriptors := make([]gatewayDescriptor, 0, len(appConfigs))
	for i := range appConfigs {
		gw := exe.Gateways()[i]
		_, dIsHTTPSSE := gw.Adapter().(*httpsse.HTTPSSEAdapter)
		_, dIsMCP := gw.Adapter().(*mcp.Adapter)
		if !dIsHTTPSSE && !dIsMCP {
			continue
		}
		descriptors = append(descriptors, gatewayDescriptor{
			GwID:      gw.GatewayID(),
			IsHTTPSSE: dIsHTTPSSE,
			IsMCP:     dIsMCP,
			AppConfig: appConfigs[i].AppConfig,
		})
	}
	dispatcherGwID, source := resolveDispatcher(descriptors)
	logger.Info("MCP OAuth dispatcher resolved", "gatewayID", dispatcherGwID, "source", source)
	runDispatcherSelfChecks(descriptors, dispatcherGwID, logger)

	seenGatewayIDs := make(map[string]int, len(appConfigs))
	for i := range appConfigs {
		gw := exe.Gateways()[i]
		gwID := gw.GatewayID()

		_, isHTTPSSE := gw.Adapter().(*httpsse.HTTPSSEAdapter)
		mcpAdapter, isMCP := gw.Adapter().(*mcp.Adapter)
		if !isHTTPSSE && !isMCP {
			continue
		}

		if err := validateGatewayID(gwID, i, appConfigs, seenGatewayIDs); err != nil {
			return "", err
		}
		seenGatewayIDs[gwID] = i

		publicURL := resolveGatewayPublicURL(appConfigs[i], isHTTPSSE, isMCP)
		if publicURL == "" {
			if isMCP {
				logger.Warn("MCP gateway has no resolvable PublicURL; state-redirection peers will reject this gateway's target_link_uri",
					"gatewayID", gwID,
					"hint", "set gateway_adapter.adapter_config.issuer (or mcp_oauth_dispatcher.external_url) on this gateway")
			} else {
				logger.Info("trust card PublicURL not set for gateway",
					"gatewayID", gwID,
					"isHTTPSSE", isHTTPSSE)
			}
		}
		sgnr, err := registerGatewaySigner(ctx, gwID, publicURL, ec)
		if err != nil {
			return "", err
		}
		authzClient := auth.NewBrokerAuthorizationClient(brk, auth.BrokerAuthorizationClientConfig{
			Namespace: namespace,
			GatewayID: gwID,
		}, logger)
		userUpsertClient := auth.UserUpsertClient(auth.NewBrokerUserUpsertClient(brk, auth.BrokerUserUpsertClientConfig{
			Namespace: namespace,
			GatewayID: gwID,
		}, logger))
		deps := oidcauth.FactoryDeps{
			Signer:           sgnr,
			AuditLogger:      ec.AuditLogger,
			AuthzClient:      authzClient,
			ProfileCache:     ec.ProfileCache,
			UserUpsertClient: userUpsertClient,
			KeyAuthority:     ec.KeyAuthority,
			Logger:           logger,
		}
		if gwID == dispatcherGwID && dispatcherGwID != "" {
			deps.OnProviderBuilt = func(p *oidcauth.Provider) {
				sv := ec.TrustManager.NewStateVerifier()
				d := oidcauth.NewStateDispatcher(sv, logger.With("gatewayID", gwID))
				p.SetStateDispatcher(d)
			}
		}
		oidcauth.RegisterFactoryForGateway(gwID, gwID, providersCatalog, deps)

		if !isMCP {
			continue
		}
		if err := injectMCPAuthDeps(mcpAdapter, gwID, sgnr, appConfigs[i], providersCatalog, ec.TrustManager, logger); err != nil {
			return "", err
		}
	}
	return dispatcherGwID, nil
}

type runtimeGatewayDeps struct {
	providersCatalog map[string]*oidcauth.OIDCConfig
	ec               *bootstrap.Components
	proxy            *entrypoint.GatewayProxy
	broker           broker.Broker
	namespace        string
	logger           *slog.Logger
	DispatcherGwID   string
}

func setupRuntimeGateway(
	ctx context.Context,
	core *entrypoint.GatewayCore,
	appConfig map[string]any,
	deps runtimeGatewayDeps,
) (retErr error) {
	gwID := core.GatewayID()

	_, isHTTPSSE := core.Adapter().(*httpsse.HTTPSSEAdapter)
	mcpAdapter, isMCP := core.Adapter().(*mcp.Adapter)
	if !isHTTPSSE && !isMCP {
		return nil
	}
	if gwID == "" {
		return fmt.Errorf("runtime-added per-gateway-signer gateway requires a non-empty gateway_id")
	}

	defer func() {
		if retErr != nil {
			teardownRuntimeGateway(ctx, core, appConfig, deps) //nolint:errcheck
		}
	}()

	publicURL := resolveGatewayPublicURL(samapp.AppEntry{AppConfig: appConfig}, isHTTPSSE, isMCP)
	if publicURL == "" {
		if isMCP {
			deps.logger.Warn("MCP gateway has no resolvable PublicURL; state-redirection peers will reject this gateway's target_link_uri",
				"gatewayID", gwID,
				"hint", "set gateway_adapter.adapter_config.issuer (or mcp_oauth_dispatcher.external_url) on this gateway")
		} else {
			deps.logger.Info("trust card PublicURL not set for gateway",
				"gatewayID", gwID,
				"isHTTPSSE", isHTTPSSE)
		}
	}
	sgnr, err := registerGatewaySigner(ctx, gwID, publicURL, deps.ec)
	if err != nil {
		return err
	}
	authzClient := auth.NewBrokerAuthorizationClient(deps.broker, auth.BrokerAuthorizationClientConfig{
		Namespace: deps.namespace,
		GatewayID: gwID,
	}, deps.logger)
	userUpsertClient := auth.UserUpsertClient(auth.NewBrokerUserUpsertClient(deps.broker, auth.BrokerUserUpsertClientConfig{
		Namespace: deps.namespace,
		GatewayID: gwID,
	}, deps.logger))
	if deps.DispatcherGwID != "" {
		if d, ok := appConfig["mcp_oauth_dispatcher"].(map[string]any); ok {
			if enabled, _ := d["enabled"].(bool); enabled && gwID != deps.DispatcherGwID {
				deps.logger.Warn("runtime-added gateway has mcp_oauth_dispatcher.enabled=true but an existing dispatcher is already wired; ignoring new dispatcher block",
					"newGatewayID", gwID, "existingDispatcher", deps.DispatcherGwID)
			}
		}
	}
	if isMCP && deps.DispatcherGwID == "" && stateRedirectionEnabledForApp(appConfig) {
		deps.logger.Warn("MCP gateway has state_redirection_enabled=true but no OAuth dispatcher is configured in this deployment",
			"gatewayID", gwID)
	}
	deps2 := oidcauth.FactoryDeps{
		Signer:           sgnr,
		AuditLogger:      deps.ec.AuditLogger,
		AuthzClient:      authzClient,
		ProfileCache:     deps.ec.ProfileCache,
		UserUpsertClient: userUpsertClient,
		KeyAuthority:     deps.ec.KeyAuthority,
		Logger:           deps.logger,
	}
	if gwID == deps.DispatcherGwID && deps.DispatcherGwID != "" {
		deps2.OnProviderBuilt = func(p *oidcauth.Provider) {
			sv := deps.ec.TrustManager.NewStateVerifier()
			d := oidcauth.NewStateDispatcher(sv, deps.logger.With("gatewayID", gwID))
			p.SetStateDispatcher(d)
		}
	}
	oidcauth.RegisterFactoryForGateway(gwID, gwID, deps.providersCatalog, deps2)

	if !isMCP {
		return nil
	}
	appEntry := samapp.AppEntry{AppConfig: appConfig}
	if err := injectMCPAuthDeps(mcpAdapter, gwID, sgnr, appEntry, deps.providersCatalog, deps.ec.TrustManager, deps.logger); err != nil {
		return err
	}
	deps.proxy.RegisterMCPMetadataRoutes(gwID, mcpAdapter.MetadataHandler())
	deps.logger.Info("registered runtime-added gateway",
		"gatewayID", gwID,
		"protectedResource", mcpauth.PathProtectedResource+"/gw/"+gwID+"/...",
		"authorizationServer", mcpauth.PathWellKnown+"/gw/"+gwID,
	)
	return nil
}

func disableKeysUnlessPlatformHosted(holder *apikey.LazyAuthority, platformHosted bool, flags *features.Registry, logger *slog.Logger) {
	if holder == nil || platformHosted {
		return
	}
	holder.Disable()
	if flags != nil && flags.HasEnvOverride(features.KeyMachineTokens) && flags.IsEnabled(features.KeyMachineTokens) {
		logger.Warn("api keys are enabled but this gateway hosts no platform, so it refuses every key; run the gateway with the platform in one process to accept keys here")
		return
	}
	logger.Debug("api keys are not served by this gateway: no platform is hosted in this process")
}

func teardownRuntimeGateway(
	ctx context.Context,
	core *entrypoint.GatewayCore,
	appConfig map[string]any,
	deps runtimeGatewayDeps,
) error {
	gwID := core.GatewayID()

	_, isHTTPSSE := core.Adapter().(*httpsse.HTTPSSEAdapter)
	_, isMCP := core.Adapter().(*mcp.Adapter)
	if !isHTTPSSE && !isMCP {
		return nil
	}
	if gwID == "" {
		return nil
	}

	oidcauth.UnregisterFactoryForGateway(gwID)

	if primary := deps.ec.TrustManager.Signer(); primary == nil || gwID != deps.ec.TrustManager.ComponentID() {
		if err := deps.ec.TrustManager.RemoveAdditionalSigner(ctx, gwID); err != nil {
			deps.logger.Warn("failed to remove per-gateway trust signer", "gatewayID", gwID, "error", err)
		}
	}

	if isMCP {
		deps.proxy.UnregisterMCPMetadataRoutes(gwID)
	}

	deps.logger.Info("unregistered runtime-removed gateway", "gatewayID", gwID)
	return nil
}

func validateGatewayID(gwID string, i int, appConfigs []samapp.AppEntry, seen map[string]int) error {
	if gwID == "" {
		return fmt.Errorf("gateway %d (%s): gateway_id is required for the per-gateway-signer model", i, appConfigs[i].Name)
	}
	if prev, dup := seen[gwID]; dup {
		return fmt.Errorf("duplicate gateway_id %q used by gateways %d (%s) and %d (%s); per-gateway signers must be unique",
			gwID, prev, appConfigs[prev].Name, i, appConfigs[i].Name)
	}
	return nil
}

func registerGatewaySigner(ctx context.Context, gwID, publicURL string, ec *bootstrap.Components) (*trust.Signer, error) {
	if primary := ec.TrustManager.Signer(); primary != nil && gwID == ec.TrustManager.ComponentID() {
		if publicURL != "" {
			if err := ec.TrustManager.SetPrimaryPublicURL(ctx, publicURL); err != nil {
				return nil, fmt.Errorf("set primary PublicURL for %s: %w", gwID, err)
			}
		}
		return primary, nil
	}
	km, err := ec.TrustManager.NewSignerKeyManager(gwID)
	if err != nil {
		return nil, fmt.Errorf("trust key manager for %s: %w", gwID, err)
	}
	sgnr := trust.NewSigner(km, gwID, time.Hour, 2*time.Hour)
	if err := ec.TrustManager.RegisterAdditionalSigner(ctx, gwID, km, publicURL); err != nil {
		return nil, fmt.Errorf("register per-gateway trust signer for %s: %w", gwID, err)
	}
	return sgnr, nil
}

func gatewaySigningSeed(appConfig map[string]any) []byte {
	secret, _ := appConfig["session_secret_key"].(string)
	if secret == "" {
		secret = os.Getenv("SESSION_SECRET_KEY")
	}
	if secret == "" {
		return nil
	}
	return []byte(secret)
}

func gatewayKeyPersistenceDir() string {
	base := "keys"
	if dataDir := os.Getenv("SAM_DATA_DIR"); dataDir != "" {
		base = filepath.Join(dataDir, "keys")
	}
	return base
}

func resolveGatewayPublicURL(entry samapp.AppEntry, isHTTPSSE, isMCP bool) string {
	if d, ok := entry.AppConfig["mcp_oauth_dispatcher"].(map[string]any); ok {
		if u, ok := d["external_url"].(string); ok && u != "" {
			return strings.TrimRight(u, "/")
		}
	}
	if isMCP {
		if ga, ok := entry.AppConfig["gateway_adapter"].(map[string]any); ok {
			if ac, ok := ga["adapter_config"].(map[string]any); ok {
				if u, ok := ac["issuer"].(string); ok && u != "" {
					return strings.TrimRight(u, "/")
				}
			}
		}
	}
	if isHTTPSSE {
		if u, ok := entry.AppConfig["frontend_server_url"].(string); ok && u != "" {
			return strings.TrimRight(u, "/")
		}
	}
	return ""
}

func injectMCPAuthDeps(
	mcpAdapter *mcp.Adapter,
	gwID string,
	sgnr *trust.Signer,
	appEntry samapp.AppEntry,
	providersCatalog map[string]*oidcauth.OIDCConfig,
	trustManager *trust.Manager,
	logger *slog.Logger,
) error {
	entCfg, err := oidcauth.ParseConfig(appEntry.AppConfig, providersCatalog)
	if err != nil {
		return fmt.Errorf("parse enterprise auth for %s: %w", gwID, err)
	}
	if entCfg.OIDC == nil {
		return fmt.Errorf("MCP gateway %s: enterprise auth config has no OIDC provider; ensure providers catalog and external_auth_provider are set", gwID)
	}
	oidcClient, err := oidcauth.NewNativeOIDCClient(*entCfg.OIDC, logger)
	if err != nil {
		return fmt.Errorf("oidc client for %s: %w", gwID, err)
	}
	authzSvc, err := oidcauth.NewAuthorizationService(entCfg.AuthorizationService, gwID, logger)
	if err != nil {
		return fmt.Errorf("authz service for %s: %w", gwID, err)
	}

	deps := &mcp.MCPAuthDeps{
		IDPClient:     oidcauth.NewMCPIDPClient(oidcClient),
		SAMMinter:     oidcauth.NewMCPSAMMinter(sgnr, time.Hour),
		AuthzResolver: oidcauth.NewMCPAuthzResolver(authzSvc),
	}

	if stateRedirectionEnabledForApp(appEntry.AppConfig) {
		if trustManager == nil {
			return fmt.Errorf("MCP gateway %s: state_redirection_enabled=true but trust manager not available", gwID)
		}
		deps.StateSigner = trust.NewStateSigner(sgnr)
		deps.StateVerifier = trustManager.NewStateVerifier()
		logger.Info("MCP OAuth state-redirection enabled", "gatewayID", gwID)
	}

	mcpAdapter.SetAuthDeps(deps)
	return nil
}

func stateRedirectionEnabledForApp(appConfig map[string]any) bool {
	ga, ok := appConfig["gateway_adapter"].(map[string]any)
	if !ok {
		return false
	}
	ac, ok := ga["adapter_config"].(map[string]any)
	if !ok {
		return false
	}
	sr, _ := ac["state_redirection_enabled"].(bool)
	return sr
}

func registerMCPMetadataRoutes(
	proxy *entrypoint.GatewayProxy,
	exe *entrypoint.GatewayExecutor,
	logger *slog.Logger,
) {
	for _, gwCore := range exe.Gateways() {
		mcpAdapter, ok := gwCore.Adapter().(*mcp.Adapter)
		if !ok {
			continue
		}
		proxy.RegisterMCPMetadataRoutes(gwCore.GatewayID(), mcpAdapter.MetadataHandler())
		logger.Info("registered MCP metadata routes",
			"gatewayID", gwCore.GatewayID(),
			"protectedResource", mcpauth.PathProtectedResource+"/gw/"+gwCore.GatewayID()+"/...",
			"authorizationServer", mcpauth.PathWellKnown+"/gw/"+gwCore.GatewayID(),
		)
	}
}
