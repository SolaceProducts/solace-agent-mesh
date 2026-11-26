// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/container"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

const DefaultDevBrokerPort = 55554

var Version string

type SAMRunner struct {
	configFiles []string
	systemEnv   bool
	logger      *slog.Logger

	devBroker     *broker.DevBrokerServer
	devBrokerPort int
	devBrokerAddr string

	traceFile    string
	traceFilters []string
	traceAcks    bool

	mockLLMAddr      string
	mockLLMDelay     time.Duration
	mockLLMScenarios []string
	mockLLMServer    *mockllm.MockLLMServer

	execProcesses []*ManagedProcess
	sacProcess    *ManagedProcess

	containerSTR *ContainerSTR

	additionalConfigs map[string][]byte

	tempFiles []string

	unexpectedExitCh   chan struct{}
	unexpectedExitOnce sync.Once
}

type Options struct {
	ConfigFiles      []string
	SystemEnv        bool
	Logger           *slog.Logger
	TraceFile        string
	TraceFilters     []string
	TraceAcks        bool
	MockLLMAddr      string
	MockLLMDelay     time.Duration
	MockLLMScenarios []string

	AdditionalConfigs map[string][]byte
}

func New(opts Options) *SAMRunner {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &SAMRunner{
		configFiles:       opts.ConfigFiles,
		systemEnv:         opts.SystemEnv,
		logger:            opts.Logger,
		traceFile:         opts.TraceFile,
		traceFilters:      opts.TraceFilters,
		traceAcks:         opts.TraceAcks,
		mockLLMAddr:       opts.MockLLMAddr,
		mockLLMDelay:      opts.MockLLMDelay,
		mockLLMScenarios:  opts.MockLLMScenarios,
		additionalConfigs: opts.AdditionalConfigs,
		unexpectedExitCh:  make(chan struct{}),
	}
}

func (r *SAMRunner) UnexpectedExit() <-chan struct{} {
	return r.unexpectedExitCh
}

func (r *SAMRunner) watchProcess(proc *ManagedProcess) {
	<-proc.Done()
	if proc.WasStopped() {
		return
	}

	exitErr := proc.ExitError()
	exitCode := 0
	if exitErr != nil {
		if ee, ok := errors.AsType[*exec.ExitError](exitErr); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}

	r.logger.Error("subprocess exited unexpectedly",
		"label", proc.Label,
		"pid", proc.Pid(),
		"exitCode", exitCode,
		"error", exitErr,
	)
	r.unexpectedExitOnce.Do(func() { close(r.unexpectedExitCh) })
}

func defaultVolumeRoot() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "sam", "volumes")
}

