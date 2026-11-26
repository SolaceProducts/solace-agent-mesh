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

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/builderapply"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samembed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type configFiles []string

func (c *configFiles) String() string { return fmt.Sprintf("%v", *c) }
func (c *configFiles) Set(value string) error {
	*c = append(*c, value)
	return nil
}

const componentName = "sam-platform"

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
	var healthAddr string
	flag.Var(&configs, "config", "YAML config file (can be specified multiple times)")
	flag.StringVar(&healthAddr, "health-addr", ":9091", "Address for liveness/readiness probes (empty to disable)")
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

	appEntry := appConfigs[0]
	logger.Info("loaded platform config", "name", appEntry.Name)

	logger, registerOTLPLogs, err := bootstrap.InstallOTLPLogs(ctx, appConfigs, componentName, logger)
	if err != nil {
		return err
	}

	brk, err := samapp.CreateBrokerFromConfig(samapp.FirstBrokerConfig(appConfigs), logger, componentName)
	if err != nil {
		return fmt.Errorf("create broker: %w", err)
	}

	if connectErr := brk.Connect(ctx); connectErr != nil {
		return fmt.Errorf("connect broker: %w", connectErr)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	namespace := "solace-agent-mesh"
	if ns, ok := appEntry.AppConfig["namespace"].(string); ok && ns != "" {
		namespace = ns
	}

	platformID := "platform"
	if pid, ok := appEntry.AppConfig["platform_id"].(string); ok && pid != "" {
		platformID = pid
	}
	ec, extensionOpts, err := bootstrap.Bootstrap(ctx, bootstrap.BootstrapConfig{
		TrustConfig:    bootstrap.LoadTrustConfig(fullCfg),
		IdentityConfig: bootstrap.LoadIdentityConfig(fullCfg),
		ComponentType:  "platform",
		ComponentID:    platformID,
		Namespace:      namespace,
		Broker:         brk,
		AuditConfig:    bootstrap.LoadAuditConfig(fullCfg),
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("extensions bootstrap: %w", err)
	}

	opts := bootstrap.BuildPlatformRuntimeOptions(ctx, config.AsMap(fullCfg), appConfigs, logger)
	opts = append(opts, extensionOpts...)

	if modelRaw, ok := appEntry.AppConfig["model"]; ok && modelRaw != nil {
		llmOpts, llmErr := bootstrap.BuildLLMOptions(ctx, modelRaw, logger)
		if llmErr != nil {
			logger.Warn("LLM client init failed; eval judge will be unavailable", "error", llmErr)
		} else {
			opts = append(opts, llmOpts...)
		}
	}

	logger.Info("extensions initialized",
		"platformID", platformID,
		"namespace", namespace,
	)

	rt := runtime.NewRuntime(brk, nil, opts...)

	registerOTLPLogs(rt)

	rt.Register("extensions", &samembed.ExtensionComponent{EC: ec})

	ctrlSvc := control.NewService(rt, namespace)
	rt.Register("control", ctrlSvc)

	authzCfg := samembed.ParsePlatformAuthzConfig(appEntry.AppConfig)
	samembed.SeedPlatformAuthzDefaults(&authzCfg, appEntry.AppConfig)

	authzSvc, authzErr := oidcauth.NewAuthorizationService(authzCfg, platformID, logger)
	if authzErr != nil {
		return fmt.Errorf("create authorization service: %w", authzErr)
	}

	rbacYAML, rbacYAMLErr := entplatform.BuildRBACYAMLState(authzCfg, logger)
	if rbacYAMLErr != nil {
		logger.Warn("rbac yaml state load failed; authz keeps only the embedded system users plus DB state, so operator-defined roles resolve to nothing — check the configured roles/users files",
			"error", rbacYAMLErr,
			"rolesPath", authzCfg.RoleToScopeDefinitionsPath,
			"usersPath", authzCfg.UserToRoleAssignmentsPath)
	}

	routeDeps := &entplatform.RouteDeps{
		FeatureRegistry:     ec.FeatureRegistry,
		EvalTaskSigner:      entplatform.NewEvalRunnerSigner(ec.TrustManager.Signer(), logger),
		IdentityTokenSigner: entplatform.NewIdentityTokenSigner(ec.TrustManager.Signer()),
		RBACYAMLState:       rbacYAML,
		MiniIDPMode:         authzCfg.MiniIDPMode,
		AuthorizationMode:   authzCfg.Type,
		KeyAuthority:        ec.KeyAuthority,
		IsRBACAuthority:     true,
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

	useAuth, _ := appEntry.AppConfig["frontend_use_authorization"].(bool)
	if useAuth {
		ext.AuthMiddleware = entplatform.NewAuthMiddleware(
			ec.TrustManager.Authenticator(), authzSvc, ec.KeyAuthority, logger)
		logger.Info("platform auth middleware enabled")
	} else {
		ext.AuthMiddleware = auth.DevMiddleware()
		logger.Info("platform auth middleware disabled (frontend_use_authorization=false), using dev identity")
	}

	core := platform.NewPlatformCore(appEntry.AppConfig, ext)
	core.SetServices(rt.Services())

	mp := entplatform.NewManageablePlatform(core, routeDeps, logger)

	httpCfg := samapp.FirstHTTPAdapterConfig(appConfigs)
	proxyOpts := bootstrap.ProxyOpts{
		ListenAddr: samapp.ResolveListenAddr("", httpCfg),
	}
	if certFile, keyFile, keyPassword, tlsAddr := samapp.ResolveProxyTLS(httpCfg); certFile != "" {
		proxyOpts.TLS = &entrypoint.ProxyTLSConfig{
			CertFile: certFile,
			KeyFile:  keyFile,
			Password: keyPassword,
		}
		proxyOpts.TLSAddr = tlsAddr
	}
	proxy := bootstrap.NewGatewayProxy(proxyOpts, logger)
	bootstrap.MountPlatform(proxy, core)

	rt.Register("platform", mp)
	rt.Register("proxy", proxy)
	if err := bootstrap.RegisterLeader(rt, bootstrap.NewLeader(rt.Services(), namespace, leader.RolePlatform, logger)); err != nil {
		return err
	}

	healthSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "health-addr" {
			healthSet = true
		}
	})
	mgmtAddr := bootstrap.ResolveManagementAddr(config.AsMap(fullCfg), healthAddr, healthSet)
	if mgmtAddr != "" {
		ms := runtime.NewManagementServer(rt, mgmtAddr, logger)
		bootstrap.MountMetricsOnManagement(ms, rt, logger)
		if _, err := ms.Start(); err != nil {
			return fmt.Errorf("start management server: %w", err)
		}
		defer func() { _ = ms.Stop(ctx) }()
	}

	return rt.Run(ctx)
}
