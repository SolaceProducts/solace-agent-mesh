// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

type RuntimeType string

const (
	RuntimePodman   RuntimeType = "podman"
	RuntimeDocker   RuntimeType = "docker"
	RuntimeOrbStack RuntimeType = "orbstack"
)

type Runtime interface {
	Type() RuntimeType

	ImageExists(ctx context.Context, tag string) (bool, error)

	Build(ctx context.Context, contextDir string, tag string, progress io.Writer) error

	Run(ctx context.Context, opts RunOptions) (string, error)

	Stop(ctx context.Context, containerID string, timeout time.Duration) error

	Remove(ctx context.Context, containerID string) error

	ListContainers(ctx context.Context, labelFilter map[string]string) ([]ContainerInfo, error)
}

type RunOptions struct {
	Image   string
	Name    string
	Labels  map[string]string
	Env     []string
	Binds   []string
	Network string
	Cmd     []string
	Detach  bool
}

type ContainerInfo struct {
	ID     string
	Name   string
	Image  string
	State  string
	Status string
}

var ErrNoRuntime = fmt.Errorf("no container runtime found")

func Detect(logger *slog.Logger) (Runtime, error) {
	if path, err := exec.LookPath("podman"); err == nil {
		logger.Debug("detected container runtime", "type", "podman", "path", path)
		return newCLIRuntime("podman", RuntimePodman, logger), nil
	}

	if path, err := exec.LookPath("docker"); err == nil {
		typ := RuntimeDocker
		if out, err := exec.Command("docker", "info", "--format", "{{.OperatingSystem}}").Output(); err == nil {
			if strings.Contains(strings.ToLower(string(out)), "orbstack") {
				typ = RuntimeOrbStack
			}
		}
		logger.Debug("detected container runtime", "type", typ, "path", path)
		return newCLIRuntime("docker", typ, logger), nil
	}

	return nil, fmt.Errorf("%w\n\nInstall one of:\n  brew install podman && podman machine init && podman machine start\n  Docker Desktop: https://docker.com/products/docker-desktop\n  OrbStack: https://orbstack.dev", ErrNoRuntime)
}

func New(typ RuntimeType, logger *slog.Logger) (Runtime, error) {
	if typ == "" {
		return Detect(logger)
	}

	switch typ {
	case RuntimePodman:
		if _, err := exec.LookPath("podman"); err != nil {
			return nil, fmt.Errorf("podman not found in PATH")
		}
		return newCLIRuntime("podman", RuntimePodman, logger), nil
	case RuntimeDocker, RuntimeOrbStack:
		if _, err := exec.LookPath("docker"); err != nil {
			return nil, fmt.Errorf("docker not found in PATH")
		}
		return newCLIRuntime("docker", typ, logger), nil
	default:
		return nil, fmt.Errorf("unsupported container runtime: %q", typ)
	}
}
