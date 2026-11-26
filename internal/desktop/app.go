// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation/edition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/runner"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sandbox"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

type App struct {
	ctx               context.Context
	instance          *embedded.Instance
	activation        edition.Gate
	logger            *slog.Logger
	enterprise        embedded.ExtensionProvider
	configDirs        []string
	defaultConfigs    map[string][]byte
	additionalConfigs map[string][]byte

	startURL string

	ready   chan struct{}
	readyMu sync.Once

	lastProgress atomic.Value

	showConsole bool

	logFile io.WriteCloser

	diagPaths diagnosticsPaths
	crash     crashSink

	shutdownOnce sync.Once

	pickedMu sync.Mutex
	picked   map[string]bool
}

const (
	chatAgentWait = 30 * time.Second
	chatAgentPoll = 200 * time.Millisecond
)

var errFileNotPicked = errors.New("read file: not a file chosen in the file dialog")

func NewApp() *App {
	return &App{
		logger: slog.Default(),
		ready:  make(chan struct{}),
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	go a.startEmbedded(ctx)
}

func (a *App) startEmbedded(ctx context.Context) {
	defer a.readyMu.Do(func() { close(a.ready) })

	if solaceWindowsBrokerWarning != "" {
		a.logger.Warn(solaceWindowsBrokerWarning)
	}

	dirs := a.configDirs
	if len(dirs) == 0 {
		if bundled := settings.BundledResourceDir("configs"); bundled != "" {
			dirs = []string{bundled}
		}
	}
	if globalDir, err := settings.DefaultGlobalDir(); err == nil {
		dirs = append(dirs, filepath.Join(globalDir, "configs"))
	}
	configPaths, err := discoverTopLevelConfigs(dirs)
	if err != nil {
		a.logger.Warn("config discovery failed, will use defaults", "error", err)
		configPaths = nil
	}

	a.activation = edition.New(a.logger)

	cfg := embedded.Config{
		Activation:        a.activation,
		Logger:            a.logger,
		LogWriter:         a.logFile,
		SystemEnv:         true,
		GatewayPort:       findFreePort("127.0.0.1", 8800),
		PlatformPort:      findFreePort("127.0.0.1", 8001),
		Extensions:        a.enterprise,
		ConfigPaths:       configPaths,
		DefaultConfigs:    a.defaultConfigs,
		AdditionalConfigs: a.additionalConfigs,
		ProgressCallback: func(msg string) {
			a.lastProgress.Store(msg)
			wailsruntime.EventsEmit(ctx, "startup:progress", msg)
		},
	}

	s, _, _ := settings.LoadGlobal()
	if s != nil && s.EffectiveSandboxMode() == settings.SandboxModeContainer {
		cfg.SandboxMode = settings.SandboxModeContainer
		cfg.NetworkBrokerPort = findFreePort("", 55555)
		if rt := sandbox.DetectRuntime(); rt != nil {
			cfg.ContainerRuntime = rt
		}
	}

	inst := embedded.New(cfg)

	if err := inst.Start(ctx); err != nil {
		a.logger.Error("failed to start embedded SAM", "error", err)
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelStop()
		if stopErr := inst.Stop(stopCtx); stopErr != nil {
			a.logger.Warn("partial-startup cleanup error", "error", stopErr)
		}
		return
	}

	a.instance = inst
	a.logger = slog.Default()

	a.logger.Info("desktop SAM started", "gateway", inst.GatewayAddr())

	if err := settings.WriteDesktopInstance(a.GatewayURL()); err != nil {
		a.logger.Warn("could not record desktop instance state", "error", err)
	}

	gwAddr := inst.GatewayAddr()
	selfTestClient := &http.Client{Timeout: 5 * time.Second}
	if resp, err := selfTestClient.Get("http://" + gwAddr + "/api/v1/config"); err != nil {
		a.logger.Error("config self-test FAILED", "error", err)
	} else {
		resp.Body.Close()
		a.logger.Info("config self-test passed", "httpStatus", resp.StatusCode)
	}

	cfg.ProgressCallback("Starting agents...")
	if !awaitChatAgent(ctx, selfTestClient, a.GatewayURL()+"/api/v1/agentCards", chatAgentWait, chatAgentPoll) {
		a.logger.Warn("no chat agent listed in time; opening the UI without one", "waited", chatAgentWait)
	}
}

func (a *App) WaitReady() {
	<-a.ready
}

func (a *App) Shutdown(context.Context) {
	a.shutdownOnce.Do(func() {
		if err := settings.ClearDesktopInstance(); err != nil {
			a.logger.Warn("could not clear desktop instance state", "error", err)
		}
		if a.activation != nil {
			a.activation.Close()
		}
		if a.instance != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := a.instance.Stop(stopCtx); err != nil {
				a.logger.Warn("stop error", "error", err)
			}
		}

		if err := a.crash.release(); err != nil {
			a.logger.Warn("could not release crash file", "error", err)
		}

		if a.logFile != nil {
			_ = a.logFile.Close()
		}
	})
}