func (r *SAMRunner) Start(ctx context.Context) error {
	if !r.systemEnv {
		if envPath := LoadDotEnv(r.logger); envPath != "" {
			r.logger.Info("loaded environment", "path", envPath)
		} else {
			r.logger.Info(".env file not found; proceeding without it")
		}
	} else {
		r.logger.Info("skipping .env loading (--system-env)")
	}

	if r.mockLLMAddr != "" {
		r.mockLLMServer = mockllm.NewMockLLMServer(r.mockLLMAddr, r.mockLLMDelay, 0, r.logger.With("logger_name", "mockllm"))
		if err := r.mockLLMServer.Start(); err != nil {
			return fmt.Errorf("start mock LLM server on %s: %w", r.mockLLMAddr, err)
		}
		r.logger.Info("mock LLM server started", "addr", r.mockLLMServer.Addr())

		for _, scenarioPath := range r.mockLLMScenarios {
			sc, err := mockllm.LoadScenario(scenarioPath)
			if err != nil {
				return fmt.Errorf("load mock LLM scenario %s: %w", scenarioPath, err)
			}
			r.mockLLMServer.LoadScenarios(sc)
		}
	}

	if len(r.additionalConfigs) > 0 {
		aweExec := detectAWEExec(r.configFiles)
		tmpFiles, err := writeAdditionalConfigs(r.additionalConfigs, aweExec, r.logger)
		if err != nil {
			return fmt.Errorf("write additional configs: %w", err)
		}
		r.configFiles = append(r.configFiles, tmpFiles...)
		r.tempFiles = append(r.tempFiles, tmpFiles...)
	}

	hasExec := PrescanForExec(r.configFiles)
	if hasExec {
		r.devBrokerPort = r.resolveDevBrokerPort()
		portStr := strconv.Itoa(r.devBrokerPort)
		for _, kv := range [][2]string{
			{"DEV_BROKER_HOST", "127.0.0.1"},
			{"DEV_BROKER_PORT", portStr},
			{"SAM_DEV_BROKER_HOST", "127.0.0.1"},
			{"SAM_DEV_BROKER_PORT", portStr},
		} {
			if err := os.Setenv(kv[0], kv[1]); err != nil {
				return fmt.Errorf("set environment %s=%s: %w", kv[0], kv[1], err)
			}
		}
		r.logger.Info("external processes detected", "devBrokerPort", r.devBrokerPort)
	}

	loader := &config.YAMLLoader{}
	var fullConfig map[string]any

	for _, cfgFile := range r.configFiles {
		cfg, err := loader.Load(ctx, cfgFile)
		if err != nil {
			return fmt.Errorf("loading config %s: %w", cfgFile, err)
		}

		raw := cfg.GetRaw("")
		if raw == nil {
			raw = cfgToMap(cfg)
		}
		cfgMap, ok := raw.(map[string]any)
		if !ok {
			r.logger.Warn("config file produced non-map result, skipping", "file", cfgFile)
			continue
		}

		if fullConfig == nil {
			fullConfig = cfgMap
		} else {
			fullConfig = mergeConfig(fullConfig, cfgMap)
		}
		r.logger.Debug("loaded config", "file", cfgFile)
	}

	if fullConfig == nil {
		return fmt.Errorf("no configuration loaded from %d files", len(r.configFiles))
	}

	appsRaw, _ := fullConfig["apps"].([]any)
	classified := ClassifyApps(appsRaw)

	hasPython := len(classified.PythonApps) > 0
	hasGoExec := len(classified.ExecGroups) > 0

	r.logger.Info("classified apps",
		"python", len(classified.PythonApps),
		"execGroups", len(classified.ExecGroups))

	logConfig, _ := fullConfig["log"].(map[string]any)

	extraSections := extractExtraSections(fullConfig)

	if hasGoExec {
		if err := r.startDevBroker(); err != nil {
			return fmt.Errorf("start dev broker: %w", err)
		}

		volResult := DetectVolumeRequirements(fullConfig)
		if volResult.NeedsVolumes {
			r.logger.Info("volume requirements detected, starting container STR")
			strCfg := ContainerSTRConfig{
				ImageTag:   container.DefaultImageTag(Version),
				Namespace:  volResult.Namespace,
				ToolsDir:   volResult.ToolsDir,
				VolumeRoot: defaultVolumeRoot(),
				BrokerHost: "127.0.0.1",
				BrokerPort: r.devBrokerPort,
			}
			cstr, err := StartContainerSTR(ctx, strCfg, r.logger)
			if err != nil {
				r.Stop()
				return fmt.Errorf("start container STR: %w", err)
			}
			r.containerSTR = cstr
		}

		if err := r.spawnExecProcesses(classified.ExecGroups, logConfig, extraSections); err != nil {
			r.Stop()
			return fmt.Errorf("spawn exec processes: %w", err)
		}
	}

	if hasPython || !hasGoExec {
		if err := r.startPythonSAC(fullConfig, classified, hasGoExec); err != nil {
			r.Stop()
			return fmt.Errorf("start Python SAC: %w", err)
		}
	} else {
		r.logger.Info("all apps are external (app_exec) — skipping Python SAC")
	}

	return nil
}

func (r *SAMRunner) Stop() {
	const stopTimeout = 5 * time.Second

	for _, proc := range r.execProcesses {
		proc.Stop(stopTimeout)
	}
	r.execProcesses = nil

	if r.containerSTR != nil {
		r.logger.Debug("stopping container STR")
		r.containerSTR.Stop(stopTimeout)
		r.containerSTR = nil
	}

	if r.sacProcess != nil {
		r.sacProcess.Stop(stopTimeout)
		r.sacProcess = nil
	}

	if r.devBroker != nil {
		r.logger.Debug("stopping dev broker")
		if err := r.devBroker.Stop(); err != nil {
			r.logger.Warn("error stopping dev broker", "error", err)
		}
		r.devBroker = nil
	}

	if r.mockLLMServer != nil {
		r.logger.Debug("stopping mock LLM server")
		r.mockLLMServer.Stop()
		r.mockLLMServer = nil
	}

	for _, f := range r.tempFiles {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			r.logger.Debug("failed to remove temp file", "file", f, "error", err)
		}
	}
	r.tempFiles = nil
}

