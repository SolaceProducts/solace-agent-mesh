// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sandbox

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"testing"
)

func TestDetectRuntime_ReturnsNilWhenNoneAvailable(t *testing.T) {
	rt := DetectRuntime()
	if rt != nil {
		t.Logf("detected runtime: %s %s at %s", rt.Name, rt.Version, rt.Path)
	} else {
		t.Log("no container runtime detected (expected in many CI environments)")
	}
}

func TestDetectRuntime_PrefersPodman(t *testing.T) {
	hasPodman := exec.Command("podman", "--version").Run() == nil
	hasDocker := exec.Command("docker", "--version").Run() == nil

	if !hasPodman || !hasDocker {
		t.Skip("both podman and docker required for this test")
	}

	rt := DetectRuntime()
	if rt == nil {
		t.Fatal("expected a runtime to be detected")
	}
	if rt.Name != "podman" {
		t.Errorf("expected podman to be preferred, got %s", rt.Name)
	}
}

func TestCheckInstallStatus(t *testing.T) {
	status := CheckInstallStatus()

	if status.Available {
		if status.Runtime == nil {
			t.Error("Available is true but Runtime is nil")
		}
		if status.Runtime.Name == "" {
			t.Error("Runtime.Name is empty")
		}
		if status.Runtime.Path == "" {
			t.Error("Runtime.Path is empty")
		}
	}

	if !status.Available && runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("brew"); err == nil {
			if !status.CanInstall {
				t.Error("expected CanInstall=true on macOS with Homebrew when no runtime available")
			}
			if status.InstallMethod != "brew install podman" {
				t.Errorf("expected 'brew install podman', got %q", status.InstallMethod)
			}
		}
	}
}

func TestCanInstallPodman_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}

	canInstall, method := canInstallPodman()
	hasBrew := exec.Command("brew", "--version").Run() == nil

	if hasBrew && !canInstall {
		t.Error("expected canInstall=true with Homebrew present")
	}
	if hasBrew && method != "brew install podman" {
		t.Errorf("expected 'brew install podman', got %q", method)
	}
}

func TestCanInstallPodman_Stubs(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("testing non-macOS stubs")
	}

	canInstall, method := canInstallPodman()
	if canInstall {
		t.Error("expected canInstall=false on non-macOS platforms (stubbed)")
	}
	if method != "" {
		t.Errorf("expected empty method, got %q", method)
	}
}

func TestHostGateway(t *testing.T) {
	tests := []struct {
		runtime string
		want    string
	}{
		{"podman", "host.containers.internal"},
		{"docker", "host.docker.internal"},
		{"unknown", "host.docker.internal"},
	}
	for _, tt := range tests {
		got := hostGateway(tt.runtime)
		if got != tt.want {
			t.Errorf("hostGateway(%q) = %q, want %q", tt.runtime, got, tt.want)
		}
	}
}

func TestSTRContainerConfig_BuildRunArgs(t *testing.T) {
	rt := &Runtime{Name: "podman", Path: "/usr/local/bin/podman"}
	container := NewSTRContainer(rt, "sam-str:latest", nopLogger())

	cfg := STRContainerConfig{
		Name:       "sam-str-0",
		BrokerPort: 55555,
		ToolsDir:   "/tmp/tools",
		EnvVars:    map[string]string{"FOO": "bar"},
	}

	args := container.buildRunArgs(cfg)

	assertContains(t, args, "run")
	assertContains(t, args, "-d")
	assertContains(t, args, "sam-str-0")
	assertContains(t, args, "SAM_DEV_BROKER_HOST=host.containers.internal")
	assertContains(t, args, "SAM_DEV_BROKER_PORT=55555")
	assertContains(t, args, "FOO=bar")
	assertContains(t, args, "sam-str:latest")
}

func TestSTRContainerConfig_BuildRunArgs_DockerHost(t *testing.T) {
	rt := &Runtime{Name: "docker", Path: "/usr/local/bin/docker"}
	container := NewSTRContainer(rt, "sam-str:latest", nopLogger())

	cfg := STRContainerConfig{
		Name:       "sam-str-0",
		BrokerPort: 55555,
	}

	args := container.buildRunArgs(cfg)
	assertContains(t, args, "SAM_DEV_BROKER_HOST=host.docker.internal")
}

func TestImageManager_Tag(t *testing.T) {
	rt := &Runtime{Name: "podman", Path: "/usr/local/bin/podman"}
	im := NewImageManager(rt, nopLogger())
	if im.Tag() != "sam-str:latest" {
		t.Errorf("expected 'sam-str:latest', got %q", im.Tag())
	}
}

func TestGenerateDockerfile(t *testing.T) {
	base := generateBaseDockerfile()
	if base == "" {
		t.Fatal("expected non-empty base Dockerfile")
	}
	for _, want := range []string{"FROM python:", "apt-get", "nodejs", "chromium"} {
		if !containsStr(base, want) {
			t.Errorf("base Dockerfile missing %q", want)
		}
	}

	df := generateDockerfile()
	if df == "" {
		t.Fatal("expected non-empty Dockerfile")
	}
	for _, want := range []string{"FROM " + baseImageTag, "ENTRYPOINT", "sam-str", "/app"} {
		if !containsStr(df, want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
}

func TestOrchestrator_WorkerCount(t *testing.T) {
	rt := &Runtime{Name: "podman", Path: "/usr/local/bin/podman"}
	orch := NewOrchestrator(rt, nopLogger())
	if orch.WorkerCount() != 0 {
		t.Errorf("expected 0 workers, got %d", orch.WorkerCount())
	}
}

func TestOrchestrator_StopAll_Empty(t *testing.T) {
	rt := &Runtime{Name: "podman", Path: "/usr/local/bin/podman"}
	orch := NewOrchestrator(rt, nopLogger())
	err := orch.StopAll(context.Background())
	if err != nil {
		t.Errorf("StopAll on empty orchestrator should not error: %v", err)
	}
}

func TestOrchestrator_LaunchAfterStop(t *testing.T) {
	rt := &Runtime{Name: "podman", Path: "/usr/local/bin/podman"}
	orch := NewOrchestrator(rt, nopLogger())
	_ = orch.StopAll(context.Background())

	_, err := orch.LaunchSTR(context.Background(), STRContainerConfig{Name: "test"})
	if err == nil {
		t.Error("expected error launching after stop")
	}
}

func assertContains(t *testing.T, args []string, substr string) {
	t.Helper()
	for _, a := range args {
		if containsStr(a, substr) {
			return
		}
	}
	t.Errorf("args %v missing %q", args, substr)
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func nopLogger() *slog.Logger {
	return slog.Default()
}
