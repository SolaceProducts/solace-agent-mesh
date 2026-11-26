// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/container"

	"gopkg.in/yaml.v3"
)

type mockContainerRuntime struct {
	typ          container.RuntimeType
	imageExists  bool
	runID        string
	runErr       error
	runOpts      container.RunOptions
	stopCalled   bool
	removeCalled bool
}

func (m *mockContainerRuntime) Type() container.RuntimeType { return m.typ }

func (m *mockContainerRuntime) ImageExists(_ context.Context, _ string) (bool, error) {
	return m.imageExists, nil
}

func (m *mockContainerRuntime) Build(_ context.Context, _ string, _ string, _ io.Writer) error {
	return nil
}

func (m *mockContainerRuntime) Run(_ context.Context, opts container.RunOptions) (string, error) {
	m.runOpts = opts
	if m.runErr != nil {
		return "", m.runErr
	}
	return m.runID, nil
}

func (m *mockContainerRuntime) Stop(_ context.Context, _ string, _ time.Duration) error {
	m.stopCalled = true
	return nil
}

func (m *mockContainerRuntime) Remove(_ context.Context, _ string) error {
	m.removeCalled = true
	return nil
}

func (m *mockContainerRuntime) ListContainers(_ context.Context, _ map[string]string) ([]container.ContainerInfo, error) {
	return nil, nil
}

func TestBuildSTRConfig(t *testing.T) {
	cfg := ContainerSTRConfig{
		WorkerID:  "test-worker",
		Namespace: "test-ns",
		ToolsDir:  "/host/tools",
	}

	result := buildSTRConfig(cfg, "127.0.0.1")

	data, err := yaml.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	yamlStr := string(data)
	for _, want := range []string{
		"worker_id: test-worker",
		"namespace: test-ns",
		"volume_root: /volumes",
		"mode: bwrap",
	} {
		if !strings.Contains(yamlStr, want) {
			t.Errorf("missing %q in config YAML:\n%s", want, yamlStr)
		}
	}
	if strings.Contains(yamlStr, "manifest_path") {
		t.Error("manifest_path must not appear in generated config")
	}
}

func TestBuildSTRConfigNoToolsDir(t *testing.T) {
	cfg := ContainerSTRConfig{
		WorkerID:  "test-worker",
		Namespace: "test-ns",
	}

	result := buildSTRConfig(cfg, "127.0.0.1")
	data, err := yaml.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	yamlStr := string(data)
	if strings.Contains(yamlStr, "manifest_path") {
		t.Error("manifest_path must not appear when ToolsDir is empty")
	}
}

func TestBuildBindMounts(t *testing.T) {
	binds := buildBindMounts("/host/volumes", "/tmp/str.yaml", "/host/tools", "")

	expected := []string{
		"/host/volumes:/volumes",
		"/tmp/str.yaml:/mnt/config/str.yaml:ro",
		"/host/tools:/mnt/tools:ro",
	}

	if len(binds) != len(expected) {
		t.Fatalf("expected %d binds, got %d", len(expected), len(binds))
	}
	for i, want := range expected {
		if binds[i] != want {
			t.Errorf("binds[%d] = %q, want %q", i, binds[i], want)
		}
	}
}

func TestBuildBindMountsNoToolsDir(t *testing.T) {
	binds := buildBindMounts("/host/volumes", "/tmp/str.yaml", "", "")

	if len(binds) != 2 {
		t.Fatalf("expected 2 binds (no optional), got %d: %v", len(binds), binds)
	}
}

func TestBuildBindMountsHostMountScope(t *testing.T) {
	binds := buildBindMounts("/host/volumes", "/tmp/str.yaml", "", "/Users/alex")
	wantSuffix := "/Users/alex:/host/home"
	found := false
	for _, b := range binds {
		if b == wantSuffix {
			found = true
		}
	}
	if !found {
		t.Errorf("expected scope bind %q in %v", wantSuffix, binds)
	}
}

func TestResolveBrokerConnectivity(t *testing.T) {
	rt := &mockContainerRuntime{typ: container.RuntimePodman}
	network, host := resolveBrokerConnectivity(rt)

	if host == "" {
		t.Error("brokerHost should not be empty")
	}
	_ = network
}

func TestWriteSTRConfigFile(t *testing.T) {
	cfg := map[string]any{
		"app_config": map[string]any{
			"worker_id": "test",
		},
	}

	path, err := writeSTRConfigFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(path) }()

	if path == "" {
		t.Fatal("expected non-empty path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("generated config is not valid YAML: %v", err)
	}
}

func TestContainerSTRStop(t *testing.T) {
	rt := &mockContainerRuntime{
		typ:   container.RuntimeDocker,
		runID: "container-123",
	}

	cstr := &ContainerSTR{
		rt:          rt,
		containerID: "container-123",
		logger:      slog.Default(),
	}

	cstr.Stop(5 * time.Second)

	if !rt.stopCalled {
		t.Error("Stop should call runtime.Stop")
	}
	if !rt.removeCalled {
		t.Error("Stop should call runtime.Remove")
	}
}

func TestContainerSTRStopCleansTempFiles(t *testing.T) {
	rt := &mockContainerRuntime{
		typ:   container.RuntimeDocker,
		runID: "container-123",
	}

	cfg := map[string]any{"test": true}
	tmpPath, err := writeSTRConfigFile(cfg)
	if err != nil {
		t.Fatal(err)
	}

	cstr := &ContainerSTR{
		rt:          rt,
		containerID: "container-123",
		logger:      slog.Default(),
		tempFiles:   []string{tmpPath},
	}

	cstr.Stop(5 * time.Second)

	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("temp file should have been removed")
	}
}

func TestContainerSTRRunError(t *testing.T) {
	rt := &mockContainerRuntime{
		typ:         container.RuntimeDocker,
		imageExists: true,
		runErr:      fmt.Errorf("container start failed"),
	}

	_, err := rt.Run(context.Background(), container.RunOptions{})
	if err == nil {
		t.Error("expected error from Run")
	}
}