func (a *App) GatewayURL() string {
	if a.instance == nil {
		return ""
	}
	return "http://" + rewriteBindHostToLoopback(a.instance.GatewayAddr())
}

func (a *App) Ready() bool {
	return a.instance != nil && a.instance.Ready()
}

func (a *App) APIBaseURL() string {
	<-a.ready
	return a.GatewayURL()
}

func (a *App) StartupProgress() string {
	if v := a.lastProgress.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (a *App) SaveFile(dataB64, filename string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	downloadsDir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return "", fmt.Errorf("create downloads dir: %w", err)
	}

	f, err := createDownloadFile(downloadsDir, filename)
	if err != nil {
		return "", err
	}
	savePath := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(savePath)
		return "", fmt.Errorf("write file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(savePath)
		return "", fmt.Errorf("close file: %w", err)
	}
	a.logger.Info("file saved", "path", savePath, "bytes", len(data))
	return savePath, nil
}

func (a *App) PickFile(accept string) (string, error) {
	path, err := nativeOpenFileDialog(a.ctx, "Select File")
	if err != nil {
		return "", fmt.Errorf("open file dialog: %w", err)
	}
	if path != "" {
		a.rememberPicked(path)
	}
	return path, nil
}

func (a *App) rememberPicked(path string) {
	a.pickedMu.Lock()
	defer a.pickedMu.Unlock()
	if a.picked == nil {
		a.picked = make(map[string]bool)
	}
	a.picked[path] = true
}

func (a *App) takePicked(path string) bool {
	a.pickedMu.Lock()
	defer a.pickedMu.Unlock()
	if !a.picked[path] {
		return false
	}
	delete(a.picked, path)
	return true
}

func (a *App) PickDirectory(title string) (string, error) {
	if title == "" {
		title = "Select Directory"
	}
	path, err := nativeOpenFolderDialog(a.ctx, title)
	if err != nil {
		return "", fmt.Errorf("open folder dialog: %w", err)
	}
	return path, nil
}

