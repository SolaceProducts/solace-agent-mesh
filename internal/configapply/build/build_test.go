// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
)

func writeFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	full := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func writeExec(t *testing.T, repo, rel, body string) {
	t.Helper()
	writeFile(t, repo, rel, body)
	full := filepath.Join(repo, rel)
	if err := os.Chmod(full, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}

func TestResolveScriptHostMatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("build.sh is the host-other script on Windows; covered by TestResolveScriptOtherOnly")
	}
	dir := t.TempDir()
	writeExec(t, dir, "build.sh", "#!/bin/sh\necho hi\n")

	plan, err := Resolve("toolset", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Kind != KindScript {
		t.Fatalf("Kind=%v, want KindScript", plan.Kind)
	}
	if plan.ScriptPath == "" {
		t.Errorf("ScriptPath empty")
	}
}

func TestResolveScriptOtherOnly(t *testing.T) {
	dir := t.TempDir()

	otherName := "build.bat"
	if runtime.GOOS == "windows" {
		otherName = "build.sh"
	}
	writeFile(t, dir, otherName, "echo hi\n")

	_, err := Resolve("toolset", dir)
	if err == nil {
		t.Fatal("expected error for OS-mismatch script-only tree")
	}
	if !strings.Contains(err.Error(), "not buildable on") {
		t.Errorf("error should mention buildability: %v", err)
	}
}

func TestResolveGoConventionRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main(){}\n")

	plan, err := Resolve("mytool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Kind != KindGoConvention {
		t.Fatalf("Kind=%v, want KindGoConvention", plan.Kind)
	}
	if plan.GoEntry != "." {
		t.Errorf("GoEntry=%q, want .", plan.GoEntry)
	}
}

func TestResolveGoConventionCmdSubdir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "cmd/mytool/main.go", "package main\n\nfunc main(){}\n")

	plan, err := Resolve("mytool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.GoEntry != "./cmd/mytool" {
		t.Errorf("GoEntry=%q, want ./cmd/mytool", plan.GoEntry)
	}
}

func TestResolveGoAmbiguousCmd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "cmd/one/main.go", "package main\n\nfunc main(){}\n")
	writeFile(t, dir, "cmd/two/main.go", "package main\n\nfunc main(){}\n")

	_, err := Resolve("mytool", dir)
	if err == nil {
		t.Fatal("expected error for ambiguous cmd/")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should call out ambiguity: %v", err)
	}
}

func TestResolvePythonRequirements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "requests==2.31.0\n")
	writeFile(t, dir, "tool.py", "def hi(): return 1\n")

	plan, err := Resolve("pytool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Kind != KindPythonConvention {
		t.Fatalf("Kind=%v, want KindPythonConvention", plan.Kind)
	}
	if plan.PythonRequirements != "requirements.txt" {
		t.Errorf("PythonRequirements=%q, want requirements.txt", plan.PythonRequirements)
	}
}

func TestResolveNoneFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.yaml", "name: tool\n")

	plan, err := Resolve("tool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Kind != KindNone {
		t.Errorf("Kind=%v, want KindNone", plan.Kind)
	}
}

func TestHashStable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main(){}\n")

	plan, err := Resolve("tool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	target := Target{OS: "linux", Arch: "arm64"}
	h1, err := Hash(plan, target, dir)
	if err != nil {
		t.Fatalf("Hash 1: %v", err)
	}
	h2, err := Hash(plan, target, dir)
	if err != nil {
		t.Fatalf("Hash 2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash drift: %s vs %s", h1, h2)
	}
}

func TestHashChangesOnContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main(){}\n")

	plan, _ := Resolve("tool", dir)
	target := Target{OS: "linux", Arch: "arm64"}
	h1, _ := Hash(plan, target, dir)

	writeFile(t, dir, "main.go", "package main\n\nfunc main(){ println(1) }\n")
	h2, _ := Hash(plan, target, dir)
	if h1 == h2 {
		t.Error("hash unchanged after content edit")
	}
}

func TestHashIgnoresDist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\n\nfunc main(){}\n")

	plan, _ := Resolve("tool", dir)
	target := Target{OS: "linux", Arch: "arm64"}
	h1, _ := Hash(plan, target, dir)

	writeFile(t, dir, "dist/tool", "fake compiled output\n")
	h2, _ := Hash(plan, target, dir)
	if h1 != h2 {
		t.Error("hash changed after dist/ write — should be excluded")
	}
}

func TestHashChangesOnPlanKind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.py", "x=1\n")

	target := Target{OS: "linux", Arch: "arm64"}
	noneHash, err := Hash(Plan{Kind: KindNone, SrcDir: dir}, target, dir)
	if err != nil {
		t.Fatalf("none hash: %v", err)
	}
	goHash, err := Hash(Plan{Kind: KindGoConvention, SrcDir: dir}, target, dir)
	if err != nil {
		t.Fatalf("go hash: %v", err)
	}
	if noneHash == goHash {
		t.Error("hash should differ across plan kinds")
	}
}

func TestHashChangesOnTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\nfunc main(){}\n")

	plan, _ := Resolve("tool", dir)
	linux, _ := Hash(plan, Target{OS: "linux", Arch: "arm64"}, dir)
	darwin, _ := Hash(plan, Target{OS: "darwin", Arch: "arm64"}, dir)
	if linux == darwin {
		t.Error("hash should differ across targets")
	}
	linuxAMD64, _ := Hash(plan, Target{OS: "linux", Arch: "amd64"}, dir)
	if linux == linuxAMD64 {
		t.Error("hash should differ across archs")
	}
}

func TestRunScriptSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses /bin/sh; run only on Unix-like hosts")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nset -eu\nmkdir -p \"$SAM_TOOL_BUILD_OUT\"\nprintf 'SAM_TOOL_NAME=%s\\n' \"$SAM_TOOL_NAME\" > \"$SAM_TOOL_BUILD_OUT/token.txt\"\n"
	writeExec(t, dir, "build.sh", script)

	plan, err := Resolve("mytool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	dist, err := Run(context.Background(), plan, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dist, "token.txt"))
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if !strings.Contains(string(body), "SAM_TOOL_NAME=mytool") {
		t.Errorf("token body=%q, want SAM_TOOL_NAME=mytool", body)
	}
}

func TestRunScriptFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses /bin/sh; run only on Unix-like hosts")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'oops something went wrong'\nexit 17\n"
	writeExec(t, dir, "build.sh", script)

	plan, _ := Resolve("mytool", dir)
	_, err := Run(context.Background(), plan, RunOptions{})
	if err == nil {
		t.Fatal("expected error from non-zero script exit")
	}
	if !strings.Contains(err.Error(), "oops something went wrong") {
		t.Errorf("error should include script tail output: %v", err)
	}
}

func TestRunGoConvention(t *testing.T) {
	if _, err := lookGoBinary(); err != nil {
		t.Skip("go binary not available")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\n\nimport \"fmt\"\nfunc main(){ fmt.Println(\"ok\") }\n")

	plan, err := Resolve("mytool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	dist, err := Run(context.Background(), plan, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dist, "mytool")); err != nil {
		t.Errorf("expected mytool under dist: %v", err)
	}
}

func TestRunGoConventionCrossCompiles_LinuxArm64Default(t *testing.T) {
	if _, err := lookGoBinary(); err != nil {
		t.Skip("go binary not available")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\nfunc main(){}\n")

	plan, err := Resolve("mytool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	dist, err := Run(context.Background(), plan, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dist, "mytool"))
	if err != nil {
		t.Fatalf("read built binary: %v", err)
	}
	if len(body) < 4 || string(body[:4]) != "\x7fELF" {
		t.Errorf("built binary should be ELF; first 4 bytes = %x", body[:min(4, len(body))])
	}
}

func TestRunGoConventionSegmentsDistByTarget(t *testing.T) {
	if _, err := lookGoBinary(); err != nil {
		t.Skip("go binary not available")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\nfunc main(){}\n")

	plan, err := Resolve("mytool", dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	distLinux, err := Run(context.Background(), plan, RunOptions{TargetOS: "linux", TargetArch: "arm64"})
	if err != nil {
		t.Fatalf("Run linux/arm64: %v", err)
	}
	if want := filepath.Join(dir, "dist", "linux-arm64"); distLinux != want {
		t.Errorf("linux/arm64 dist = %s; want %s", distLinux, want)
	}

	distLinuxAMD64, err := Run(context.Background(), plan, RunOptions{TargetOS: "linux", TargetArch: "amd64"})
	if err != nil {
		t.Fatalf("Run linux/amd64: %v", err)
	}
	if want := filepath.Join(dir, "dist", "linux-amd64"); distLinuxAMD64 != want {
		t.Errorf("linux/amd64 dist = %s; want %s", distLinuxAMD64, want)
	}

	if _, err := os.Stat(filepath.Join(distLinux, "mytool")); err != nil {
		t.Errorf("linux/arm64 binary disappeared after linux/amd64 build: %v", err)
	}
}

func TestRunGoConventionRespectsTargetOverride(t *testing.T) {
	if _, err := lookGoBinary(); err != nil {
		t.Skip("go binary not available")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\nfunc main(){}\n")

	plan, _ := Resolve("mytool", dir)
	dist, err := Run(context.Background(), plan, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dist, "mytool"))
	if err != nil {
		t.Fatalf("read built binary: %v", err)
	}
	if len(body) < 20 || string(body[:4]) != "\x7fELF" {
		t.Fatalf("expected ELF binary, got %x", body[:min(4, len(body))])
	}
	machine := uint16(body[18]) | uint16(body[19])<<8
	const elfAMD64 = 0x3E
	if machine != elfAMD64 {
		t.Errorf("e_machine=0x%X; want 0x3E (AMD64)", machine)
	}
}

func TestAppendBuildEnv_IncludesTargetVars(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	plan := Plan{Name: "demo", SrcDir: "/tmp/demo"}
	env := appendBuildEnv(nil, plan, "/tmp/demo/dist", RunOptions{})

	want := map[string]string{
		"SAM_TOOL_NAME":        "demo",
		"SAM_TOOL_BUILD_OUT":   "/tmp/demo/dist",
		"SAM_TOOL_TARGET_OS":   "linux",
		"SAM_TOOL_TARGET_ARCH": "arm64",
	}
	got := envToMap(env)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env[%s] = %q; want %q", k, got[k], v)
		}
	}
}

func TestAppendBuildEnv_HonoursExportedOverride(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	plan := Plan{Name: "demo", SrcDir: "/tmp/demo"}
	env := appendBuildEnv(nil, plan, "/tmp/demo/dist", RunOptions{})
	got := envToMap(env)
	if got["SAM_TOOL_TARGET_ARCH"] != "amd64" {
		t.Errorf("SAM_TOOL_TARGET_ARCH = %q; want amd64", got["SAM_TOOL_TARGET_ARCH"])
	}
}

func TestAppendBuildEnv_PythonVersion(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_TOOL_PYTHON_VERSION", "")
	plan := Plan{Name: "demo", SrcDir: "/tmp/demo"}

	withVer := envToMap(appendBuildEnv(nil, plan, "/tmp/demo/dist", RunOptions{PythonVersion: "3.11"}))
	if withVer["SAM_TOOL_PYTHON_VERSION"] != "3.11" {
		t.Errorf("SAM_TOOL_PYTHON_VERSION = %q; want 3.11", withVer["SAM_TOOL_PYTHON_VERSION"])
	}

	noVer := envToMap(appendBuildEnv(nil, plan, "/tmp/demo/dist", RunOptions{}))
	if _, set := noVer["SAM_TOOL_PYTHON_VERSION"]; set {
		t.Errorf("SAM_TOOL_PYTHON_VERSION should be unset when RunOptions.PythonVersion is empty; got %q", noVer["SAM_TOOL_PYTHON_VERSION"])
	}
}

func TestAppendBuildEnv_ReproducibleGoFlags(t *testing.T) {
	if _, err := lookGoBinary(); err != nil {
		t.Skip("go binary not available")
	}
	base := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "GOFLAGS=") || strings.HasPrefix(kv, "GOENV=")
	})
	goEnvFile := filepath.Join(t.TempDir(), "go.env")
	if err := os.WriteFile(goEnvFile, []byte("GOFLAGS=-tags=persisted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Name: "demo", SrcDir: "/tmp/demo"}
	for _, tc := range []struct {
		name  string
		extra []string
		want  string
	}{
		{"defaults", []string{"GOENV=off"}, "-trimpath -buildvcs=false"},
		{"exported GOFLAGS kept", []string{"GOENV=off", "GOFLAGS=-mod=vendor"}, "-mod=vendor -trimpath -buildvcs=false"},
		{"go env -w GOFLAGS kept", []string{"GOENV=" + goEnvFile}, "-tags=persisted -trimpath -buildvcs=false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := append(slices.Clone(base), tc.extra...)
			if got := envToMap(appendBuildEnv(env, plan, "/tmp/demo/dist", RunOptions{}))["GOFLAGS"]; got != tc.want {
				t.Errorf("GOFLAGS = %q; want %q", got, tc.want)
			}
		})
	}
}

func envToMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

func lookGoBinary() (string, error) {
	return exec.LookPath("go")
}

func TestPipPlatformArgs_HostMatchReturnsNil(t *testing.T) {
	hostVer := hostPythonMinor()
	if hostVer == "" {
		t.Skip("python3 not available or version unrecognised")
	}
	args, err := pipPlatformArgs(RunOptions{
		TargetOS:      runtime.GOOS,
		TargetArch:    runtime.GOARCH,
		PythonVersion: hostVer,
	})
	if err != nil {
		t.Fatalf("pipPlatformArgs(host): unexpected error %v", err)
	}
	if args != nil {
		t.Errorf("pipPlatformArgs(host) = %v; want nil", args)
	}
}

func TestPipPlatformArgs_SameArchDifferentPythonVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pipPlatformArgs has no windows target; Python tool builds are unix-only")
	}
	hostVer := hostPythonMinor()
	if hostVer == "" {
		t.Skip("python3 not available or version unrecognised")
	}
	targetVer := "3.11"
	if hostVer == "3.11" {
		targetVer = "3.12"
	}
	args, err := pipPlatformArgs(RunOptions{
		TargetOS:      runtime.GOOS,
		TargetArch:    runtime.GOARCH,
		PythonVersion: targetVer,
	})
	if err != nil {
		t.Fatalf("pipPlatformArgs(same-arch, different python): unexpected error %v", err)
	}
	if args == nil {
		t.Fatal("pipPlatformArgs(same-arch, different python): want platform flags, got nil")
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--python-version "+targetVer) {
		t.Errorf("args %q missing --python-version %s", joined, targetVer)
	}
	if !strings.Contains(joined, "--only-binary=:all:") {
		t.Errorf("args %q missing --only-binary=:all:", joined)
	}
}

func TestPipPlatformArgs_KnownCrossTargets(t *testing.T) {
	cases := []struct {
		os, arch, wantPlatform string
	}{
		{"linux", "arm64", "manylinux2014_aarch64"},
		{"linux", "amd64", "manylinux2014_x86_64"},
		{"darwin", "arm64", "macosx_11_0_arm64"},
		{"darwin", "amd64", "macosx_10_9_x86_64"},
	}
	for _, c := range cases {
		if c.os == runtime.GOOS && c.arch == runtime.GOARCH {
			continue
		}
		args, err := pipPlatformArgs(RunOptions{TargetOS: c.os, TargetArch: c.arch})
		if err != nil {
			t.Errorf("%s/%s: unexpected error %v", c.os, c.arch, err)
			continue
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--platform "+c.wantPlatform) {
			t.Errorf("%s/%s: args %q missing platform %q", c.os, c.arch, joined, c.wantPlatform)
		}
		if !strings.Contains(joined, "--only-binary=:all:") {
			t.Errorf("%s/%s: args %q missing --only-binary=:all:", c.os, c.arch, joined)
		}
	}
}

