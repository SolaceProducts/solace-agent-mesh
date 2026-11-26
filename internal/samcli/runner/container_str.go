// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	goruntime "runtime"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/container"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type ContainerSTR struct {
	rt          container.Runtime
	containerID string
	logger      *slog.Logger
	tempFiles   []string
}

type ContainerSTRConfig struct {
	ImageTag string

	Namespace string

	ToolsDir string

	VolumeRoot string

	BrokerHost string

	BrokerPort int

	WorkerID string

	HostMountScope string
}

func StartContainerSTR(ctx context.Context, cfg ContainerSTRConfig, logger *slog.Logger) (*ContainerSTR, error) {
	if cfg.WorkerID == "" {
		cfg.WorkerID = "container-worker"
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "solace-agent-mesh"
	}

	rt, err := container.Detect(logger)
	if err != nil {
		return nil, fmt.Errorf("no container runtime found (required for volume-backed tools): %w", err)
	}
	logger.Info("detected container runtime", "type", rt.Type())

	exists, err := rt.ImageExists(ctx, cfg.ImageTag)
	if err != nil {
		return nil, fmt.Errorf("check STR image %q: %w", cfg.ImageTag, err)
	}
	if !exists {
		logger.Info("STR image not found, building", "tag", cfg.ImageTag)
		if buildErr := container.BuildSTRImage(ctx, rt, container.BuildOptions{
			Tag:      cfg.ImageTag,
			Progress: os.Stderr,
		}); buildErr != nil {
			return nil, fmt.Errorf("build STR image %q: %w", cfg.ImageTag, buildErr)
		}
	}

	volumeRoot := cfg.VolumeRoot
	if volumeRoot == "" {
		volumeRoot = defaultVolumeRoot()
	}
	if mkdirErr := os.MkdirAll(volumeRoot, 0o755); mkdirErr != nil {
		return nil, fmt.Errorf("create volume root %s: %w", volumeRoot, mkdirErr)
	}

	network, brokerHost := resolveBrokerConnectivity(rt)

	hostScope := cfg.HostMountScope
	if hostScope == "" {
		hostScope = volume.DefaultHostMountScope()
	}

	strConfig := buildSTRConfig(cfg, brokerHost)
	tmpPath, err := writeSTRConfigFile(strConfig)
	if err != nil {
		return nil, fmt.Errorf("write STR config: %w", err)
	}

	brokerPort := strconv.Itoa(cfg.BrokerPort)
	cmd := []string{"--config", "/mnt/config/str.yaml"}
	if cfg.ToolsDir != "" {
		cmd = append(cmd, "--tools-dir", "/mnt/tools")
	}
	opts := container.RunOptions{
		Image:   cfg.ImageTag,
		Name:    "sam-str-" + cfg.WorkerID,
		Labels:  map[string]string{container.LabelComponent: "str"},
		Network: network,
		Detach:  true,
		Env: []string{
			"SAM_DEV_BROKER_HOST=" + brokerHost,
			"SAM_DEV_BROKER_PORT=" + brokerPort,
			"DEV_BROKER_HOST=" + brokerHost,
			"DEV_BROKER_PORT=" + brokerPort,
		},
		Binds: buildBindMounts(volumeRoot, tmpPath, cfg.ToolsDir, hostScope),
		Cmd:   cmd,
	}

	logger.Info("starting STR container",
		"image", cfg.ImageTag,
		"network", network,
		"broker", brokerHost+":"+brokerPort,
		"volumeRoot", volumeRoot,
		"hostMountScope", hostScope)

	containerID, err := rt.Run(ctx, opts)
	if err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("start STR container: %w", err)
	}

	logger.Info("STR container started", "containerID", containerID[:minLen(containerID, 12)])

	return &ContainerSTR{
		rt:          rt,
		containerID: containerID,
		logger:      logger,
		tempFiles:   []string{tmpPath},
	}, nil
}

func (c *ContainerSTR) Stop(timeout time.Duration) {
	ctx := context.Background()

	c.logger.Debug("stopping STR container", "containerID", c.containerID[:minLen(c.containerID, 12)])
	if err := c.rt.Stop(ctx, c.containerID, timeout); err != nil {
		c.logger.Warn("error stopping STR container", "error", err)
	}

	if err := c.rt.Remove(ctx, c.containerID); err != nil {
		c.logger.Warn("error removing STR container", "error", err)
	}

	for _, f := range c.tempFiles {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			c.logger.Debug("failed to remove temp file", "file", f, "error", err)
		}
	}
}

func resolveBrokerConnectivity(rt container.Runtime) (network, brokerHost string) {
	if goruntime.GOOS != "darwin" {
		return "host", "127.0.0.1"
	}

	if rt.Type() == container.RuntimePodman {
		return "host", "127.0.0.1"
	}

	return "", "host.docker.internal"
}

func buildSTRConfig(cfg ContainerSTRConfig, brokerHost string) map[string]any {
	sandboxCfg := map[string]any{
		"mode": "bwrap",
	}

	appConfig := map[string]any{
		"worker_id":   cfg.WorkerID,
		"namespace":   cfg.Namespace,
		"volume_root": "/volumes",
		"sandbox":     sandboxCfg,
	}

	return map[string]any{
		"app_config": appConfig,
	}
}

func buildBindMounts(volumeRoot, configPath, toolsDir, hostMountScope string) []string {
	binds := []string{
		volumeRoot + ":/volumes",
		configPath + ":/mnt/config/str.yaml:ro",
	}
	if toolsDir != "" {
		binds = append(binds, toolsDir+":/mnt/tools:ro")
	}
	if hostMountScope != "" {
		binds = append(binds, hostMountScope+":"+volume.ContainerMountPoint)
	}
	return binds
}

func writeSTRConfigFile(cfg map[string]any) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal STR config: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "sam-str-config-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create temp STR config: %w", err)
	}

	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("write temp STR config: %w", writeErr)
	}
	_ = tmpFile.Close()

	return tmpFile.Name(), nil
}

func minLen(s string, n int) int {
	if len(s) < n {
		return len(s)
	}
	return n
}
