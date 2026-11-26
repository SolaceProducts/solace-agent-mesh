// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type CLIRuntime struct {
	binary string
	typ    RuntimeType
	logger *slog.Logger
}

func newCLIRuntime(binary string, typ RuntimeType, logger *slog.Logger) *CLIRuntime {
	return &CLIRuntime{
		binary: binary,
		typ:    typ,
		logger: logger,
	}
}

func (r *CLIRuntime) Type() RuntimeType { return r.typ }

func (r *CLIRuntime) ImageExists(ctx context.Context, tag string) (bool, error) {
	cmd := exec.CommandContext(ctx, r.binary, "image", "inspect", tag)
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

func (r *CLIRuntime) Build(ctx context.Context, contextDir string, tag string, progress io.Writer) error {
	args := []string{"build", "-t", tag, contextDir}
	r.logger.Debug("building container image", "cmd", r.binary, "args", args)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Stdout = progress
	cmd.Stderr = progress
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build failed: %w", r.binary, err)
	}
	return nil
}

func (r *CLIRuntime) Run(ctx context.Context, opts RunOptions) (string, error) {
	args := r.buildRunArgs(opts)
	r.logger.Debug("starting container", "cmd", r.binary, "args", redactedRunArgs(args))

	cmd := exec.CommandContext(ctx, r.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s run failed: %w\nstderr: %s", r.binary, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (r *CLIRuntime) Stop(ctx context.Context, containerID string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	args := []string{"stop", "-t", strconv.Itoa(secs), containerID}
	r.logger.Debug("stopping container", "container", containerID, "timeout", timeout)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s stop failed: %w\n%s", r.binary, err, string(out))
	}
	return nil
}

func (r *CLIRuntime) Remove(ctx context.Context, containerID string) error {
	args := []string{"rm", containerID}
	r.logger.Debug("removing container", "container", containerID)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s rm failed: %w\n%s", r.binary, err, string(out))
	}
	return nil
}

func (r *CLIRuntime) ListContainers(ctx context.Context, labelFilter map[string]string) ([]ContainerInfo, error) {
	args := []string{"ps", "-a", "--format", "json"}
	for k, v := range labelFilter {
		args = append(args, "--filter", fmt.Sprintf("label=%s=%s", k, v))
	}

	cmd := exec.CommandContext(ctx, r.binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s ps failed: %w\n%s", r.binary, err, stderr.String())
	}

	return parseContainerList(stdout.Bytes(), r.typ)
}

func (r *CLIRuntime) buildRunArgs(opts RunOptions) []string {
	args := []string{"run"}

	if opts.Detach {
		args = append(args, "--detach")
	}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	for k, v := range opts.Labels {
		args = append(args, "--label", fmt.Sprintf("%s=%s", k, v))
	}
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}
	for _, bind := range opts.Binds {
		args = append(args, "-v", bind)
	}
	if opts.Network != "" {
		args = append(args, "--network", opts.Network)
	}

	args = append(args, opts.Image)
	args = append(args, opts.Cmd...)
	return args
}

func redactedRunArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i < len(out)-1; i++ {
		if out[i] != "--env" {
			continue
		}
		if name, _, found := strings.Cut(out[i+1], "="); found {
			out[i+1] = name + "=[REDACTED]"
		}
	}
	return out
}

func parseContainerList(data []byte, typ RuntimeType) ([]ContainerInfo, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}

	var containers []containerJSON
	if data[0] == '[' {
		if err := json.Unmarshal(data, &containers); err != nil {
			return nil, fmt.Errorf("parse container list: %w", err)
		}
	} else {
		for _, line := range bytes.Split(data, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var c containerJSON
			if err := json.Unmarshal(line, &c); err != nil {
				return nil, fmt.Errorf("parse container entry: %w", err)
			}
			containers = append(containers, c)
		}
	}

	result := make([]ContainerInfo, len(containers))
	for i, c := range containers {
		result[i] = ContainerInfo{
			ID:     c.ID,
			Name:   c.name(),
			Image:  c.Image,
			State:  c.State,
			Status: c.Status,
		}
	}
	return result, nil
}

type containerJSON struct {
	ID     string `json:"Id"`
	Names  any    `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
}

func (c *containerJSON) name() string {
	switch v := c.Names.(type) {
	case string:
		return strings.TrimPrefix(v, "/")
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return strings.TrimPrefix(s, "/")
			}
		}
	}
	return ""
}