func TestPipPlatformArgs_UnsupportedTargetErrors(t *testing.T) {
	_, err := pipPlatformArgs(RunOptions{TargetOS: "plan9", TargetArch: "mips"})
	if err == nil {
		t.Fatal("pipPlatformArgs(plan9/mips): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported target") {
		t.Errorf("error %q; want it to mention %q", err.Error(), "unsupported target")
	}
}

func TestVerifyNonEmptyDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := verifyNonEmptyDir(missing); err == nil {
		t.Error("verifyNonEmptyDir(missing): expected error, got nil")
	}

	empty := t.TempDir()
	if err := verifyNonEmptyDir(empty); err == nil {
		t.Error("verifyNonEmptyDir(empty): expected error, got nil")
	}

	populated := t.TempDir()
	writeFile(t, populated, "marker.txt", "x")
	if err := verifyNonEmptyDir(populated); err != nil {
		t.Errorf("verifyNonEmptyDir(populated): unexpected error %v", err)
	}
}

func TestRunPythonConvention_UnsupportedTargetErrorsBeforePip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"demo\"\nversion = \"0.1.0\"\n")

	plan, _ := Resolve("demo", dir)
	if plan.Kind != KindPythonConvention {
		t.Fatalf("Resolve kind = %v; want KindPythonConvention", plan.Kind)
	}
	_, err := Run(context.Background(), plan, RunOptions{TargetOS: "plan9", TargetArch: "mips"})
	if err == nil {
		t.Fatal("Run(unsupported target): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported target") {
		t.Errorf("error %q; want it to mention %q", err.Error(), "unsupported target")
	}
}

func TestRunPythonConvention_BelowMinPythonErrorsBeforePip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"demo\"\nversion = \"0.1.0\"\n")

	plan, _ := Resolve("demo", dir)
	if plan.Kind != KindPythonConvention {
		t.Fatalf("Resolve kind = %v; want KindPythonConvention", plan.Kind)
	}
	_, err := Run(context.Background(), plan, RunOptions{
		TargetOS:      runtime.GOOS,
		TargetArch:    runtime.GOARCH,
		PythonVersion: "3.9",
	})
	if err == nil {
		t.Fatal("Run(target Python 3.9): expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3.9") || !strings.Contains(msg, "3.10") {
		t.Errorf("error %q; want it to name both the 3.9 target and the 3.10 minimum", msg)
	}
}

func TestRunScriptPythonToolset_BelowMinPythonErrorsBeforeBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scaffold build.sh is the host script on unix hosts")
	}
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"demo\"\nversion = \"0.1.0\"\nrequires-python = \">=3.10\"\n")
	writeExec(t, dir, "build.sh", "#!/bin/sh\necho SENTINEL-build-sh-ran >&2\nexit 3\n")

	plan, _ := Resolve("demo", dir)
	if plan.Kind != KindScript {
		t.Fatalf("Resolve kind = %v; want KindScript (build.sh present)", plan.Kind)
	}
	_, err := Run(context.Background(), plan, RunOptions{
		TargetOS:      runtime.GOOS,
		TargetArch:    runtime.GOARCH,
		PythonVersion: "3.9",
	})
	if err == nil {
		t.Fatal("Run(script python toolset, target 3.9): expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3.9") || !strings.Contains(msg, "3.10") {
		t.Errorf("error %q; want the version-floor guard", msg)
	}
	if strings.Contains(msg, "SENTINEL-build-sh-ran") {
		t.Errorf("guard must fire BEFORE build.sh runs; got %q", msg)
	}
}

