// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestBuildPrlimitArgs_ProfileOnly(t *testing.T) {
	profile := Profiles["standard"]
	args := buildPrlimitArgs(profile, nil)

	if len(args) == 0 {
		t.Fatal("expected prlimit args for standard profile")
	}

	const mb = 1024 * 1024
	expectFlag(t, args, "--cpu=300")
	expectFlag(t, args, fmt.Sprintf("--fsize=%d", 256*mb))
	expectFlag(t, args, "--nofile=512:512")
	expectFlag(t, args, "--nproc=256:256")
	expectFlag(t, args, fmt.Sprintf("--stack=%d", 8*mb))

	for _, a := range args {
		if strings.HasPrefix(a, "--as=") {
			t.Errorf("buildPrlimitArgs must not emit --as, got %q", a)
		}
	}
}

func TestBuildPrlimitArgs_RestrictiveProfile(t *testing.T) {
	profile := Profiles["restrictive"]
	args := buildPrlimitArgs(profile, nil)

	expectFlag(t, args, "--cpu=60")
	expectFlag(t, args, "--nofile=128:128")
	expectFlag(t, args, "--nproc=64:64")
}

func TestBuildPrlimitArgs_PermissiveProfile(t *testing.T) {
	profile := Profiles["permissive"]
	args := buildPrlimitArgs(profile, nil)

	const mb = 1024 * 1024
	expectFlag(t, args, "--cpu=600")
	expectFlag(t, args, "--nproc=1024:1024")
	expectFlag(t, args, fmt.Sprintf("--stack=%d", 16*mb))
	for _, a := range args {
		if strings.HasPrefix(a, "--as=") {
			t.Errorf("buildPrlimitArgs must not emit --as, got %q", a)
		}
	}
}

func TestBuildPrlimitArgs_WithOverrides(t *testing.T) {
	profile := Profiles["standard"]
	overrides := &ResourceLimits{
		MaxCPUSeconds: 600,
	}

	args := buildPrlimitArgs(profile, overrides)

	const mb = 1024 * 1024
	expectFlag(t, args, "--cpu=600")
	expectFlag(t, args, fmt.Sprintf("--fsize=%d", 256*mb))
	expectFlag(t, args, "--nofile=512:512")
	expectFlag(t, args, "--nproc=256:256")
}

func TestBuildPrlimitArgs_OverrideAllFields(t *testing.T) {
	profile := Profiles["restrictive"]
	overrides := &ResourceLimits{
		MaxCPUSeconds:  900,
		MaxFileSizeMB:  512,
		MaxOpenFiles:   2048,
		MaxProcesses:   512,
		MaxStackSizeMB: 32,
	}

	args := buildPrlimitArgs(profile, overrides)

	const mb = 1024 * 1024
	expectFlag(t, args, "--cpu=900")
	expectFlag(t, args, fmt.Sprintf("--fsize=%d", 512*mb))
	expectFlag(t, args, "--nofile=2048:2048")
	expectFlag(t, args, "--nproc=512:512")
	expectFlag(t, args, fmt.Sprintf("--stack=%d", 32*mb))
}

func TestBuildPrlimitArgs_ZeroProfile(t *testing.T) {
	args := buildPrlimitArgs(SandboxProfile{}, nil)
	if args != nil {
		t.Errorf("expected nil for zero profile, got %v", args)
	}
}

func TestBuildPrlimitArgs_PartialZero(t *testing.T) {
	profile := SandboxProfile{
		MaxCPUSeconds: 60,
	}
	args := buildPrlimitArgs(profile, nil)

	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d: %v", len(args), args)
	}
	expectFlag(t, args, "--cpu=60")
}

