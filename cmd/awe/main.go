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
	"os"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/proxy/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samembed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

type configFiles []string

func (c *configFiles) String() string { return fmt.Sprintf("%v", *c) }
func (c *configFiles) Set(value string) error {
	*c = append(*c, value)
	return nil
}

const componentName = "sam-awe"

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
	flag.Var(&configs, "config", "YAML config file (can be specified multiple times)")
	healthAddr := flag.String("health-addr", ":8090", "address for health/ready HTTP endpoints")
	showVersion := flag.Bool("version", false, "Print version and exit")
	waitForPlatform := flag.Bool("wait-for-platform", true, "report ready only after a GWE has answered with this AWE's platform agents (or 2 minutes); set false for an AWE with no platform")
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

	awe.SetToolDefaults(samapp.ToolDefaultsOf(fullCfg))

	appConfigs, err := samapp.LoadAppEntriesFromConfig(fullCfg)
	if err != nil {
		return fmt.Errorf("loading apps from %s: %w", configs[0], err)
	}

	for _, path := range configs[1:] {
		awe.MergeToolDefaults(samapp.LoadFileToolDefaults(ctx, loader, path))
		var entries []samapp.AppEntry
		entries, err = samapp.LoadAppEntries(ctx, loader, path)
		if err != nil {
			return fmt.Errorf("loading %s: %w", path, err)
		}
		appConfigs = append(appConfigs, entries...)
	}

	appConfigs, err = bootstrap.MergeBundledAWEApps(ctx, loader, appConfigs, settings.BuilderConfigYAML(), logger)
	if err != nil {
		return err
	}

	logger, registerOTLPLogs, err := bootstrap.InstallOTLPLogs(ctx, appConfigs, componentName, logger)
	if err != nil {
		return err
	}

	brokerCfg := samapp.ResolveBrokerConfig(appConfigs, fullCfg, logger)
	brk, err := samapp.CreateBrokerFromConfig(brokerCfg, logger, componentName)
	if err != nil {
		return fmt.Errorf("create broker: %w", err)
	}

	*healthAddr = bootstrap.ResolveHealthAddrFlag(config.AsMap(fullCfg), *healthAddr)
	if connectErr := bootstrap.ConnectStandaloneBroker(ctx, brk, *healthAddr, logger); connectErr != nil {
		return fmt.Errorf("connect broker: %w", connectErr)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	opts, sessionStore, err := bootstrap.BuildAWERuntimeOptions(ctx, config.AsMap(fullCfg), appConfigs, logger)
	if err != nil {
		return fmt.Errorf("build runtime options: %w", err)
	}

	componentID := appConfigs[0].Name
	namespace := "solace-agent-mesh"
	if ns, ok := appConfigs[0].AppConfig["namespace"].(string); ok && ns != "" {
		namespace = ns
	}

	ec, extensionOpts, err := bootstrap.Bootstrap(ctx, bootstrap.BootstrapConfig{
		TrustConfig:    bootstrap.LoadTrustConfig(fullCfg),
		IdentityConfig: bootstrap.LoadIdentityConfig(fullCfg),
		ComponentType:  "agent",
		ComponentID:    componentID,
		Namespace:      namespace,
		Broker:         brk,
		SessionStore:   sessionStore,
		AuditConfig:    bootstrap.LoadAuditConfig(fullCfg),
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("extensions bootstrap: %w", err)
	}
	opts = append(opts, extensionOpts...)

	logger.Info("extensions initialized",
		"componentID", componentID,
		"namespace", namespace,
		"trustEnabled", bootstrap.LoadTrustConfig(fullCfg).Enabled,
	)

	bindingsReg := bootstrap.NewBindingsRegistry(brk, namespace, logger)
	defer bindingsReg.Close()
	opts = append(opts, runtime.WithBindings(bindingsReg))

	rt := runtime.NewRuntime(brk, nil, opts...)
	bootstrap.ReportVolumeList(rt.Services().Volumes(), rt.Services().FeatureRegistry(), logger)

	registerOTLPLogs(rt)

	rt.Register("extensions", &samembed.ExtensionComponent{EC: ec})

	ctrlSvc := control.NewService(rt, namespace)
	rt.Register("control", ctrlSvc)

	rt.Register("model-bindings", bindings.NewWatcher(bindingsReg, brk, namespace, logger))

	exe := bootstrap.NewAWEExecutor(rt.Services(), bootstrap.WithAWEBaseConfig(config.AsMap(fullCfg)))
	if _, err := bootstrap.LoadAWEApps(exe, appConfigs, logger); err != nil {
		return err
	}

	rt.Register("awe", exe)
	exe.WaitForPlatformBeforeReady(*waitForPlatform)
	rt.OnStarted(func() { exe.FollowPlatformAgents(ctx, namespace) })

	if reg, ok := rt.Services().Metrics().(*observability.Registry); ok {
		observability.RegisterInstanceUp(reg, "AWE")
		if bs, ok := brk.(observability.BrokerState); ok {
			observability.RegisterBrokerConnectionState(reg, "AWE", bs)
		}
		observability.RegisterComponentCount(reg, "agent", exe.ListAgentNames)
		observability.RegisterActiveTasks(reg, "AWE", func() []observability.ActiveTaskCount {
			counts := exe.ActiveTaskCounts()
			out := make([]observability.ActiveTaskCount, 0, len(counts))
			for key, n := range counts {
				out = append(out, observability.ActiveTaskCount{Kind: key.Kind, Trigger: key.Trigger, Count: n})
			}
			return out
		})
		observability.RegisterAgentActiveExecutions(reg, exe.ActiveTaskCountsByAgent)
	}

	if *healthAddr != "" {
		ms := runtime.NewManagementServer(rt, *healthAddr, logger)
		bootstrap.MountMetricsOnManagement(ms, rt, logger)
		_, hsErr := ms.Start()
		if hsErr != nil {
			return fmt.Errorf("start management server: %w", hsErr)
		}
		defer func() { _ = ms.Stop(ctx) }()
	}

	return rt.Run(ctx)
}