func TestIsPythonToolset(t *testing.T) {
	pyDir := t.TempDir()
	writeFile(t, pyDir, "pyproject.toml", "[project]\nname = \"d\"\nversion = \"0\"\n")
	reqDir := t.TempDir()
	writeFile(t, reqDir, "requirements.txt", "")
	goDir := t.TempDir()
	writeFile(t, goDir, "go.mod", "module d\n\ngo 1.26\n")

	cases := []struct {
		name string
		plan Plan
		want bool
	}{
		{"convention pyproject", Plan{Kind: KindPythonConvention, SrcDir: pyDir}, true},
		{"script + pyproject", Plan{Kind: KindScript, SrcDir: pyDir}, true},
		{"script + requirements", Plan{Kind: KindScript, SrcDir: reqDir}, true},
		{"script + go.mod only", Plan{Kind: KindScript, SrcDir: goDir}, false},
		{"go convention", Plan{Kind: KindGoConvention, SrcDir: goDir}, false},
		{"none", Plan{Kind: KindNone, SrcDir: pyDir}, false},
	}
	for _, tc := range cases {
		if got := isPythonToolset(tc.plan); got != tc.want {
			t.Errorf("%s: isPythonToolset = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRunGoConvention_BelowMinPythonNotBlocked(t *testing.T) {
	if _, err := lookGoBinary(); err != nil {
		t.Skip("go binary not available")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module demo\n\ngo 1.26\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	plan, _ := Resolve("demo", dir)
	if plan.Kind != KindGoConvention {
		t.Fatalf("Resolve kind = %v; want KindGoConvention", plan.Kind)
	}
	_, err := Run(context.Background(), plan, RunOptions{
		TargetOS:      runtime.GOOS,
		TargetArch:    runtime.GOARCH,
		PythonVersion: "3.9",
	})
	if err != nil && strings.Contains(err.Error(), "require Python >=") {
		t.Errorf("Go toolset wrongly blocked by the Python floor: %v", err)
	}
}

func TestCheckToolPythonVersionComparesMajorMinorNumerically(t *testing.T) {
	cases := []struct {
		version string
		wantErr bool
	}{
		{"3.9", true},
		{"3.9.6", true},
		{"3.8", true},
		{"2.7", true},
		{"3.10", false},
		{"3.10.6", false},
		{"3.11", false},
		{"3.14", false},
		{"4.0", false},
		{"", false},
		{"nonsense", false},
		{"3", false},
	}
	for _, tc := range cases {
		err := checkToolPythonVersion(tc.version)
		if (err != nil) != tc.wantErr {
			t.Errorf("checkToolPythonVersion(%q): err=%v, wantErr=%v", tc.version, err, tc.wantErr)
			continue
		}
		if tc.wantErr && !strings.Contains(err.Error(), MinToolPythonVersion) {
			t.Errorf("checkToolPythonVersion(%q): error %q should name the floor %q", tc.version, err.Error(), MinToolPythonVersion)
		}
	}
}

func TestRunPythonConvention_EmptyOutputErrors(t *testing.T) {
	if _, err := exec.LookPath("pip"); err != nil {
		t.Skip("pip not available")
	}
	hostVer := hostPythonMinor()
	if hostVer == "" {
		t.Skip("python3 not available or version unrecognised")
	}
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "")

	plan, _ := Resolve("demo", dir)
	if plan.Kind != KindPythonConvention {
		t.Fatalf("Resolve kind = %v; want KindPythonConvention", plan.Kind)
	}
	_, err := Run(context.Background(), plan, RunOptions{
		TargetOS:      runtime.GOOS,
		TargetArch:    runtime.GOARCH,
		PythonVersion: hostVer,
	})
	if err == nil {
		t.Fatal("Run(empty requirements): expected 'produced no output' error, got nil")
	}
	if !strings.Contains(err.Error(), "produced no output") {
		t.Errorf("error %q; want it to mention %q", err.Error(), "produced no output")
	}
}

func TestPyprojectScriptNames(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file returns empty", func(t *testing.T) {
		names, err := pyprojectScriptNames(filepath.Join(dir, "nope.toml"))
		if err != nil {
			t.Fatalf("err = %v; want nil for missing file", err)
		}
		if len(names) != 0 {
			t.Errorf("names = %v; want empty", names)
		}
	})

	t.Run("extracts scripts and stops at next table", func(t *testing.T) {
		writeFile(t, dir, "pyproject.toml", `[project]
name = "demo"

[project.scripts]
greet = "demo:cli"
report = 'demo.report:main'

[tool.hatch.build.targets.wheel]
packages = ["src/demo"]
`)
		names, err := pyprojectScriptNames(filepath.Join(dir, "pyproject.toml"))
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		want := []string{"greet", "report"}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("names = %v; want %v (must not include the [tool.hatch...] key)", names, want)
		}
	})

	t.Run("no scripts section returns empty", func(t *testing.T) {
		writeFile(t, dir, "noscripts.toml", "[project]\nname = \"demo\"\n")
		names, err := pyprojectScriptNames(filepath.Join(dir, "noscripts.toml"))
		if err != nil || len(names) != 0 {
			t.Errorf("names=%v err=%v; want empty,nil", names, err)
		}
	})
}

func TestVerifyPythonSchema_SkipsCrossCompile(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "dist/python/bin/greet", "#!/bin/sh\nexit 1\n")
	writeFile(t, dir, "pyproject.toml", "[project.scripts]\ngreet = \"demo:cli\"\n")

	otherOS := "linux"
	if runtime.GOOS == "linux" {
		otherOS = "darwin"
	}
	plan := Plan{Kind: KindPythonConvention, SrcDir: dir, PythonRequirements: "pyproject.toml"}
	err := verifyPythonSchema(context.Background(), plan, filepath.Join(dir, "dist"),
		RunOptions{TargetOS: otherOS, TargetArch: runtime.GOARCH})
	if err != nil {
		t.Fatalf("verifyPythonSchema cross-compile = %v; want nil (must skip, not run the script)", err)
	}
}

func TestVerifyPythonSchema_SkipsWithoutPyproject(t *testing.T) {
	dir := t.TempDir()
	plan := Plan{Kind: KindPythonConvention, SrcDir: dir, PythonRequirements: "requirements.txt"}
	err := verifyPythonSchema(context.Background(), plan, filepath.Join(dir, "dist"),
		RunOptions{TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH})
	if err != nil {
		t.Fatalf("verifyPythonSchema requirements.txt = %v; want nil (no console script to probe)", err)
	}
}

func TestVerifyPythonSchema_SkipsPythonVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "dist/python/bin/greet", "#!/bin/sh\nexit 1\n")
	writeFile(t, dir, "pyproject.toml", "[project.scripts]\ngreet = \"demo:cli\"\n")

	hostVer := hostPythonMinor()
	if hostVer == "" {
		t.Skip("python3 not available or version unrecognised")
	}
	targetVer := "3.11"
	if hostVer == "3.11" {
		targetVer = "3.12"
	}
	plan := Plan{Kind: KindPythonConvention, SrcDir: dir, PythonRequirements: "pyproject.toml"}
	err := verifyPythonSchema(context.Background(), plan, filepath.Join(dir, "dist"),
		RunOptions{TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH, PythonVersion: targetVer})
	if err != nil {
		t.Fatalf("verifyPythonSchema python-version-mismatch = %v; want nil (must skip, not run the script)", err)
	}
}

