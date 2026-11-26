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

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"

	_ "go.uber.org/automaxprocs"
)

func main() {
	logger := samlog.NewStderrLogger(slog.LevelDebug)
	slog.SetDefault(logger)

	if err := run(); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "YAML config file for STR worker")
	healthAddr := flag.String("health-addr", ":8090", "address for health/ready HTTP endpoints")
	toolsDir := flag.String("tools-dir", "", "directory of builtin tools to discover at startup")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	samversion.PrintVersionAndExit("sam-str", *showVersion)

	if *configPath == "" {
		return fmt.Errorf("--config flag is required")
	}

	ctx := context.Background()

	loader := &config.YAMLLoader{}
	fullCfg, err := loader.Load(ctx, *configPath)
	if err != nil {
		return fmt.Errorf("loading config %s: %w", *configPath, err)
	}

	logger, cleanup, err := samlog.SetupFromConfig(fullCfg)
	if err != nil {
		return fmt.Errorf("setup logging: %w", err)
	}
	defer cleanup()
	logger = logger.With("component", "sam-str")
	slog.SetDefault(logger)

	pprofShutdown := runtime.StartPprofServer(logger)
	defer pprofShutdown(context.Background())

	appCfgMap := str.ExtractAppConfigMap(fullCfg)

	if *toolsDir != "" {
		str.InjectDiscoveredToolManifests(*toolsDir, appCfgMap, logger)
	}
	appCfg := config.FromMap(appCfgMap)

	workerCfg, err := str.ParseWorkerConfig(appCfg)
	if err != nil {
		return fmt.Errorf("parse worker config: %w", err)
	}
	workerCfg.BuiltinToolsDir = *toolsDir

	logger.Info("STR worker config loaded",
		"workerID", workerCfg.WorkerID,
		"namespace", workerCfg.Namespace,
		"manifests", workerCfg.ManifestPaths,
		"mode", workerCfg.Sandbox.Mode)

	hostname, hErr := os.Hostname()
	if hErr != nil {
		hostname = "local"
	}
	clientID := "sam-str-" + hostname
	logger.Info("STR broker identity", "clientID", clientID, "workerID", workerCfg.WorkerID)
	brk, err := samapp.CreateBrokerFromConfig(samapp.ExtractBrokerConfigFromRaw(fullCfg), logger, clientID)
	if err != nil {
		return fmt.Errorf("create broker: %w", err)
	}
	*healthAddr = bootstrap.ResolveHealthAddrFlag(config.AsMap(fullCfg), *healthAddr)
	if connectErr := bootstrap.ConnectStandaloneBroker(ctx, brk, *healthAddr, logger); connectErr != nil {
		return fmt.Errorf("connect broker: %w", connectErr)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	opts := bootstrap.BuildSTRRuntimeOptions(ctx, config.AsMap(fullCfg), appCfg, logger)

	ec, extensionOpts, err := bootstrap.Bootstrap(ctx, bootstrap.BootstrapConfig{
		TrustConfig:    bootstrap.LoadTrustConfig(fullCfg),
		IdentityConfig: bootstrap.LoadIdentityConfig(fullCfg),
		ComponentType:  "str",
		ComponentID:    workerCfg.WorkerID,
		Namespace:      workerCfg.Namespace,
		Broker:         brk,
		AuditConfig:    bootstrap.LoadAuditConfig(fullCfg),
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("extensions bootstrap: %w", err)
	}
	opts = append(opts, extensionOpts...)

	bindingsReg := bootstrap.NewBindingsRegistry(brk, workerCfg.Namespace, logger)
	defer bindingsReg.Close()
	opts = append(opts, runtime.WithBindings(bindingsReg))

	logger.Info("extensions initialized",
		"workerID", workerCfg.WorkerID,
		"namespace", workerCfg.Namespace,
		"trustEnabled", bootstrap.LoadTrustConfig(fullCfg).Enabled,
	)

	rt := runtime.NewRuntime(brk, nil, opts...)

	rt.Register("extensions", &extensionComponent{ec: ec})

	ctrlSvc := control.NewService(rt, workerCfg.Namespace)
	rt.Register("control", ctrlSvc)

	rt.Register("model-bindings", bindings.NewWatcher(bindingsReg, brk, workerCfg.Namespace, logger))

	worker, err := bootstrap.NewSTRWorkerFromConfig(rt.Services(), workerCfg, appCfg)
	if err != nil {
		return fmt.Errorf("create STR worker: %w", err)
	}
	rt.Register("str", worker)

	if reg, ok := rt.Services().Metrics().(*observability.Registry); ok {
		observability.RegisterInstanceUp(reg, "STR")
		if bs, ok := brk.(observability.BrokerState); ok {
			observability.RegisterBrokerConnectionState(reg, "STR", bs)
		}
	}

	if *healthAddr != "" {
		ms := runtime.NewManagementServer(rt, *healthAddr, logger)
		bootstrap.MountMetricsOnManagement(ms, rt, logger)
		if h := worker.IsolatedToolsHandler(); h != nil {
			ms.Handle(str.IsolatedToolsURLPath, h)
		}
		addr, hsErr := ms.Start()
		if hsErr != nil {
			return fmt.Errorf("start management server: %w", hsErr)
		}
		logger.Info("management server listening", "addr", addr)
		defer func() { _ = ms.Stop(ctx) }()
	}

	return rt.Run(ctx)
}

type extensionComponent struct {
	ec *bootstrap.Components
}

func (e *extensionComponent) Init(_ context.Context, _ config.Config) error { return nil }
func (e *extensionComponent) Start(ctx context.Context) error               { return e.ec.Start(ctx) }
func (e *extensionComponent) Stop(ctx context.Context) error                { return e.ec.Stop(ctx) }
func (e *extensionComponent) Health() error                                 { return nil }