func TestWrapWithPrlimit_Active(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			BwrapBin:   "/usr/bin/bwrap",
			PrlimitBin: "/usr/bin/prlimit",
		},
		logger: slog.Default(),
	}

	profile := Profiles["standard"]
	bwrapArgs := []string{"--die-with-parent", "--ro-bind", "/", "/", "--", "python3", "args.json"}

	cmd := runner.wrapWithPrlimit(context.Background(), profile, nil, bwrapArgs)

	if cmd.Path != "/usr/bin/prlimit" && !strings.HasSuffix(cmd.Path, "prlimit") {
		t.Errorf("expected prlimit as command, got %q", cmd.Path)
	}

	args := cmd.Args
	separatorIdx := -1
	for i, a := range args {
		if a == "--" {
			separatorIdx = i
			break
		}
	}
	if separatorIdx < 0 {
		t.Fatal("no '--' separator found in prlimit command")
	}

	if args[separatorIdx+1] != "/usr/bin/bwrap" {
		t.Errorf("expected bwrap after separator, got %q", args[separatorIdx+1])
	}

	bwrapStart := separatorIdx + 2
	if args[bwrapStart] != "--die-with-parent" {
		t.Errorf("expected bwrap args after bwrap path, got %q", args[bwrapStart])
	}
}

func TestWrapWithPrlimit_ZeroProfile(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			BwrapBin:   "/usr/bin/bwrap",
			PrlimitBin: "/usr/bin/prlimit",
		},
		logger: slog.Default(),
	}

	bwrapArgs := []string{"--die-with-parent", "--", "python3", "args.json"}
	cmd := runner.wrapWithPrlimit(context.Background(), SandboxProfile{}, nil, bwrapArgs)

	if !strings.HasSuffix(cmd.Path, "bwrap") {
		t.Errorf("expected bwrap as command for zero profile, got %q", cmd.Path)
	}
}

func TestBuildBwrapCommand_WithPrlimit(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:       "bwrap",
			BwrapBin:   "/usr/bin/bwrap",
			PrlimitBin: "/usr/bin/prlimit",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:       "test-tool",
		SandboxProfile: "standard",
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}

	if !strings.HasSuffix(cmd.Args[0], "prlimit") {
		t.Errorf("expected prlimit as argv[0], got %q", cmd.Args[0])
	}

	foundBwrap := false
	for i, a := range cmd.Args {
		if a == "--" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "/usr/bin/bwrap" {
			foundBwrap = true
			break
		}
	}
	if !foundBwrap {
		t.Errorf("bwrap not found after '--' in args: %v", cmd.Args)
	}
}

func TestBuildBwrapCommand_WithPerToolOverrides(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:       "bwrap",
			BwrapBin:   "/usr/bin/bwrap",
			PrlimitBin: "/usr/bin/prlimit",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:       "heavy-tool",
		SandboxProfile: "restrictive",
		ResourceLimits: &ResourceLimits{
			MaxCPUSeconds: 900,
		},
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	expectFlag(t, args, "--cpu=900")

	expectFlag(t, args, "--nproc=64:64")

	for _, a := range args {
		if strings.HasPrefix(a, "--as=") {
			t.Errorf("buildBwrapCommand must not emit --as, got %q", a)
		}
	}
}

func TestBuildGoBwrapCommand_WithPrlimit(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:       "bwrap",
			BwrapBin:   "/usr/bin/bwrap",
			PrlimitBin: "/usr/bin/prlimit",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:       "go-tool",
		Executable:     "/usr/local/bin/go-tool",
		SandboxProfile: "standard",
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}

	if !strings.HasSuffix(cmd.Args[0], "prlimit") {
		t.Errorf("expected prlimit as argv[0], got %q", cmd.Args[0])
	}
}

func TestBuildDirectCommand_NoPrlimit(t *testing.T) {
	runner := &SandboxRunner{
		cfg: SandboxConfig{
			Mode: "direct",
		},
		logger: slog.Default(),
	}
	runner.cfg.setDefaults()

	entry := &ManifestEntry{
		ToolName:   "test_tool",
		Executable: "/usr/local/bin/test_tool",
	}
	cmd := runner.buildDirectCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", "")

	if strings.Contains(cmd.Args[0], "prlimit") {
		t.Errorf("direct mode should not use prlimit, got %q", cmd.Args[0])
	}
}

