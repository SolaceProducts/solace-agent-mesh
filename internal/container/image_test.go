// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultImageTag(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{"1.0.0", "sam-str:1.0.0"},
		{"dev", "sam-str:dev"},
		{"", "sam-str:dev"},
		{"0.5.0-rc1", "sam-str:0.5.0-rc1"},
	}
	for _, tt := range tests {
		got := DefaultImageTag(tt.version)
		if got != tt.want {
			t.Errorf("DefaultImageTag(%q) = %q, want %q", tt.version, got, tt.want)
		}
	}
}

func TestResolveSTRBinary_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sam-str")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	localPath, cleanup, err := ResolveSTRBinary(path)
	if err != nil {
		t.Fatalf("ResolveSTRBinary: %v", err)
	}
	if cleanup != nil {
		t.Error("expected nil cleanup for local path")
	}
	if localPath != path {
		t.Errorf("localPath = %q, want %q", localPath, path)
	}
}

func TestResolveSTRBinary_MissingPath(t *testing.T) {
	_, _, err := ResolveSTRBinary("/nonexistent/sam-str")
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestResolveSTRBinary_URL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake-binary-content"))
	}))
	defer srv.Close()

	localPath, cleanup, err := ResolveSTRBinary(srv.URL + "/sam-str")
	if err != nil {
		t.Fatalf("ResolveSTRBinary: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup for URL download")
	}
	defer cleanup()

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "fake-binary-content" {
		t.Errorf("content = %q", data)
	}
}

func TestResolveSTRBinary_URLNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, _, err := ResolveSTRBinary(srv.URL + "/sam-str")
	if err == nil {
		t.Fatal("expected error for 404 URL")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error = %q, want to contain 'HTTP 404'", err.Error())
	}
}

func TestResolveSTRBinary_NoSource(t *testing.T) {
	_, _, err := ResolveSTRBinary("")
	if err == nil {
		t.Skip("~/.sam/config.yaml has container.str_binary set")
	}
	if !strings.Contains(err.Error(), "no sam-str binary specified") {
		t.Errorf("error = %q, want to contain 'no sam-str binary specified'", err.Error())
	}
}

