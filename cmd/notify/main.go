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

	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type configFiles []string

func (c *configFiles) String() string { return fmt.Sprintf("%v", *c) }
func (c *configFiles) Set(value string) error {
	*c = append(*c, value)
	return nil
}

func main() {
	logger := samlog.NewStderrLogger(slog.LevelInfo)
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

	samversion.PrintVersionAndExit("sam-notify", *showVersion)

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
	slog.SetDefault(logger)
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
	logger.Info("loaded notify config", "name", appEntry.Name)

	brk, err := samapp.CreateBrokerFromConfig(samapp.FirstBrokerConfig(appConfigs), logger, "sam-notify")
	if err != nil {
		return fmt.Errorf("create broker: %w", err)
	}
	if connectErr := brk.Connect(ctx); connectErr != nil {
		return fmt.Errorf("connect broker: %w", connectErr)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	flagRegistry, err := bootstrap.LoadFeatureRegistry(logger)
	if err != nil {
		return err
	}
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(logger), runtime.WithFeatureRegistry(flagRegistry))

	core := bootstrap.NewNotifyCore(rt.Services(), appEntry.AppConfig, notify.NotifyExtensions{})

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
	bootstrap.MountNotify(proxy, core)

	rt.Register("notify", core)
	rt.Register("proxy", proxy)
	namespace := config.DefaultNamespace
	if ns, ok := appEntry.AppConfig["namespace"].(string); ok && ns != "" {
		namespace = ns
	}
	if err := bootstrap.RegisterLeader(rt, bootstrap.NewLeader(rt.Services(), namespace, leader.RoleNotify, logger)); err != nil {
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