func TestVerifyPythonSchema_NativeSuccessAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh stub scripts")
	}
	hostVer := hostPythonMinor()
	if hostVer == "" {
		t.Skip("python3 not available or version unrecognised")
	}
	nativeOpts := RunOptions{TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH, PythonVersion: hostVer}

	t.Run("schema exit 0 passes", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project.scripts]\ngreet = \"demo:cli\"\n")
		writeExec(t, dir, "dist/python/bin/greet", "#!/bin/sh\necho '{\"tools\":{}}'\nexit 0\n")
		plan := Plan{Kind: KindPythonConvention, SrcDir: dir, PythonRequirements: "pyproject.toml"}
		if err := verifyPythonSchema(context.Background(), plan, filepath.Join(dir, "dist"), nativeOpts); err != nil {
			t.Fatalf("verifyPythonSchema = %v; want nil", err)
		}
	})

	t.Run("schema exit 1 fails with tool name and stderr", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project.scripts]\ngreet = \"demo:cli\"\n")
		writeExec(t, dir, "dist/python/bin/greet",
			"#!/bin/sh\necho 'ModuleNotFoundError: No module named demo' >&2\nexit 1\n")
		plan := Plan{Kind: KindPythonConvention, SrcDir: dir, PythonRequirements: "pyproject.toml"}
		err := verifyPythonSchema(context.Background(), plan, filepath.Join(dir, "dist"), nativeOpts)
		if err == nil {
			t.Fatal("verifyPythonSchema: expected error for a script that exits 1, got nil")
		}
		if !strings.Contains(err.Error(), "greet") || !strings.Contains(err.Error(), "not discoverable") {
			t.Errorf("error %q; want it to name the tool and 'not discoverable'", err.Error())
		}
		if !strings.Contains(err.Error(), "ModuleNotFoundError") {
			t.Errorf("error %q; want it to include the captured stderr", err.Error())
		}
	})

	t.Run("all declared scripts are probed, not just the first", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml",
			"[project.scripts]\ngreet = \"demo:cli\"\nreport = \"demo:report\"\n")
		writeExec(t, dir, "dist/python/bin/greet", "#!/bin/sh\nexit 0\n")
		writeExec(t, dir, "dist/python/bin/report", "#!/bin/sh\necho 'boom' >&2\nexit 1\n")
		plan := Plan{Kind: KindPythonConvention, SrcDir: dir, PythonRequirements: "pyproject.toml"}
		err := verifyPythonSchema(context.Background(), plan, filepath.Join(dir, "dist"), nativeOpts)
		if err == nil {
			t.Fatal("expected the second (broken) script to fail the check; got nil — loop may stop at first")
		}
		if !strings.Contains(err.Error(), "report") {
			t.Errorf("error %q; want it to name the failing second script %q", err.Error(), "report")
		}
	})

	t.Run("declared script missing from python/bin errors clearly", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", "[project.scripts]\ngreet = \"demo:cli\"\n")
		writeFile(t, dir, "dist/python/placeholder", "x")
		plan := Plan{Kind: KindPythonConvention, SrcDir: dir, PythonRequirements: "pyproject.toml"}
		err := verifyPythonSchema(context.Background(), plan, filepath.Join(dir, "dist"), nativeOpts)
		if err == nil {
			t.Fatal("expected an error when the declared console script is absent, got nil")
		}
		if !strings.Contains(err.Error(), "greet") || !strings.Contains(err.Error(), "not discoverable") {
			t.Errorf("error %q; want it to name the tool and 'not discoverable'", err.Error())
		}
	})
}

func TestLastLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"fewer than n returns all", "a\nb\nc", 20, "a\nb\nc"},
		{"more than n keeps last n", "1\n2\n3\n4\n5", 2, "4\n5"},
		{"exactly n", "a\nb\nc", 3, "a\nb\nc"},
		{"trailing newline trimmed", "a\nb\n", 20, "a\nb"},
		{"empty", "", 20, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastLines([]byte(tc.in), tc.n); got != tc.want {
				t.Errorf("lastLines(%q, %d) = %q; want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func bundleHash(t *testing.T, dist string) string {
	t.Helper()
	_, hash, err := bundle.BundleDir(dist)
	if err != nil {
		t.Fatalf("BundleDir: %v", err)
	}
	return hash
}

func TestScrubPythonBuildPaths_IdenticalAcrossCheckouts(t *testing.T) {
	const body = "import sys\nfrom demo import cli\nsys.exit(cli())\n"
	const shell = "#!/bin/sh\necho not python\n"
	layer := func(checkout, shebang string) string {
		dist := t.TempDir()
		writeExec(t, dist, "python/bin/demo", shebang+body)
		writeExec(t, dist, "python/bin/helper.sh", shell)
		writeFile(t, dist, "python/demo/__init__.py", "def cli(): return 0\n")
		directURL := `{"dir_info": {}, "url": "file://` + checkout + `/toolsets/demo/src"}`
		writeFile(t, dist, "python/demo-0.1.0.dist-info/direct_url.json", directURL)
		writeFile(t, dist, "python/demo-0.1.0.dist-info/RECORD",
			"../../bin/demo,"+recordHash([]byte(shebang+body))+",99\r\n"+
				"demo-0.1.0.dist-info/RECORD,,\r\n"+
				"demo-0.1.0.dist-info/direct_url.json,"+recordHash([]byte(directURL))+",246\r\n"+
				"demo/__init__.py,sha256=abc,20\r\n")
		return dist
	}
	longVenv := "/ci/workspace/" + strings.Repeat("x", 130) + "/.venv/bin/python3"
	a := layer("/ci/run-1", "#!/ci/run-1/.venv/bin/python3.12\n")
	b := layer("/ci/run-2", "#!/bin/sh\n'''exec' "+longVenv+` "$0" "$@"`+"\n' '''\n")

	for _, dist := range []string{a, b} {
		if err := scrubPythonBuildPaths(dist); err != nil {
			t.Fatalf("scrubPythonBuildPaths: %v", err)
		}
	}
	if ha, hb := bundleHash(t, a), bundleHash(t, b); ha != hb {
		t.Fatalf("bundle hashes differ after scrub: %s vs %s", ha, hb)
	}

	script, err := os.ReadFile(filepath.Join(a, "python", "bin", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/usr/bin/env python3\n" + body; string(script) != want {
		t.Errorf("script = %q; want %q", script, want)
	}
	if got, readErr := os.ReadFile(filepath.Join(a, "python", "bin", "helper.sh")); readErr != nil || string(got) != shell {
		t.Errorf("non-Python script = %q, %v; want it untouched", got, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(a, "python", "demo-0.1.0.dist-info", "direct_url.json")); !os.IsNotExist(statErr) {
		t.Errorf("direct_url.json still present (stat err %v)", statErr)
	}
	record, err := os.ReadFile(filepath.Join(a, "python", "demo-0.1.0.dist-info", "RECORD"))
	if err != nil {
		t.Fatal(err)
	}
	wantRecord := "../../bin/demo," + recordHash(script) + "," + strconv.Itoa(len(script)) + "\r\n" +
		"demo-0.1.0.dist-info/RECORD,,\r\n" +
		"demo/__init__.py,sha256=abc,20\r\n"
	if string(record) != wantRecord {
		t.Errorf("RECORD =\n%s\nwant\n%s", record, wantRecord)
	}
}

func TestWithPortablePythonShebang(t *testing.T) {
	const rest = "import demo\n"
	for _, tc := range []struct {
		name, script string
		wantOK       bool
	}{
		{name: "absolute interpreter with flags", script: "#!/opt/py/bin/python3.12 -I\n" + rest, wantOK: true},
		{name: "env python", script: "#!/usr/bin/env python3\n" + rest, wantOK: true},
		{name: "env non-python", script: "#!/usr/bin/env node\n" + rest},
		{name: "no shebang", script: rest},
		{name: "empty shebang", script: "#!\n" + rest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := withPortablePythonShebang([]byte(tc.script))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v; want %v", ok, tc.wantOK)
			}
			if want := portablePythonShebang + rest; ok && string(got) != want {
				t.Errorf("script = %q; want %q", got, want)
			}
		})
	}
}

func TestScrubPythonBuildPaths_Layouts(t *testing.T) {
	const distInfo = "python/demo-0.1.0.dist-info/"
	const lfRecord = "demo/__init__.py,sha256=abc,20\n"
	for _, tc := range []struct {
		name       string
		files      map[string]string
		wantErr    bool
		wantRecord string
	}{
		{name: "python is a file", files: map[string]string{"python": ""}, wantErr: true},
		{name: "bin is a file", files: map[string]string{"python/bin": ""}, wantErr: true},
		{
			name: "malformed RECORD",
			files: map[string]string{
				distInfo + "direct_url.json": "{}",
				distInfo + "RECORD":          "\"unterminated\n",
			},
			wantErr: true,
		},
		{name: "no RECORD", files: map[string]string{distInfo + "direct_url.json": "{}"}},
		{
			name:       "nothing to scrub",
			files:      map[string]string{distInfo + "RECORD": lfRecord},
			wantRecord: lfRecord,
		},
		{
			name: "rewritten script not in RECORD",
			files: map[string]string{
				"python/bin/demo":     "#!/ci/.venv/bin/python3\n",
				"python/bin/sub/keep": "",
				distInfo + "RECORD":   lfRecord,
			},
			wantRecord: lfRecord,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			for rel, body := range tc.files {
				writeFile(t, dist, rel, body)
			}
			err := scrubPythonBuildPaths(dist)
			if (err != nil) != tc.wantErr {
				t.Fatalf("scrubPythonBuildPaths err = %v; wantErr %v", err, tc.wantErr)
			}
			if _, statErr := os.Stat(filepath.Join(dist, distInfo, "direct_url.json")); !tc.wantErr && !os.IsNotExist(statErr) {
				t.Errorf("direct_url.json still present (stat err %v)", statErr)
			}
			if tc.wantRecord == "" {
				return
			}
			if got, _ := os.ReadFile(filepath.Join(dist, distInfo, "RECORD")); string(got) != tc.wantRecord {
				t.Errorf("RECORD = %q; want it untouched %q", got, tc.wantRecord)
			}
		})
	}
}

func TestScrubPythonBuildPaths_NotADirectoryNamesPath(t *testing.T) {
	for _, tc := range []struct{ name, file string }{
		{name: "python is a file", file: "python"},
		{name: "bin is a file", file: "python/bin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			writeFile(t, dist, tc.file, "")
			err := scrubPythonBuildPaths(dist)
			if err == nil {
				t.Fatal("scrubPythonBuildPaths err = nil; want error")
			}
			want := filepath.Join(dist, filepath.FromSlash(tc.file)) + " is not a directory"
			if err.Error() != want {
				t.Errorf("err = %q; want %q", err, want)
			}
		})
	}
}

