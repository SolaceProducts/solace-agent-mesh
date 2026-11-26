// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type Runtime struct {
	Name string

	Path string

	Version string
}

func DetectRuntime() *Runtime {
	ensureCommonPaths()

	for _, name := range []string{"podman", "docker"} {
		if rt := probeRuntime(name); rt != nil {
			return rt
		}
	}
	return nil
}

func ensureCommonPaths() {
	extraPaths := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
	}
	current := os.Getenv("PATH")
	for _, p := range extraPaths {
		if !strings.Contains(current, p) {
			current = current + ":" + p
		}
	}
	_ = os.Setenv("PATH", current)
}

func probeRuntime(name string) *Runtime {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil
	}

	out, err := exec.Command(path, "version", "--format", "{{.Client.Version}}").CombinedOutput()
	if err != nil {
		out, err = exec.Command(path, "--version").CombinedOutput()
		if err != nil {
			return nil
		}
	}

	version := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(version, " "); idx >= 0 {
		version = version[idx+1:]
	}

	return &Runtime{
		Name:    name,
		Path:    path,
		Version: version,
	}
}

type InstallStatus struct {
	Available bool

	Runtime *Runtime

	CanInstall bool

	InstallMethod string
}

func CheckInstallStatus() *InstallStatus {
	status := &InstallStatus{}

	if rt := DetectRuntime(); rt != nil {
		status.Available = true
		status.Runtime = rt
		return status
	}

	status.CanInstall, status.InstallMethod = canInstallPodman()
	return status
}

func canInstallPodman() (bool, string) {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err == nil {
			return true, "brew install podman"
		}
		return false, ""

	case "linux":
		return false, ""

	case "windows":
		return false, ""

	default:
		return false, ""
	}
}

func InstallPodman(ctx context.Context, logger *slog.Logger, progress func(msg string)) error {
	switch runtime.GOOS {
	case "darwin":
		return installPodmanDarwin(ctx, logger, progress)
	case "linux":
		return fmt.Errorf("automatic podman installation is not yet supported on Linux — please install manually")
	case "windows":
		return fmt.Errorf("automatic podman installation is not yet supported on Windows — please install manually")
	default:
		return fmt.Errorf("automatic podman installation is not supported on %s", runtime.GOOS)
	}
}

func installPodmanDarwin(ctx context.Context, logger *slog.Logger, progress func(msg string)) error {
	brewPath, err := exec.LookPath("brew")
	if err != nil {
		return fmt.Errorf("Homebrew not found — install Homebrew first (https://brew.sh)")
	}

	progress("Installing podman via Homebrew...")
	logger.Info("installing podman", "method", "brew")

	cmd := exec.CommandContext(ctx, brewPath, "install", "podman")
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("podman install failed", "error", err, "outputLen", len(output), "outputHead", string(output[:min(200, len(output))]))
		return fmt.Errorf("brew install podman failed: %w\n%s", err, string(output))
	}

	progress("Initializing podman machine...")
	logger.Info("initializing podman machine")

	initCmd := exec.CommandContext(ctx, "podman", "machine", "init")
	initOutput, err := initCmd.CombinedOutput()
	if err != nil {
		if !strings.Contains(string(initOutput), "already exists") {
			logger.Warn("podman machine init warning", "error", err, "output", string(initOutput))
		}
	}

	progress("Starting podman machine...")
	logger.Info("starting podman machine")

	startCmd := exec.CommandContext(ctx, "podman", "machine", "start")
	startOutput, err := startCmd.CombinedOutput()
	if err != nil {
		if !strings.Contains(string(startOutput), "already running") {
			logger.Warn("podman machine start warning", "error", err, "output", string(startOutput))
		}
	}

	rt := probeRuntime("podman")
	if rt == nil {
		return fmt.Errorf("podman was installed but is not functional — try running 'podman machine start' manually")
	}

	progress(fmt.Sprintf("Podman %s installed successfully", rt.Version))
	logger.Info("podman installed", "version", rt.Version)
	return nil
}