func TestParseResourceLimits_AllFields(t *testing.T) {
	cfg := map[string]any{
		"max_cpu_seconds":   600,
		"max_file_size_mb":  512,
		"max_open_files":    1024,
		"max_processes":     512,
		"max_stack_size_mb": 16,
	}

	rl := parseResourceLimits(cfg)
	if rl == nil {
		t.Fatal("expected non-nil ResourceLimits")
	}
	if rl.MaxCPUSeconds != 600 {
		t.Errorf("MaxCPUSeconds = %d, want 600", rl.MaxCPUSeconds)
	}
	if rl.MaxFileSizeMB != 512 {
		t.Errorf("MaxFileSizeMB = %d, want 512", rl.MaxFileSizeMB)
	}
	if rl.MaxOpenFiles != 1024 {
		t.Errorf("MaxOpenFiles = %d, want 1024", rl.MaxOpenFiles)
	}
	if rl.MaxProcesses != 512 {
		t.Errorf("MaxProcesses = %d, want 512", rl.MaxProcesses)
	}
	if rl.MaxStackSizeMB != 16 {
		t.Errorf("MaxStackSizeMB = %d, want 16", rl.MaxStackSizeMB)
	}
}

func TestParseResourceLimits_Partial(t *testing.T) {
	cfg := map[string]any{
		"max_cpu_seconds": 600,
	}

	rl := parseResourceLimits(cfg)
	if rl == nil {
		t.Fatal("expected non-nil ResourceLimits")
	}
	if rl.MaxCPUSeconds != 600 {
		t.Errorf("MaxCPUSeconds = %d, want 600", rl.MaxCPUSeconds)
	}
	if rl.MaxOpenFiles != 0 {
		t.Errorf("MaxOpenFiles = %d, want 0", rl.MaxOpenFiles)
	}
}

func TestParseResourceLimits_Empty(t *testing.T) {
	cfg := map[string]any{}
	rl := parseResourceLimits(cfg)
	if rl != nil {
		t.Errorf("expected nil for empty config, got %+v", rl)
	}
}

func TestParseResourceLimits_WrongTypes(t *testing.T) {
	cfg := map[string]any{
		"max_cpu_seconds": "not-a-number",
	}
	rl := parseResourceLimits(cfg)
	if rl != nil {
		t.Errorf("expected nil for wrong types, got %+v", rl)
	}
}

func TestParseResourceLimits_MaxMemoryMBIgnored(t *testing.T) {
	cfg := map[string]any{"max_memory_mb": 4096}
	rl := parseResourceLimits(cfg)
	if rl != nil {
		t.Errorf("max_memory_mb should be ignored, expected nil, got %+v", rl)
	}
}

func TestProfileFieldNames(t *testing.T) {
	p := Profiles["standard"]
	if p.MaxCPUSeconds != 300 {
		t.Errorf("MaxCPUSeconds = %d, want 300", p.MaxCPUSeconds)
	}
	if p.MaxFileSizeMB != 256 {
		t.Errorf("MaxFileSizeMB = %d, want 256", p.MaxFileSizeMB)
	}
	if p.MaxOpenFiles != 512 {
		t.Errorf("MaxOpenFiles = %d, want 512", p.MaxOpenFiles)
	}
	if p.MaxProcesses != 256 {
		t.Errorf("MaxProcesses = %d, want 256", p.MaxProcesses)
	}
	if p.MaxStackSizeMB != 8 {
		t.Errorf("MaxStackSizeMB = %d, want 8", p.MaxStackSizeMB)
	}
}

func expectFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, a := range args {
		if a == flag {
			return
		}
	}
	t.Errorf("expected flag %q in args: %v", flag, args)
}