func TestRun_GoBundleIdenticalAcrossCheckouts(t *testing.T) {
	if _, err := lookGoBinary(); err != nil {
		t.Skip("go binary not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	const buildSh = "#!/bin/sh\nset -e\n" +
		`CGO_ENABLED=0 GOOS="$SAM_TOOL_TARGET_OS" GOARCH="$SAM_TOOL_TARGET_ARCH" go build -o "$SAM_TOOL_BUILD_OUT/$SAM_TOOL_NAME" .` + "\n"
	for _, tc := range []struct {
		name   string
		script bool
	}{{name: "convention"}, {name: "build.sh", script: true}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.script && runtime.GOOS == "windows" {
				t.Skip("build.sh is unix-only")
			}
			opts := RunOptions{TargetOS: "linux", TargetArch: "arm64"}
			var hashes []string
			for i := range 2 {
				dir := t.TempDir()
				writeFile(t, dir, "go.mod", "module example\n\ngo 1.22\n")
				writeFile(t, dir, "main.go", "package main\n\nimport \"fmt\"\nfunc main(){ fmt.Println(\"ok\") }\n")
				if tc.script {
					writeExec(t, dir, "build.sh", buildSh)
				}
				commitAll(t, dir, fmt.Sprintf("2026-01-0%dT00:00:00Z", i+1))
				plan, err := Resolve("mytool", dir)
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}
				dist, err := Run(t.Context(), plan, opts)
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				hashes = append(hashes, bundleHash(t, dist))
			}
			if hashes[0] != hashes[1] {
				t.Fatalf("Go bundle hashes differ across checkouts: %s vs %s", hashes[0], hashes[1])
			}
		})
	}
}

func commitAll(t *testing.T, dir, date string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

const demoBuildBackend = `import base64, hashlib, os, zipfile

FILES = {
    "demo/__init__.py": "def cli():\n    print('{\"tools\": {}}')\n",
    "demo-0.1.0.dist-info/METADATA": "Metadata-Version: 2.1\nName: demo\nVersion: 0.1.0\n",
    "demo-0.1.0.dist-info/WHEEL": "Wheel-Version: 1.0\nGenerator: test\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
    "demo-0.1.0.dist-info/entry_points.txt": "[console_scripts]\ndemo = demo:cli\n",
}


def build_wheel(wheel_directory, config_settings=None, metadata_directory=None):
    name = "demo-0.1.0-py3-none-any.whl"
    record = []
    with zipfile.ZipFile(os.path.join(wheel_directory, name), "w") as zf:
        for path, body in FILES.items():
            data = body.encode()
            zf.writestr(path, data)
            digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
            record.append(f"{path},sha256={digest},{len(data)}")
        record.append("demo-0.1.0.dist-info/RECORD,,")
        zf.writestr("demo-0.1.0.dist-info/RECORD", "\n".join(record) + "\n")
    return name
`

const demoPyproject = `[build-system]
requires = []
build-backend = "demo_backend"
backend-path = ["."]

[project]
name = "demo"
version = "0.1.0"

[project.scripts]
demo = "demo:cli"
`

func TestRun_PythonBundleIdenticalAcrossCheckouts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Python tool builds are unix-only")
	}
	hostVer := hostPythonMinor()
	if hostVer == "" {
		t.Skip("python3 not available or version unrecognised")
	}
	if err := exec.Command("python3", "-m", "pip", "--version").Run(); err != nil {
		t.Skip("pip not available")
	}
	t.Setenv("PIP_NO_INDEX", "1")
	t.Setenv("PIP_DISABLE_PIP_VERSION_CHECK", "1")
	opts := RunOptions{TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH, PythonVersion: hostVer}
	var hashes []string
	for range 2 {
		dir := t.TempDir()
		writeFile(t, dir, "pyproject.toml", demoPyproject)
		writeFile(t, dir, "demo_backend.py", demoBuildBackend)
		plan, err := Resolve("demo", dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		dist, err := Run(t.Context(), plan, opts)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		script, err := os.ReadFile(filepath.Join(dist, "python", "bin", "demo"))
		if err != nil {
			t.Fatalf("pip did not write the console script: %v", err)
		}
		if !strings.HasPrefix(string(script), "#!/usr/bin/env python3\n") {
			t.Errorf("console script shebang not normalized: %q", strings.SplitN(string(script), "\n", 2)[0])
		}
		distInfo := filepath.Join(dist, "python", "demo-0.1.0.dist-info")
		if _, statErr := os.Stat(filepath.Join(distInfo, "direct_url.json")); !os.IsNotExist(statErr) {
			t.Errorf("direct_url.json still present (stat err %v)", statErr)
		}
		record, err := os.ReadFile(filepath.Join(distInfo, "RECORD"))
		if err != nil {
			t.Fatal(err)
		}
		if want := "../../bin/demo," + recordHash(script) + ","; !strings.Contains(string(record), want) {
			t.Errorf("RECORD has no row %q matching the rewritten script:\n%s", want, record)
		}
		hashes = append(hashes, bundleHash(t, dist))
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("Python bundle hashes differ across checkouts: %s vs %s", hashes[0], hashes[1])
	}
}
