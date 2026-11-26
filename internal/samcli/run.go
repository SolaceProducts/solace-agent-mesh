// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation/edition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/desktop/frontend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/runner"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func newRunCmd() *cobra.Command {
	var skipFiles []string
	var systemEnv bool
	var embeddedMode bool
	var staticDir string
	var listenAddr string
	var healthAddr string
	var traceFile string
	var traceFilters []string
	var traceAcks bool
	var debugSubscribeTopics []string
	var debugSubscribeFile string
	var networkBrokerPort int
	var noOnboarding bool

	cmd := &cobra.Command{
		Use:    "run [FILES...]",
		Short:  "Run your agents locally on this machine",
		Hidden: true,
		Long: `Run the Solace Agent Mesh with specified YAML configuration files.

Accepts paths to individual YAML files or directories. When a directory is
provided, it is recursively searched for YAML files. When no paths are given,
config files are auto-discovered from a configs/ directory.`,
		Example: `  sam run
  sam run configs/agents/my_agent.yaml
  sam run configs/ --skip broker
  sam run --embedded`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if embeddedMode {
				return runSAMEmbedded(embeddedRunOpts{
					files:                args,
					skipFiles:            skipFiles,
					systemEnv:            systemEnv,
					staticDir:            staticDir,
					listenAddr:           listenAddr,
					healthAddr:           healthAddr,
					debugSubscribeTopics: debugSubscribeTopics,
					debugSubscribeFile:   debugSubscribeFile,
					networkBrokerPort:    networkBrokerPort,
					noOnboarding:         noOnboarding,
				})
			}
			if len(debugSubscribeTopics) > 0 || debugSubscribeFile != "" {
				return fmt.Errorf("--debug-subscribe and --debug-subscribe-file are only supported in --embedded mode")
			}
			return runSAM(args, skipFiles, systemEnv, traceFile, traceFilters, traceAcks, noOnboarding)
		},
	}

	cmd.Flags().StringArrayVarP(&skipFiles, "skip", "s", nil,
		"File name(s) to exclude from the run")
	cmd.Flags().BoolVar(&systemEnv, "system-env", false,
		"Use system environment variables only; do not load .env file")
	cmd.Flags().BoolVar(&noOnboarding, "no-onboarding", false,
		"Skip the interactive first-run onboarding wizard; start with whatever LLM config is in the environment (placeholder models if none)")
	cmd.Flags().BoolVar(&embeddedMode, "embedded", false,
		"Run all Go components in-process (single binary, no subprocesses)")
	cmd.Flags().StringVar(&staticDir, "static-dir", "",
		"Directory with frontend static assets (served at / in embedded mode)")
	cmd.Flags().StringVar(&traceFile, "trace-file", "",
		"Path to trace output file (NDJSON); enables dev broker message tracing")
	cmd.Flags().StringArrayVar(&traceFilters, "trace-filter", nil,
		"Topic filter for tracing (Solace wildcards; repeatable). Default: trace all")
	cmd.Flags().BoolVar(&traceAcks, "trace-acks", false,
		"Also trace message ACK events")
	cmd.Flags().StringVar(&listenAddr, "listen", "",
		"Entrypoint/proxy listen address (e.g., :8080); embedded mode only")
	cmd.Flags().StringVar(&healthAddr, "health-addr", "",
		"Health probe listen address (e.g., :8090, 'disabled' to skip); embedded mode only")
	cmd.Flags().StringArrayVar(&debugSubscribeTopics, "debug-subscribe", nil,
		"Subscribe a logging consumer to this topic on the shared broker (repeatable, wildcards allowed); embedded mode only")
	cmd.Flags().StringVar(&debugSubscribeFile, "debug-subscribe-file", "",
		"When set, --debug-subscribe writes events as newline-delimited JSON to this file (truncated on startup) instead of stderr")
	cmd.Flags().IntVar(&networkBrokerPort, "network-broker-port", 0,
		"When >0, also start a TCP DevBrokerServer on this port so external STR containers / Python processes can connect to the embedded broker; embedded mode only")

	return clianalytics.TrackCommand(cmd)
}