func (r *SAMRunner) startDevBroker() error {
	r.devBroker = broker.NewDevBrokerServer(nil, r.logger)

	if r.traceFile != "" {
		tracer, err := broker.NewTraceWriter(r.traceFile, r.traceFilters, r.traceAcks)
		if err != nil {
			return fmt.Errorf("create trace writer for %s: %w", r.traceFile, err)
		}
		r.devBroker.SetTracer(tracer)
		r.logger.Info("message tracing enabled", "file", r.traceFile, "filters", len(r.traceFilters), "acks", r.traceAcks)
	}

	addr := fmt.Sprintf(":%d", r.devBrokerPort)

	actualAddr, err := r.devBroker.Start(addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	r.devBrokerAddr = actualAddr
	r.logger.Info("dev broker started", "addr", actualAddr)
	return nil
}

var healthPortByBinary = map[string]string{
	"sam-awe":                 ":8090",
	"sam-awe-enterprise":      ":8090",
	"sam-gateway":             ":9090",
	"sam-gateway-enterprise":  ":9090",
	"sam-platform":            ":9091",
	"sam-platform-enterprise": ":9091",
	"sam-str":                 ":8092",
	"sam-str-enterprise":      ":8092",
}

func resolveHealthPort(label string) (string, bool) {
	role := strings.TrimPrefix(label, "sam-")
	envName := "SAM_" + strings.ToUpper(strings.ReplaceAll(role, "-", "_")) + "_HEALTH_ADDR"
	if v := os.Getenv(envName); v != "" {
		return v, true
	}
	p, ok := healthPortByBinary[label]
	return p, ok
}

func (r *SAMRunner) spawnExecProcesses(execGroups map[string][]map[string]any, logConfig map[string]any, extraSections map[string]any) error {
	childEnv := BuildChildEnv("127.0.0.1", r.devBrokerPort)

	for exePath, apps := range execGroups {
		tmpPath, err := WriteTempConfig(apps, logConfig, extraSections)
		if err != nil {
			return fmt.Errorf("write temp config for %s: %w", exePath, err)
		}
		r.tempFiles = append(r.tempFiles, tmpPath)

		label := filepath.Base(exePath)
		args := []string{exePath, "--config", tmpPath}
		if port, ok := resolveHealthPort(label); ok {
			args = append(args, "--health-addr="+port)
		}

		proc, err := SpawnProcess(label, args, childEnv, r.logger)
		if err != nil {
			return err
		}
		r.execProcesses = append(r.execProcesses, proc)
		go r.watchProcess(proc)
	}

	return nil
}

func injectAppModuleIntoGoADKApps(apps []any, brokerCfg map[string]any) {
	for _, app := range apps {
		appMap, ok := app.(map[string]any)
		if !ok {
			continue
		}
		_, hasAppModule := appMap["app_module"]
		_, hasBroker := appMap["broker"]
		_, hasFlows := appMap["flows"]
		_, hasAppConfig := appMap["app_config"]

		if hasAppConfig && !hasAppModule && !hasBroker && !hasFlows {
			appMap["app_module"] = "solace_agent_mesh.agent.sac.app"
			if brokerCfg != nil {
				appMap["broker"] = brokerCfg
			}
		}
	}
}

func extractBrokerConfig(fullConfig map[string]any) map[string]any {
	if sc, ok := fullConfig["shared_config"].([]any); ok {
		for _, item := range sc {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if bc, ok := itemMap["broker_connection"].(map[string]any); ok {
				return bc
			}
		}
	}
	brokerURL := os.Getenv("SOLACE_BROKER_URL")
	if brokerURL == "" {
		return nil
	}
	cfg := map[string]any{
		"broker_url":      brokerURL,
		"broker_vpn":      os.Getenv("SOLACE_BROKER_VPN"),
		"broker_username": os.Getenv("SOLACE_BROKER_USERNAME"),
		"broker_password": os.Getenv("SOLACE_BROKER_PASSWORD"),
	}
	devMode := os.Getenv("SOLACE_DEV_MODE")
	if devMode == "true" {
		cfg["dev_mode"] = true
	}
	return cfg
}

func (r *SAMRunner) startPythonSAC(fullConfig map[string]any, classified ClassifiedApps, hasGoExec bool) error {
	if hasGoExec {
		fullConfig["apps"] = toAnySlice(classified.PythonApps)
	}

	if apps, ok := fullConfig["apps"].([]any); ok {
		brokerCfg := extractBrokerConfig(fullConfig)
		injectAppModuleIntoGoADKApps(apps, brokerCfg)
	}

	tmpPath, err := WriteTempConfig(nil, nil)
	if err != nil {
		return r.startPythonSACDirect(fullConfig)
	}
	_ = os.Remove(tmpPath)

	data, err := marshalFullConfig(fullConfig)
	if err != nil {
		return fmt.Errorf("marshal SAC config: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "sam-sac-*.yaml")
	if err != nil {
		return fmt.Errorf("create SAC temp config: %w", err)
	}
	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return fmt.Errorf("write SAC temp config: %w", writeErr)
	}
	_ = tmpFile.Close()
	r.tempFiles = append(r.tempFiles, tmpFile.Name())

	r.logger.Debug("wrote SAC temp config", "path", tmpFile.Name(), "size", len(data))

	if debugPath := os.Getenv("SAM_DEBUG_SAC_CONFIG"); debugPath != "" {
		_ = os.WriteFile(debugPath, data, 0644)
		r.logger.Debug("wrote SAC debug config", "path", debugPath)
	}

	pythonPath := findPython()

	args := []string{pythonPath, "-m", "solace_ai_connector.main", tmpFile.Name()}

	var env []string
	if hasGoExec {
		env = BuildChildEnv("127.0.0.1", r.devBrokerPort)
	} else {
		env = os.Environ()
	}

	proc, err := SpawnProcess("python-sac", args, env, r.logger)
	if err != nil {
		return fmt.Errorf("start Python SAC: %w", err)
	}
	r.sacProcess = proc
	go r.watchProcess(proc)
	return nil
}

func (r *SAMRunner) startPythonSACDirect(fullConfig map[string]any) error {
	pythonPath := findPython()
	args := append([]string{pythonPath, "-m", "solace_ai_connector.main"}, r.configFiles...)

	var env []string
	if r.devBroker != nil {
		env = BuildChildEnv("127.0.0.1", r.devBrokerPort)
	} else {
		env = os.Environ()
	}

	proc, err := SpawnProcess("python-sac", args, env, r.logger)
	if err != nil {
		return fmt.Errorf("start Python SAC: %w", err)
	}
	r.sacProcess = proc
	go r.watchProcess(proc)
	return nil
}

func (r *SAMRunner) resolveDevBrokerPort() int {
	portStr := os.Getenv("SAM_DEV_BROKER_PORT")
	if portStr == "" {
		portStr = os.Getenv("DEV_BROKER_PORT")
	}
	if portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 {
			return port
		}
	}
	return DefaultDevBrokerPort
}

func WaitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("port %s not ready after %v", addr, timeout)
}