func EnsurePodmanMachineRunning(ctx context.Context, rt *Runtime, logger *slog.Logger) error {
	if rt.Name != "podman" || runtime.GOOS != "darwin" {
		return nil
	}

	cmd := exec.CommandContext(ctx, rt.Path, "machine", "info", "--format", "{{.Host.MachineState}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		logger.Debug("podman machine info failed, attempting start", "error", err)
		return startPodmanMachine(ctx, rt, logger)
	}

	state := strings.TrimSpace(string(out))
	if state == "Running" {
		return nil
	}

	logger.Info("podman machine not running, starting...", "state", state)
	return startPodmanMachine(ctx, rt, logger)
}

func startPodmanMachine(ctx context.Context, rt *Runtime, logger *slog.Logger) error {
	cmd := exec.CommandContext(ctx, rt.Path, "machine", "start")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "already running") {
			return nil
		}
		return fmt.Errorf("podman machine start failed: %w\n%s", err, string(out))
	}
	logger.Info("podman machine started")
	return nil
}

func ResolveSandboxDir() (string, error) {
	if dir := os.Getenv("SAM_SANDBOX_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return filepath.Abs(dir)
		}
	}

	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("eval symlinks: %w", err)
	}
	exeDir := filepath.Dir(exePath)

	appBundlePath := filepath.Join(exeDir, "..", "Resources", "sandbox")
	if info, err := os.Stat(appBundlePath); err == nil && info.IsDir() {
		return filepath.Abs(appBundlePath)
	}

	devPath := filepath.Join(exeDir, "sandbox")
	if info, err := os.Stat(devPath); err == nil && info.IsDir() {
		return filepath.Abs(devPath)
	}

	return "", fmt.Errorf("sandbox directory not found (checked %s and %s)", appBundlePath, devPath)
}

type ImageManager struct {
	rt     *Runtime
	logger *slog.Logger
	mu     sync.Mutex

	imageTag string
}

func NewImageManager(rt *Runtime, logger *slog.Logger) *ImageManager {
	return &ImageManager{
		rt:       rt,
		logger:   logger.With("logger_name", "image-manager"),
		imageTag: "sam-str:latest",
	}
}

func (m *ImageManager) ImageExists(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, m.rt.Path, "image", "inspect", m.imageTag)
	return cmd.Run() == nil
}

func (m *ImageManager) BuildImage(ctx context.Context, sandboxDir string, progress func(msg string)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	platform := "linux/" + runtime.GOARCH

	if !m.imageExists(ctx, baseImageTag) {
		progress("Building base sandbox image (one-time, may take several minutes)...")
		m.logger.Info("building base image", "tag", baseImageTag)
		if err := m.buildImage(ctx, generateBaseDockerfile(), baseImageTag, platform, sandboxDir, progress); err != nil {
			return fmt.Errorf("build base image: %w", err)
		}
		m.logger.Info("base image built", "tag", baseImageTag)
	} else {
		m.logger.Info("base image already exists", "tag", baseImageTag)
	}

	progress("Building sandbox container image...")
	m.logger.Info("building STR image", "context", sandboxDir, "tag", m.imageTag)
	if err := m.buildImage(ctx, generateDockerfile(), m.imageTag, platform, sandboxDir, progress); err != nil {
		return err
	}

	progress("Sandbox container image ready")
	m.logger.Info("STR image built", "tag", m.imageTag)
	return nil
}

func (m *ImageManager) imageExists(ctx context.Context, tag string) bool {
	cmd := exec.CommandContext(ctx, m.rt.Path, "image", "inspect", tag)
	return cmd.Run() == nil
}

func (m *ImageManager) buildImage(ctx context.Context, dockerfile, tag, platform, contextDir string, progress func(msg string)) error {
	cmd := exec.CommandContext(ctx, m.rt.Path, "build",
		"--platform", platform,
		"-t", tag,
		"-f", "-",
		contextDir,
	)
	cmd.Stdin = strings.NewReader(dockerfile)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return fmt.Errorf("start image build: %w", err)
	}

	var buildOutput strings.Builder
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			buildOutput.WriteString(line)
			buildOutput.WriteByte('\n')
			m.logger.Debug("build output", "line", line)

			if msg := parseBuildProgress(line); msg != "" {
				progress(msg)
			}
		}
	}()

	err := cmd.Wait()
	_ = pw.Close()
	<-scanDone

	if err != nil {
		m.logger.Error("image build failed", "tag", tag, "error", err)
		return fmt.Errorf("image build failed: %w\n%s", err, buildOutput.String())
	}
	return nil
}