func waitForShutdownSignal(sigCh <-chan os.Signal, unexpectedCh <-chan struct{}, logger *slog.Logger) bool {
	select {
	case <-sigCh:
		logger.Info("stopping SAM...")
		return false
	case <-unexpectedCh:
		logger.Error("managed subprocess exited unexpectedly; shutting down SAM")
		return true
	}
}

func runSAM(files []string, skipFiles []string, systemEnv bool, traceFile string, traceFilters []string, traceAcks bool, noOnboarding bool) error {
	logger := newCLILogger(slog.LevelInfo)
	slog.SetDefault(logger)

	if !systemEnv {
		if envPath := runner.LoadDotEnv(logger); envPath != "" {
			logger.Info("loaded environment", "path", envPath)
		}
	}
	logResolvedHome(logger)

	s, settingsPath, err := settings.Load()
	if err != nil {
		return fmt.Errorf("loading settings: %w", err)
	}
	if s != nil {
		logger.Info("loaded settings", "path", settingsPath)
		settings.InjectIntoEnv(s, settings.SettingsDir(settingsPath))
	}

	configFiles, err := runner.DiscoverConfigFiles(files, skipFiles)
	if err != nil {
		return fmt.Errorf("discovering config files: %w", err)
	}

	if len(configFiles) == 0 {
		if s == nil {
			s, err = resolveOnboarding(noOnboarding, logger)
			if err != nil {
				return fmt.Errorf("onboarding: %w", err)
			}
			globalDir, _ := settings.DefaultGlobalDir()
			settings.InjectIntoEnv(s, globalDir)
		}

		configDir := filepath.Join(".sam", "configs")
		logger.Info("no config files found, writing defaults", "dir", configDir)
		if writeErr := settings.WriteDefaultConfigs(configDir); writeErr != nil {
			return fmt.Errorf("writing default configs: %w", writeErr)
		}

		configFiles, err = runner.DiscoverConfigFiles([]string{configDir}, nil)
		if err != nil {
			return fmt.Errorf("discovering default configs: %w", err)
		}
		if len(configFiles) == 0 {
			logger.Warn("no configuration files found after writing defaults")
			return nil
		}
	}

	logger.Info("discovered configuration files", "files", configFiles)

	runner.Version = Version

	r := runner.New(runner.Options{
		ConfigFiles:       configFiles,
		SystemEnv:         systemEnv,
		Logger:            logger,
		TraceFile:         traceFile,
		TraceFilters:      traceFilters,
		TraceAcks:         traceAcks,
		AdditionalConfigs: settings.AdditionalConfigYAML(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := r.Start(ctx); err != nil {
		return fmt.Errorf("starting SAM: %w", err)
	}

	logger.Info("SAM is running (press Ctrl+C to stop)")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	unexpectedExit := waitForShutdownSignal(sigCh, r.UnexpectedExit(), logger)

	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("SAM stopped")
	case <-sigCh:
		logger.Warn("forced exit")
		cancel()
		os.Exit(1) //nolint:gocritic // intentional forced exit; cancel() called above
	}

	if unexpectedExit {
		return fmt.Errorf("SAM terminated because a managed subprocess exited unexpectedly")
	}
	return nil
}

type embeddedRunOpts struct {
	files                []string
	skipFiles            []string
	systemEnv            bool
	staticDir            string
	listenAddr           string
	healthAddr           string
	debugSubscribeTopics []string
	debugSubscribeFile   string
	networkBrokerPort    int
	noOnboarding         bool
}

func runSAMEmbedded(opts embeddedRunOpts) error {
	logger := newCLILogger(slog.LevelInfo)
	slog.SetDefault(logger)

	if !opts.systemEnv {
		if envPath := runner.LoadDotEnv(logger); envPath != "" {
			logger.Info("loaded environment", "path", envPath)
		}
	}
	logResolvedHome(logger)

	configPaths := opts.files
	if len(configPaths) == 0 {
		configPaths = []string{"configs"}
	}

	cfg := embedded.Config{
		ConfigPaths:          configPaths,
		SkipPatterns:         opts.skipFiles,
		SystemEnv:            opts.systemEnv,
		Logger:               logger,
		HealthAddr:           opts.healthAddr,
		DebugSubscribeTopics: opts.debugSubscribeTopics,
		DebugSubscribeFile:   opts.debugSubscribeFile,
		NetworkBrokerPort:    opts.networkBrokerPort,
	}

	if opts.listenAddr != "" {
		if host, portStr, err := net.SplitHostPort(opts.listenAddr); err == nil {
			if port, convErr := strconv.Atoi(portStr); convErr == nil && port > 0 {
				cfg.GatewayPort = port
			}
			if host != "" {
				cfg.GatewayBindHost = host
			}
		} else {
			logger.Warn("--listen flag ignored: invalid address format (expected :PORT or HOST:PORT)", "value", opts.listenAddr)
		}
	}

	if opts.staticDir != "" {
		if info, statErr := os.Stat(opts.staticDir); statErr == nil && info.IsDir() {
			cfg.StaticFS = os.DirFS(opts.staticDir)
			logger.Info("serving frontend from directory", "path", opts.staticDir)
		} else {
			logger.Warn("static-dir not found or not a directory", "dir", opts.staticDir)
		}
	} else {
		assets, assetsErr := frontend.Assets() //nolint:staticcheck // build-tag-dependent
		if assetsErr == nil {                  //nolint:staticcheck // build-tag-dependent
			cfg.StaticFS = assets
			logger.Info("serving frontend from embedded assets")
		}
	}

	if !opts.systemEnv {
		s, settingsPath, err := settings.Load()
		if err != nil {
			return fmt.Errorf("loading settings: %w", err)
		}
		if s != nil {
			logger.Info("loaded settings", "path", settingsPath)
			settings.InjectIntoEnv(s, settings.SettingsDir(settingsPath))
		} else {
			var onboardErr error
			s, onboardErr = resolveOnboarding(opts.noOnboarding, logger)
			if onboardErr != nil {
				return fmt.Errorf("onboarding: %w", onboardErr)
			}
			globalDir, _ := settings.DefaultGlobalDir()
			settings.InjectIntoEnv(s, globalDir)
		}
		if s != nil && s.EffectiveSandboxMode() == settings.SandboxModeContainer {
			cfg.SandboxMode = settings.SandboxModeContainer
		}
	}

	if !opts.systemEnv {
		discoveredFiles, _ := runner.DiscoverConfigFiles(configPaths, opts.skipFiles)
		if len(discoveredFiles) == 0 {
			logger.Info("no config files found, using built-in defaults")
			cfg.DefaultConfigs = settings.DefaultConfigYAML()
		}
		cfg.AdditionalConfigs = settings.AdditionalConfigYAML()
	}

	gate := edition.New(logger)
	cfg.Activation = gate

	if embeddedConfigModifier != nil {
		embeddedConfigModifier(&cfg)
	}
	inst := embedded.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := inst.Start(ctx); err != nil {
		return fmt.Errorf("starting embedded SAM: %w", err)
	}

	logger.Info("SAM is running in embedded mode (press Ctrl+C to stop)",
		"gateway", inst.GatewayAddr())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	<-sigCh
	logger.Info("stopping embedded SAM...")
	if gate != nil {
		gate.Close()
	}

	done := make(chan struct{})
	go func() {
		_ = inst.Stop(context.Background())
		close(done)
	}()

	select {
	case <-done:
		logger.Info("embedded SAM stopped")
	case <-sigCh:
		logger.Warn("forced exit")
		cancel()
		os.Exit(1) //nolint:gocritic // intentional forced exit; cancel() called above
	}

	return nil
}
