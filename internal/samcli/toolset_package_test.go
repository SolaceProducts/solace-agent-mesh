// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/toolinit"
	"github.com/spf13/cobra"
)

func authedTestToken(t *testing.T) {
	t.Helper()
	t.Setenv("SAM_PLATFORM_TOKEN", "test-token")
}

func emptyCredentialCache(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("SAM_PLATFORM_TOKEN", "")
}

func newPackageCmdForTest() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd, out
}

func writeKindNoneToolset(t *testing.T, name string) (repoRoot, srcDir string) {
	t.Helper()
	repoRoot = t.TempDir()
	srcDir = filepath.Join(repoRoot, "toolsets", name, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	for rel, body := range map[string]string{
		"manifest.yaml": "tools: []\n",
		"tool.txt":      "hello",
	} {
		if err := os.WriteFile(filepath.Join(srcDir, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return repoRoot, srcDir
}

func zipEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	t.Cleanup(func() { _ = zr.Close() })
	names := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return names
}

func TestResolveToolBuildTarget_EnvWins(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "3.11")
	osS, arch, pythonVer, source, err := resolveToolBuildTarget(context.Background(), "http://unused.invalid")
	if err != nil {
		t.Fatalf("resolveToolBuildTarget: %v", err)
	}
	if osS != "linux" || arch != "amd64" || source != targetSourceEnv {
		t.Errorf("got (%s,%s,%s); want (linux,amd64,%s)", osS, arch, source, targetSourceEnv)
	}
	if pythonVer != "3.11" {
		t.Errorf("pythonVer = %q; want 3.11 (carried from SAM_TOOL_PYTHON_VERSION)", pythonVer)
	}
}

func TestResolveToolBuildTarget_DefaultFallback(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")
	osS, arch, pythonVer, source, err := resolveToolBuildTarget(context.Background(), "")
	if err != nil {
		t.Fatalf("resolveToolBuildTarget: %v", err)
	}
	if osS != "linux" || arch != "arm64" || source != "default" {
		t.Errorf("got (%s,%s,%s); want (linux,arm64,default)", osS, arch, source)
	}
	if pythonVer != "" {
		t.Errorf("pythonVer = %q; want empty (no env, no platform)", pythonVer)
	}
}

func TestResolveToolBuildTarget_SendsAuthHeader(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")
	t.Setenv("SAM_PLATFORM_TOKEN", "tok-123")
	var gotAuth string
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "str_init"})
	}))
	t.Cleanup(srv.Close)

	if _, _, _, _, err := resolveToolBuildTarget(context.Background(), srv.URL); err != nil {
		t.Fatalf("resolveToolBuildTarget: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization header = %q; want %q", gotAuth, "Bearer tok-123")
	}
}

func TestResolveToolBuildTarget_UnauthenticatedFallsBackToAnonymous(t *testing.T) {
	emptyCredentialCache(t)
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")

	var gotAuth string
	var queried bool
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		queried = true
		writeV2Single(w, http.StatusOK, map[string]string{"os": "darwin", "arch": "arm64", "source": "str_init", "pythonVersion": "3.9"})
	}))
	t.Cleanup(srv.Close)

	osS, arch, pythonVer, source, err := resolveToolBuildTarget(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("resolveToolBuildTarget: %v (unauthenticated query must fall back to anonymous, not fail)", err)
	}
	if !queried {
		t.Fatal("platform was never queried")
	}
	if osS != "darwin" || arch != "arm64" || source != "str-init" {
		t.Errorf("got (%s,%s,%s); want (darwin,arm64,str-init)", osS, arch, source)
	}
	if pythonVer != "3.9" {
		t.Errorf("pythonVer = %q; want 3.9", pythonVer)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header = %q; want empty (anonymous fallback)", gotAuth)
	}
}

func TestResolveToolBuildTarget_SecuredPlatformHintsLogin(t *testing.T) {
	emptyCredentialCache(t)
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")

	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusUnauthorized, "unauthorized")
	}))
	t.Cleanup(srv.Close)

	_, _, _, _, err := resolveToolBuildTarget(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error on 401; got nil")
	}
	if !strings.Contains(err.Error(), "sam auth login") {
		t.Errorf("err = %v; want it to mention `sam auth login`", err)
	}
}

func TestResolveToolBuildTarget_ForbiddenAndOtherErrorsPassThrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
	}{
		{"forbidden", http.StatusForbidden},
		{"server error", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emptyCredentialCache(t)
			t.Setenv("SAM_TOOL_TARGET_OS", "")
			t.Setenv("SAM_TOOL_TARGET_ARCH", "")
			t.Setenv("SAM_TOOL_PYTHON_VERSION", "")

			srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
				writeV2Error(w, tc.code, "nope")
			}))
			t.Cleanup(srv.Close)

			_, _, _, _, err := resolveToolBuildTarget(context.Background(), srv.URL)
			if err == nil {
				t.Fatalf("expected an error on %d; got nil", tc.code)
			}
			if strings.Contains(err.Error(), "sam auth login") {
				t.Errorf("err = %v; %d must NOT be rewritten into a sam auth login hint", err, tc.code)
			}
		})
	}
}

