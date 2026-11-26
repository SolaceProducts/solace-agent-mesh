// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func invariantTestRunner(t *testing.T, freshProc bool) *SandboxRunner {
	t.Helper()
	r := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:       "bwrap",
			BwrapBin:   "/usr/bin/bwrap",
			PrlimitBin: "/usr/bin/prlimit",
		},
		logger:    slog.Default(),
		freshProc: freshProc,
	}
	r.cfg.setDefaults()
	return r
}

func TestBuildBwrapCommand_FreshProcDropped(t *testing.T) {
	runner := invariantTestRunner(t, false)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	for i, a := range args {
		if a == "--proc" {
			t.Errorf("--proc must be dropped when freshProc=false; found at %d: %v", i, args)
		}
	}
	expectFlag(t, args, "--unshare-pid")
	if findFlagTriple(args, "--ro-bind", "/", "/") < 0 {
		t.Errorf("expected --ro-bind / / to remain (provides the ancestor /proc): %v", args)
	}
}

func TestBuildBwrapCommand_IsolationInvariants(t *testing.T) {
	forbiddenExact := []string{"--cap-add", "--uid", "--userns", "--userns2", "--disable-userns"}

	for _, freshProc := range []bool{true, false} {
		name := "freshProc=false"
		if freshProc {
			name = "freshProc=true"
		}
		t.Run(name, func(t *testing.T) {
			runner := invariantTestRunner(t, freshProc)
			entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}

			cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
			if err != nil {
				t.Fatalf("buildBwrapCommand: %v", err)
			}
			args := cmd.Args

			for _, a := range args {
				for _, f := range forbiddenExact {
					if a == f {
						t.Errorf("argv must never contain %q (isolation invariant): %v", f, args)
					}
				}
				if strings.HasPrefix(a, "--cap-add") {
					t.Errorf("argv must never add capabilities: %v", args)
				}
				if strings.Contains(a, "CAP_SYS_PTRACE") {
					t.Errorf("argv must never grant CAP_SYS_PTRACE: %v", args)
				}
			}
			expectFlag(t, args, "--unshare-pid")
		})
	}
}

func TestBuildBwrapCommand_MasksFederatedTokenDir(t *testing.T) {
	tokenDir := t.TempDir()
	tokenFile := filepath.Join(tokenDir, "azure-identity-token")
	if err := os.WriteFile(tokenFile, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(tokenDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(llm.AzureFederatedTokenFileEnv, tokenFile)

	runner := invariantTestRunner(t, true)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}
	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	args := cmd.Args

	mask := findFlagTriple(args, "--tmpfs", wantDir, "--remount-ro")
	if mask < 0 || mask+3 >= len(args) || args[mask+3] != wantDir {
		t.Fatalf("expected --tmpfs %s --remount-ro %s: %v", wantDir, wantDir, args)
	}
	for i := mask + 1; i < len(args); i++ {
		if args[i] == bwrapFlagBind || args[i] == bwrapFlagROBind {
			t.Errorf("bind at %d follows the token mask and could expose it again: %v", i, args)
		}
	}
}

func TestBuildBwrapCommand_NoFederatedTokenNoMask(t *testing.T) {
	t.Setenv(llm.AzureFederatedTokenFileEnv, "")

	runner := invariantTestRunner(t, true)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}
	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	for _, a := range cmd.Args {
		if a == "--remount-ro" {
			t.Errorf("no federated token is configured, so nothing should be masked: %v", cmd.Args)
		}
	}
}

func TestBuildBwrapCommand_UnresolvableFederatedTokenDirWarns(t *testing.T) {
	t.Setenv(llm.AzureFederatedTokenFileEnv, filepath.Join(t.TempDir(), "missing", "token"))
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	runner := invariantTestRunner(t, true)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}
	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", logger)
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	if slices.Contains(cmd.Args, "--remount-ro") {
		t.Errorf("an unresolvable directory cannot be masked: %v", cmd.Args)
	}
	if !strings.Contains(logs.String(), "federated token directory not masked") {
		t.Errorf("want a warning that the token directory is not masked, got logs %q", logs.String())
	}
}

func TestBuildBwrapCommand_FederatedTokenInRootIsRefused(t *testing.T) {
	t.Setenv(llm.AzureFederatedTokenFileEnv, "/token")

	runner := invariantTestRunner(t, true)
	entry := &ManifestEntry{ToolName: "test-tool", SandboxProfile: "standard"}
	_, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err == nil || !strings.Contains(err.Error(), llm.AzureFederatedTokenFileEnv) {
		t.Fatalf("error = %v, want one naming %s: masking / would hide the whole filesystem", err, llm.AzureFederatedTokenFileEnv)
	}
}
