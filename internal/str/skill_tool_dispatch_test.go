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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestToolNameSeparatorsAgree(t *testing.T) {
	if SkillToolSep != PackageToolSep {
		t.Errorf("SkillToolSep %q != PackageToolSep %q", SkillToolSep, PackageToolSep)
	}

	tool := samtoolsdk.NewTool("echo", "Echo",
		func(context.Context, struct{}, *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			return &samtoolsdk.Result{}, nil
		})
	prefixed := "owner" + SkillToolSep + "echo"
	if _, err := samtoolsdk.InvokeInProcess(
		t.Context(), []*samtoolsdk.ToolDef{tool}, prefixed, nil, nil, samtoolsdk.InProcessBindings{},
	); err != nil {
		t.Errorf("SDK does not resolve %q built with SkillToolSep: %v", prefixed, err)
	}
}

func TestBareToolNameOf(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		want     string
	}{
		{"skill prefixed", "sam-test-harness__generate_test_plan", "generate_test_plan"},
		{"package prefixed", "mypkg__do_thing", "do_thing"},
		{"already bare", "generate_test_plan", ""},
		{"empty owner", "__generate_test_plan", ""},
		{"empty bare", "sam-test-harness__", ""},
		{"empty", "", ""},
		{"splits on first separator", "owner__a__b", "a__b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bareToolNameOf(tc.toolName); got != tc.want {
				t.Errorf("bareToolNameOf(%q) = %q, want %q", tc.toolName, got, tc.want)
			}
		})
	}
}

func TestBuildRunnerArgsDispatchName(t *testing.T) {
	runner := &SandboxRunner{cfg: SandboxConfig{}, logger: slog.Default()}
	params := &remotetool.InvokeParams{TaskID: "task-1", AppName: "app"}

	cases := []struct {
		name  string
		entry *ManifestEntry
		want  string
	}{
		{
			name:  "bare tool name dispatches as-is",
			entry: &ManifestEntry{ToolName: "generate_test_plan"},
			want:  "generate_test_plan",
		},
		{
			name: "prefixed tool name dispatches the recorded bare name",
			entry: &ManifestEntry{
				ToolName:     "sam-test-harness__generate_test_plan",
				BareToolName: "generate_test_plan",
			},
			want: "generate_test_plan",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := runner.buildRunnerArgs(params, tc.entry, nil, nil,
				"/tmp/status", "/tmp/result.json", "/tmp/out", "", nil)
			if args.ToolName != tc.want {
				t.Errorf("runner_args tool_name = %q, want %q", args.ToolName, tc.want)
			}
		})
	}
}

func TestRefreshToolSchemaRacesDispatch(t *testing.T) {
	dir := t.TempDir()
	const key = "myskill__probe"

	writeDispatchRecordingStub(t, filepath.Join(dir, "probe"), "probe", filepath.Join(dir, "unused.txt"))
	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n  " + key + ":\n    executable: " + filepath.Join(dir, "probe") + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	m := NewManifest(manifestPath, slog.Default())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := m.GetTool(key)
	if entry == nil {
		t.Fatalf("precondition: %q missing from the manifest", key)
	}

	runner := &SandboxRunner{cfg: SandboxConfig{}, logger: slog.Default()}
	runRefreshRace(t, m, key, func() {
		snapshot := m.SnapshotEntry(entry)
		runner.buildRunnerArgs(&remotetool.InvokeParams{}, &snapshot, nil, nil, "", "", "", "", nil)
	})
}

func TestWarnUnresolvedDispatchName(t *testing.T) {
	cases := []struct {
		name  string
		entry *ManifestEntry
		want  bool
	}{
		{
			name:  "prefixed and never confirmed",
			entry: &ManifestEntry{ToolName: "myskill__do_thing"},
			want:  true,
		},
		{
			name:  "binary reports its own prefixed name",
			entry: &ManifestEntry{ToolName: "myskill__do_thing", ToolNameObserved: true},
			want:  false,
		},
		{
			name:  "dispatch name resolved",
			entry: &ManifestEntry{ToolName: "myskill__do_thing", BareToolName: "do_thing"},
			want:  false,
		},
		{
			name:  "bare name carries no prefix",
			entry: &ManifestEntry{ToolName: "do_thing"},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			runner := &SandboxRunner{cfg: SandboxConfig{}, logger: logger}

			runner.warnUnresolvedDispatchName(tc.entry, logger)
			got := strings.Contains(buf.String(), "owner-prefixed tool name that --schema never confirmed")
			if got != tc.want {
				t.Errorf("warned = %v, want %v (log: %q)", got, tc.want, buf.String())
			}
			if !tc.want {
				return
			}

			before := strings.Count(buf.String(), "owner-prefixed")
			for range 5 {
				runner.warnUnresolvedDispatchName(tc.entry, logger)
			}
			if after := strings.Count(buf.String(), "owner-prefixed"); after != before {
				t.Errorf("warning repeated: %d lines after 5 more dispatches, want %d", after, before)
			}
		})
	}
}

func TestLoadRecordsBareNameWhenSchemaProbeFails(t *testing.T) {
	dir := t.TempDir()

	exe := filepath.Join(dir, "brokentool")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake tool: %v", err)
	}

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\n" +
		"tools:\n" +
		"  sam-test-harness__generate_test_plan:\n" +
		"    executable: " + exe + "\n" +
		"    parameters:\n" +
		"      type: object\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	m := NewManifest(manifestPath, slog.Default())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entry := m.GetTool("sam-test-harness__generate_test_plan")
	if entry == nil {
		t.Fatal("entry missing from manifest")
	}
	if unservable, reason := m.UnservableState(entry); unservable {
		t.Fatalf("entry should stay servable via manifest parameters, got unservable: %s", reason)
	}
	if entry.BareToolName != "generate_test_plan" {
		t.Errorf("BareToolName = %q, want %q", entry.BareToolName, "generate_test_plan")
	}

	runner := &SandboxRunner{cfg: SandboxConfig{}, logger: slog.Default()}
	args := runner.buildRunnerArgs(&remotetool.InvokeParams{}, entry, nil, nil,
		"/tmp/status", "/tmp/result.json", "/tmp/out", "", nil)
	if args.ToolName != "generate_test_plan" {
		t.Errorf("dispatched tool_name = %q, want %q", args.ToolName, "generate_test_plan")
	}
}
