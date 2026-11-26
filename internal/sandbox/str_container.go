// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type STRContainer struct {
	rt          *Runtime
	logger      *slog.Logger
	imageTag    string
	containerID string
	mu          sync.Mutex
}

type STRContainerConfig struct {
	Name string

	BrokerHost string

	BrokerPort int

	ConfigPath string

	ConfigData []byte

	ToolsDir string

	WorkDir string

	EnvVars map[string]string

	ExtraVolumes []string
}

func NewSTRContainer(rt *Runtime, imageTag string, logger *slog.Logger) *STRContainer {
	return &STRContainer{
		rt:       rt,
		logger:   logger.With("logger_name", "str-container"),
		imageTag: imageTag,
	}
}

func (c *STRContainer) Start(ctx context.Context, cfg STRContainerConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.containerID != "" {
		return fmt.Errorf("container already running: %s", c.containerID)
	}

	rmCmd := exec.CommandContext(ctx, c.rt.Path, "rm", "-f", cfg.Name)
	_ = rmCmd.Run()

	args := c.buildRunArgs(cfg)

	c.logger.Info("starting STR container",
		"name", cfg.Name,
		"image", c.imageTag,
		"broker", fmt.Sprintf("%s:%d", cfg.BrokerHost, cfg.BrokerPort))

	cmd := exec.CommandContext(ctx, c.rt.Path, args...)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	c.containerID = strings.TrimSpace(string(out))
	c.logger.Info("STR container started", "id", c.containerID[:12], "name", cfg.Name)
	return nil
}

func (c *STRContainer) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.containerID == "" {
		return nil
	}

	c.logger.Info("stopping STR container", "id", c.containerID[:12])

	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	stopCmd := exec.CommandContext(stopCtx, c.rt.Path, "stop", "-t", "5", c.containerID)
	if out, err := stopCmd.CombinedOutput(); err != nil {
		c.logger.Warn("container stop error", "error", err, "output", string(out))
	}

	rmCmd := exec.CommandContext(ctx, c.rt.Path, "rm", "-f", c.containerID)
	if out, err := rmCmd.CombinedOutput(); err != nil {
		c.logger.Warn("container rm error", "error", err, "output", string(out))
	}

	c.containerID = ""
	return nil
}

func (c *STRContainer) IsRunning(ctx context.Context) bool {
	c.mu.Lock()
	id := c.containerID
	c.mu.Unlock()

	if id == "" {
		return false
	}

	cmd := exec.CommandContext(ctx, c.rt.Path, "inspect", "--format", "{{.State.Running}}", id)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func (c *STRContainer) Logs(ctx context.Context, lines int) (string, error) {
	c.mu.Lock()
	id := c.containerID
	c.mu.Unlock()

	if id == "" {
		return "", fmt.Errorf("no container running")
	}

	cmd := exec.CommandContext(ctx, c.rt.Path, "logs", "--tail", fmt.Sprintf("%d", lines), id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("get logs: %w", err)
	}
	return string(out), nil
}

func (c *STRContainer) buildRunArgs(cfg STRContainerConfig) []string {
	args := []string{
		"run", "-d",
		"--init",
		"--name", cfg.Name,
	}

	brokerHost := cfg.BrokerHost
	if brokerHost == "" {
		brokerHost = hostGateway(c.rt.Name)
	}

	args = append(args,
		"-e", fmt.Sprintf("SAM_DEV_BROKER_HOST=%s", brokerHost),
		"-e", fmt.Sprintf("SAM_DEV_BROKER_PORT=%d", cfg.BrokerPort),
		"-e", fmt.Sprintf("DEV_BROKER_HOST=%s", brokerHost),
		"-e", fmt.Sprintf("DEV_BROKER_PORT=%d", cfg.BrokerPort),
	)

	for k, v := range cfg.EnvVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	configPath := cfg.ConfigPath
	if configPath == "" && len(cfg.ConfigData) > 0 {
		tmpFile, err := writeTempConfig(cfg.ConfigData)
		if err != nil {
			c.logger.Error("failed to write temp config", "name", cfg.Name, "error", err)
		} else {
			configPath = tmpFile
		}
	}
	if configPath != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/app/config.yaml:ro", configPath))
	}

	if cfg.ToolsDir != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/tools", cfg.ToolsDir))
	}

	workDir := cfg.WorkDir
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "sam-str-work")
		_ = os.MkdirAll(workDir, 0755)
	}
	args = append(args, "-v", fmt.Sprintf("%s:/work", workDir))

	for _, vol := range cfg.ExtraVolumes {
		args = append(args, "-v", vol)
	}

	args = append(args, c.imageTag)
	if configPath != "" {
		args = append(args, "--config", "/app/config.yaml")
	}

	return args
}

func hostGateway(runtimeName string) string {
	switch runtimeName {
	case "podman":
		return "host.containers.internal"
	default:
		return "host.docker.internal"
	}
}

func writeTempConfig(data []byte) (string, error) {
	dir := filepath.Join(os.TempDir(), "sam-str-configs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "str-*.yaml")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}