func TestResolveToolBuildTarget_Platform(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")
	authedTestToken(t)
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "str_init", "pythonVersion": "3.11"})
	}))
	t.Cleanup(srv.Close)

	osS, arch, pythonVer, source, err := resolveToolBuildTarget(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("resolveToolBuildTarget: %v", err)
	}
	if osS != "linux" || arch != "arm64" || source != "str-init" {
		t.Errorf("got (%s,%s,%s); want (linux,arm64,str-init)", osS, arch, source)
	}
	if pythonVer != "3.11" {
		t.Errorf("pythonVer = %q; want 3.11 (from platform buildTarget)", pythonVer)
	}
}

func TestResolveToolBuildTarget_FallbackFails(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")
	authedTestToken(t)
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "fallback"})
	}))
	t.Cleanup(srv.Close)

	if _, _, _, _, err := resolveToolBuildTarget(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on source=fallback; got nil (would build against a guessed arch)")
	} else if _, ok := errors.AsType[*reconcile.UndiscoverableTargetError](err); !ok {
		t.Fatalf("err = %v; want *reconcile.UndiscoverableTargetError", err)
	}
}

func TestResolveToolBuildTarget_EnvOverridesPlatformAxis(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")
	authedTestToken(t)
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "str_init", "pythonVersion": "3.12"})
	}))
	t.Cleanup(srv.Close)

	osS, arch, pythonVer, source, err := resolveToolBuildTarget(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("resolveToolBuildTarget: %v", err)
	}
	if osS != "linux" || arch != "amd64" || source != targetSourceEnv {
		t.Errorf("got (%s,%s,%s); want (linux,amd64,%s)", osS, arch, source, targetSourceEnv)
	}
	if pythonVer != "3.12" {
		t.Errorf("pythonVer = %q; want 3.12 (platform value, env overrides only the arch axis)", pythonVer)
	}
}

func TestResolveToolBuildTarget_EnvPythonOverridesPlatform(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "3.11")
	authedTestToken(t)
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "str_init", "pythonVersion": "3.12"})
	}))
	t.Cleanup(srv.Close)

	osS, arch, pythonVer, source, err := resolveToolBuildTarget(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("resolveToolBuildTarget: %v", err)
	}
	if osS != "linux" || arch != "amd64" || source != targetSourceEnv {
		t.Errorf("got (%s,%s,%s); want (linux,amd64,%s)", osS, arch, source, targetSourceEnv)
	}
	if pythonVer != "3.11" {
		t.Errorf("pythonVer = %q; want 3.11 (SAM_TOOL_PYTHON_VERSION overrides platform's 3.12)", pythonVer)
	}
}

func TestWritePackagedBundle_PythonVersionShownForPythonToolsOnly(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "3.11")
	stubZip := func(_, _, _ string) ([]byte, error) { return []byte("zip"), nil }

	t.Run("python tool shows version", func(t *testing.T) {
		cmd, out := newPackageCmdForTest()
		err := writePackagedBundle(context.Background(), cmd, packageRequest{
			kind: "toolset", name: "pytool", output: filepath.Join(t.TempDir(), "p.zip"),
			uploadHint: "Toolsets → Upload bundle", pythonTool: true,
		}, stubZip)
		if err != nil {
			t.Fatalf("writePackagedBundle: %v", err)
		}
		if s := out.String(); !strings.Contains(s, "Python 3.11") {
			t.Errorf("expected Python version in output; got %q", s)
		}
	})

	t.Run("go tool omits version", func(t *testing.T) {
		cmd, out := newPackageCmdForTest()
		err := writePackagedBundle(context.Background(), cmd, packageRequest{
			kind: "toolset", name: "gotool", output: filepath.Join(t.TempDir(), "g.zip"),
			uploadHint: "Toolsets → Upload bundle", pythonTool: false,
		}, stubZip)
		if err != nil {
			t.Fatalf("writePackagedBundle: %v", err)
		}
		progress := strings.SplitN(out.String(), "\n", 2)[0]
		if strings.Contains(progress, "Python") {
			t.Errorf("did not expect Python in the progress line for a Go tool; got %q", progress)
		}
	})
}

func TestWritePackagedBundle_PythonToolUnknownVersionWarns(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")
	cmd, out := newPackageCmdForTest()
	err := writePackagedBundle(context.Background(), cmd, packageRequest{
		kind: "toolset", name: "pytool", output: filepath.Join(t.TempDir(), "p.zip"),
		uploadHint: "Toolsets → Upload bundle", pythonTool: true,
	}, func(_, _, _ string) ([]byte, error) { return []byte("zip"), nil })
	if err != nil {
		t.Fatalf("writePackagedBundle: %v", err)
	}
	if s := out.String(); !strings.Contains(s, "SAM_TOOL_PYTHON_VERSION") {
		t.Errorf("expected hint to set SAM_TOOL_PYTHON_VERSION; got %q", s)
	}
}