func parseBuildProgress(line string) string {
	lo := strings.ToLower(line)

	if strings.HasPrefix(strings.ToUpper(line), "STEP ") {
		switch {
		case strings.Contains(lo, "from python"):
			return "Pulling Python base image..."
		case strings.Contains(lo, "apt-get"):
			return "Installing system packages..."
		case strings.Contains(lo, "pip install"):
			return "Installing Python packages..."
		case strings.Contains(lo, "copy sam-str"):
			return "Copying STR binary..."
		case strings.Contains(lo, "copy tools"):
			return "Copying tool binaries..."
		case strings.Contains(lo, "entrypoint"):
			return "Finalizing image..."
		}
	}

	if strings.HasPrefix(lo, "copying blob") || strings.HasPrefix(lo, "getting image") {
		return "Downloading base image..."
	}

	if strings.Contains(line, "Downloading") && (strings.Contains(line, ".whl") || strings.Contains(line, ".tar")) {
		return "Downloading Python packages..."
	}
	if strings.Contains(line, "Installing collected packages") {
		return "Installing Python packages..."
	}
	if strings.Contains(line, "Successfully installed") {
		return "Python packages installed"
	}

	if strings.Contains(lo, "installing dependencies") {
		return "Installing browser dependencies..."
	}

	return ""
}

func (m *ImageManager) Tag() string {
	return m.imageTag
}

const baseImageTag = "sam-str-base:latest"

func generateBaseDockerfile() string {
	return `FROM python:3.12-slim

# Install system dependencies for common Python tools.
# Includes libraries needed by Kaleido's bundled Chromium (plotly chart rendering),
# plus ffmpeg and ImageMagick for media processing.
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    g++ \
    pkg-config \
    libcairo2-dev \
    libpango1.0-dev \
    libgl1-mesa-dev \
    fonts-dejavu \
    ffmpeg \
    libavformat-dev \
    libavcodec-dev \
    libavdevice-dev \
    libavutil-dev \
    libavfilter-dev \
    libswscale-dev \
    libswresample-dev \
    imagemagick \
    libexpat1 \
    libnspr4 \
    libnss3 \
    libx11-6 \
    libxcomposite1 \
    libxdamage1 \
    libxext6 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libasound2 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libpango-1.0-0 \
    libcairo2 \
    libdbus-1-3 \
    chromium \
    && rm -rf /var/lib/apt/lists/*

# Install LaTeX for manim MathTex/Tex rendering.
RUN apt-get update && apt-get install -y --no-install-recommends \
    texlive-latex-base \
    texlive-fonts-recommended \
    texlive-latex-extra \
    dvisvgm \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js for tools that require it (e.g., Claude Code CLI).
RUN apt-get update && apt-get install -y --no-install-recommends \
    nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# Install uv for fast per-tool venv creation.
RUN pip install --no-cache-dir uv

# Force source builds for PyAV so it links against the system ffmpeg.
# Prebuilt PyAV wheels bundle ffmpeg libraries compiled with CPU instructions
# that cause SIGILL under QEMU arm64 emulation (e.g. Podman on macOS).
ENV UV_NO_BINARY=av

# Create work directories.
RUN mkdir -p /app /work /tools
`
}

func generateDockerfile() string {
	return `FROM ` + baseImageTag + `

# Bundle sam-tool-sdk source so tool venvs can install it offline.
COPY sam-tool-sdk /tmp/sam-tool-sdk

# Copy the pre-compiled STR binary.
COPY sam-str /app/sam-str
RUN chmod +x /app/sam-str

# Set working directory.
WORKDIR /app

ENTRYPOINT ["/app/sam-str"]
`
}