func findPython() string {
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		venvPython := filepath.Join(venv, "bin", "python")
		if _, err := os.Stat(venvPython); err == nil {
			return venvPython
		}
	}

	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return "python3"
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

func cfgToMap(cfg config.Config) map[string]any {
	result := make(map[string]any)

	for _, key := range []string{
		"apps", "log", "shared_config",
		"trust_manager", "agent_identity", "audit_log",
		"providers",
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

func extractExtraSections(fullConfig map[string]any) map[string]any {
	extra := make(map[string]any)
	for k, v := range fullConfig {
		if k == "apps" || k == "log" {
			continue
		}
		extra[k] = v
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func toAnySlice(maps []map[string]any) []any {
	result := make([]any, len(maps))
	for i, m := range maps {
		result[i] = m
	}
	return result
}

func marshalFullConfig(cfg map[string]any) ([]byte, error) {
	return marshalYAMLBytes(cfg)
}

func (c ClassifiedApps) HasPythonApps() bool {
	return len(c.PythonApps) > 0
}

func (c ClassifiedApps) HasExecGroups() bool {
	return len(c.ExecGroups) > 0
}

func (r *SAMRunner) DevBrokerAddr() string {
	return r.devBrokerAddr
}

func (r *SAMRunner) ProcessCount() int {
	count := len(r.execProcesses)
	if r.sacProcess != nil {
		count++
	}
	if r.containerSTR != nil {
		count++
	}
	return count
}