func (a *App) ReadFileBase64(path string) (string, error) {
	if !a.takePicked(path) {
		return "", errFileNotPicked
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

type SandboxStatus struct {
	Mode             string `json:"mode"`
	RuntimeAvailable bool   `json:"runtime_available"`
	RuntimeName      string `json:"runtime_name,omitempty"`
	RuntimeVersion   string `json:"runtime_version,omitempty"`
	CanInstall       bool   `json:"can_install"`
	InstallMethod    string `json:"install_method,omitempty"`
}

func (a *App) GetSandboxStatus() SandboxStatus {
	s, _, _ := settings.LoadGlobal()
	mode := "builtin"
	if s != nil && s.EffectiveSandboxMode() == settings.SandboxModeContainer {
		mode = "container"
	}

	status := SandboxStatus{Mode: mode}

	installStatus := sandbox.CheckInstallStatus()
	if installStatus.Available {
		status.RuntimeAvailable = true
		status.RuntimeName = installStatus.Runtime.Name
		status.RuntimeVersion = installStatus.Runtime.Version
	}
	status.CanInstall = installStatus.CanInstall
	status.InstallMethod = installStatus.InstallMethod

	return status
}

func (a *App) SetSandboxMode(mode string) error {
	sandboxMode := settings.SandboxMode(mode)
	if sandboxMode != settings.SandboxModeBuiltin && sandboxMode != settings.SandboxModeContainer {
		return fmt.Errorf("invalid sandbox mode: %q (must be 'builtin' or 'container')", mode)
	}

	if sandboxMode == settings.SandboxModeContainer {
		if rt := sandbox.DetectRuntime(); rt == nil {
			return fmt.Errorf("no container runtime found — install docker or podman first")
		}
	}

	s, settingsPath, err := settings.LoadGlobal()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if s == nil {
		return fmt.Errorf("no settings found — complete onboarding first")
	}

	s.Sandbox.Mode = sandboxMode

	if sandboxMode == settings.SandboxModeContainer {
		if rt := sandbox.DetectRuntime(); rt != nil {
			s.Sandbox.ContainerRuntime = rt.Name
		}
	}

	dir := settings.SettingsDir(settingsPath)
	if err := settings.Save(s, dir); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}

	a.logger.Info("sandbox mode updated", "mode", mode)
	return nil
}

func (a *App) InstallContainerRuntime() error {
	return sandbox.InstallPodman(a.ctx, a.logger, func(msg string) {
		a.logger.Info(msg)
	})
}

func (a *App) setupLogFile(paths diagnosticsPaths) io.WriteCloser {
	w, _, err := logging.NewRotatingFile(logging.RotatingFileOptions{
		Path:       paths.desktopLog(),
		MaxSizeMB:  20,
		MaxBackups: 5,
	})
	if err != nil {
		a.logger.Warn("desktop log file unavailable, runtime logs will not persist", "path", paths.desktopLog(), "error", err)
		return nil
	}
	if w == nil {
		return nil
	}
	slog.SetDefault(slog.New(newDesktopLogHandler(w)))
	a.logger = slog.Default()
	return w
}

func (a *App) startDiagnostics() {
	var loadedEnvPath string
	if home, err := settings.DefaultGlobalDir(); err == nil {
		loadedEnvPath = runner.LoadDotEnvFile(filepath.Join(home, ".env"), a.logger)
	}

	paths, err := resolveDiagnosticsPaths()
	if err != nil {
		return
	}
	a.diagPaths = paths

	if w := a.setupLogFile(paths); w != nil {
		a.logFile = w
	}
	if loadedEnvPath != "" {
		a.logger.Info("loaded desktop environment", "path", loadedEnvPath)
	}

	if err := pruneCrashFiles(paths.crash, crashFilesRetained); err != nil {
		a.logger.Warn("could not prune crash files", "error", err)
	}
	id := newRunIdentity()
	if sink, err := installCrashSink(paths, id, time.Now()); err != nil {
		a.logger.Warn("could not install crash sink", "error", err)
	} else {
		a.crash = sink
	}

	a.logger.Info("desktop diagnostics ready",
		"runId", id.RunID,
		"diagnosticsDir", paths.root,
	)
}

func discoverTopLevelConfigs(dirs []string) ([]string, error) {
	picked := make(map[string]string)
	var order []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".yaml" && ext != ".yml" {
				continue
			}
			if strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), "shared_config") {
				continue
			}
			abs, err := filepath.Abs(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			if prev, dup := picked[e.Name()]; dup {
				slog.Default().With("logger_name", "desktop-config-discovery").Info("config override",
					"basename", e.Name(),
					"using", abs,
					"shadowed", prev)
			} else {
				order = append(order, e.Name())
			}
			picked[e.Name()] = abs
		}
	}
	files := make([]string, 0, len(order))
	for _, name := range order {
		files = append(files, picked[name])
	}
	return files, nil
}

func findFreePort(host string, preferred int) int {
	if portFree(host, preferred) {
		return preferred
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return preferred
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func portFree(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