func TestIsURL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"https://example.com/binary", true},
		{"http://example.com/binary", true},
		{"./bin/sam-str", false},
		{"/usr/local/bin/sam-str", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isURL(tt.input)
		if got != tt.want {
			t.Errorf("isURL(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestDockerfileContentEmbedded(t *testing.T) {
	if dockerfileContent == "" {
		t.Fatal("embedded Dockerfile is empty")
	}
	if !strings.Contains(dockerfileContent, "FROM debian:bookworm") {
		t.Error("Dockerfile doesn't contain expected base image")
	}
	if !strings.Contains(dockerfileContent, "sam-str") {
		t.Error("Dockerfile doesn't reference sam-str")
	}
	if !strings.Contains(dockerfileContent, "bubblewrap") {
		t.Error("Dockerfile doesn't install bubblewrap")
	}
}

func TestDockerfileBuildsEventMeshTool(t *testing.T) {
	if !strings.Contains(dockerfileContent, "./tools/go/event-mesh") {
		t.Error("Dockerfile.str does not build the event-mesh tool")
	}
	if !strings.Contains(dockerfileContent, "/tools/event-mesh/manifest.yaml") {
		t.Error("Dockerfile.str does not copy the event-mesh manifest")
	}
	for line := range strings.SplitSeq(dockerfileContent, "\n") {
		if strings.Contains(line, "tools/go/event-mesh") && strings.Contains(line, "nosolace") {
			t.Errorf("event-mesh built with nosolace (panics on broker connect): %q", line)
		}
	}
}

type mockRuntime struct {
	typ         RuntimeType
	buildCalled bool
	buildErr    error
	buildTag    string
	buildCheck  func(contextDir string) error
}

func (m *mockRuntime) Type() RuntimeType { return m.typ }
func (m *mockRuntime) ImageExists(ctx context.Context, tag string) (bool, error) {
	return false, nil
}
func (m *mockRuntime) Build(ctx context.Context, contextDir string, tag string, progress io.Writer) error {
	m.buildCalled = true
	m.buildTag = tag
	if m.buildCheck != nil {
		if err := m.buildCheck(contextDir); err != nil {
			return err
		}
	}
	return m.buildErr
}
func (m *mockRuntime) Run(ctx context.Context, opts RunOptions) (string, error) {
	return "", nil
}
func (m *mockRuntime) Stop(ctx context.Context, containerID string, timeout time.Duration) error {
	return nil
}
func (m *mockRuntime) Remove(ctx context.Context, containerID string) error { return nil }
func (m *mockRuntime) ListContainers(ctx context.Context, labelFilter map[string]string) ([]ContainerInfo, error) {
	return nil, nil
}

func TestBuildSTRImage_Success(t *testing.T) {
	dir := t.TempDir()
	var machine elf.Machine
	switch HostContainerArch() {
	case "amd64":
		machine = elf.EM_X86_64
	case "arm64":
		machine = elf.EM_AARCH64
	default:
		t.Skipf("unsupported arch: %s", HostContainerArch())
	}
	binaryPath := writeMinimalELF(t, dir, "sam-str", machine)

	rt := &mockRuntime{
		typ: RuntimeDocker,
		buildCheck: func(contextDir string) error {
			dfPath := filepath.Join(contextDir, "Dockerfile")
			if _, err := os.Stat(dfPath); err != nil {
				return fmt.Errorf("Dockerfile not in build context: %w", err)
			}
			binPath := filepath.Join(contextDir, "sam-str")
			if _, err := os.Stat(binPath); err != nil {
				return fmt.Errorf("sam-str not in build context: %w", err)
			}
			return nil
		},
	}
	ctx := context.Background()

	err := BuildSTRImage(ctx, rt, BuildOptions{
		STRBinary: binaryPath,
		Version:   "1.0.0",
		Progress:  io.Discard,
	})
	if err != nil {
		t.Fatalf("BuildSTRImage: %v", err)
	}

	if !rt.buildCalled {
		t.Error("Build was not called on runtime")
	}
	if rt.buildTag != "sam-str:1.0.0" {
		t.Errorf("buildTag = %q, want 'sam-str:1.0.0'", rt.buildTag)
	}
}

func TestBuildSTRImage_BuildError(t *testing.T) {
	dir := t.TempDir()
	var machine elf.Machine
	switch HostContainerArch() {
	case "amd64":
		machine = elf.EM_X86_64
	case "arm64":
		machine = elf.EM_AARCH64
	default:
		t.Skipf("unsupported arch: %s", HostContainerArch())
	}
	binaryPath := writeMinimalELF(t, dir, "sam-str", machine)

	rt := &mockRuntime{typ: RuntimeDocker, buildErr: fmt.Errorf("build failed")}
	ctx := context.Background()

	err := BuildSTRImage(ctx, rt, BuildOptions{
		STRBinary: binaryPath,
		Version:   "1.0.0",
		Progress:  io.Discard,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, rt.buildErr) && !strings.Contains(err.Error(), "build failed") {
		t.Errorf("error = %q, want to contain 'build failed'", err.Error())
	}
}

func TestBuildSTRImage_InvalidBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad-binary")
	if err := os.WriteFile(path, []byte("not an ELF"), 0o755); err != nil {
		t.Fatal(err)
	}

	rt := &mockRuntime{typ: RuntimeDocker}
	ctx := context.Background()

	err := BuildSTRImage(ctx, rt, BuildOptions{
		STRBinary: path,
		Progress:  io.Discard,
	})
	if err == nil {
		t.Fatal("expected error for invalid binary")
	}
	if rt.buildCalled {
		t.Error("Build should not have been called for invalid binary")
	}
}

func TestBuildSTRImage_NoBinary(t *testing.T) {
	rt := &mockRuntime{typ: RuntimeDocker}
	ctx := context.Background()

	err := BuildSTRImage(ctx, rt, BuildOptions{
		Progress: io.Discard,
	})
	if err == nil {
		t.Skip("config has str_binary set")
	}
	if !strings.Contains(err.Error(), "no sam-str binary specified") {
		t.Errorf("error = %q, want to contain instructions", err.Error())
	}
}