func TestToolsetUsesPython(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  []string
		expect bool
	}{
		{"pyproject", []string{"pyproject.toml"}, true},
		{"requirements", []string{"requirements.txt"}, true},
		{"go module", []string{"go.mod", "main.go"}, false},
		{"raw files", []string{"manifest.yaml"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
					t.Fatalf("write %s: %v", f, err)
				}
			}
			if got := toolsetUsesPython(dir); got != tc.expect {
				t.Errorf("toolsetUsesPython = %v; want %v", got, tc.expect)
			}
		})
	}
}

func TestRunToolsetPackage_KindNoneBundlesSource(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	repoRoot, _ := writeKindNoneToolset(t, "noop")
	outZip := filepath.Join(t.TempDir(), "noop.zip")

	cmd, out := newPackageCmdForTest()
	if err := runToolsetPackage(context.Background(), cmd, "noop", repoRoot, "", "", outZip); err != nil {
		t.Fatalf("runToolsetPackage: %v", err)
	}

	if _, err := os.Stat(outZip); err != nil {
		t.Fatalf("expected zip at %s: %v", outZip, err)
	}
	entries := zipEntries(t, outZip)
	if !entries["manifest.yaml"] || !entries["tool.txt"] {
		t.Errorf("zip missing source files; got %v", entries)
	}
	if s := out.String(); !strings.Contains(s, "Next step") || !strings.Contains(s, "linux/arm64") {
		t.Errorf("output missing target line / next step: %q", s)
	}
}

func TestRunToolsetPackage_DefaultOutputName(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	repoRoot, _ := writeKindNoneToolset(t, "noop")

	workDir := t.TempDir()
	t.Chdir(workDir)

	cmd, _ := newPackageCmdForTest()
	if err := runToolsetPackage(context.Background(), cmd, "noop", repoRoot, "", "", ""); err != nil {
		t.Fatalf("runToolsetPackage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workDir, "noop.zip")); err != nil {
		t.Errorf("expected noop.zip in cwd: %v", err)
	}
}

func TestToolsetPackageCmd_Execute(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	repoRoot, _ := writeKindNoneToolset(t, "noop")
	outZip := filepath.Join(t.TempDir(), "noop.zip")

	cmd := newToolsetPackageCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"noop", repoRoot, "-o", outZip})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(outZip); err != nil {
		t.Errorf("expected zip at %s: %v", outZip, err)
	}
}

func TestRunToolsetPackage_GoBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("requires go build")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	repoRoot := t.TempDir()
	src := filepath.Join(repoRoot, "toolsets", "greeter", "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	for rel, body := range map[string]string{
		"go.mod":  "module greeter\n\ngo 1.23\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(src, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	outZip := filepath.Join(t.TempDir(), "greeter.zip")

	cmd, _ := newPackageCmdForTest()
	if err := runToolsetPackage(context.Background(), cmd, "greeter", repoRoot, "", "", outZip); err != nil {
		t.Fatalf("runToolsetPackage: %v", err)
	}
	if !zipEntries(t, outZip)["greeter"] {
		t.Errorf("zip missing the compiled binary; got %v", zipEntries(t, outZip))
	}
}

func TestRunToolsetPackage_ScaffoldedGoToolsetBuildsAWindowsExecutable(t *testing.T) {
	if testing.Short() {
		t.Skip("requires go build")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "windows")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	repoRoot := t.TempDir()
	if _, err := toolinit.Init(toolinit.Options{Name: "greeter", Path: repoRoot, Lang: toolinit.LangGo, CLIVersion: "9.9.9"}); err != nil {
		t.Fatalf("toolinit.Init: %v", err)
	}
	outZip := filepath.Join(t.TempDir(), "greeter.zip")

	cmd, _ := newPackageCmdForTest()
	if err := runToolsetPackage(context.Background(), cmd, "greeter", repoRoot, "", "", outZip); err != nil {
		t.Fatalf("runToolsetPackage: %v", err)
	}
	if entries := zipEntries(t, outZip); !entries["greeter.exe"] {
		t.Errorf("zip missing greeter.exe; got %v", entries)
	}
}

func TestRunToolsetPackage_MissingSrcErrors(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	cmd, _ := newPackageCmdForTest()
	err := runToolsetPackage(context.Background(), cmd, "ghost", t.TempDir(), "", "", filepath.Join(t.TempDir(), "ghost.zip"))
	if err == nil || !strings.Contains(err.Error(), "toolset src dir not found") {
		t.Errorf("err = %v; want 'toolset src dir not found'", err)
	}
}
