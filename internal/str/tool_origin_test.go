// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestManifestOrigin_OnlyTheBuiltinRootIsOurs(t *testing.T) {
	root := t.TempDir()
	builtinDir := filepath.Join(root, "builtin-tools", "pptx")
	customerDir := filepath.Join(root, "toolsets", "acme")
	siblingDir := filepath.Join(root, "builtin-tools-evil", "x")
	for _, d := range []string{builtinDir, customerDir, siblingDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("create manifest dir %s: %v", d, err)
		}
	}
	sources := []string{
		writeManifest(t, builtinDir, "version: 1\ntools:\n  ours_tool:\n    executable: ours_tool\n"),
		writeManifest(t, customerDir, "version: 1\ntools:\n  their_tool:\n    executable: their_tool\n"),
		writeManifest(t, siblingDir, "version: 1\ntools:\n  sibling_tool:\n    executable: sibling_tool\n"),
	}

	m := NewManifestFromSources(sources, testLogger())
	m.SetBuiltinRoots([]string{filepath.Join(root, "builtin-tools")})
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("load manifests from three sources: %v", err)
	}

	for tool, want := range map[string]string{
		"ours_tool":    taxonomy.ToolCategoryBuiltin,
		"their_tool":   taxonomy.ToolCategoryCustom,
		"sibling_tool": taxonomy.ToolCategoryCustom,
	} {
		entry := m.GetTool(tool)
		if entry == nil {
			t.Fatalf("%s not loaded", tool)
		}
		if got := entry.toolCategory(); got != want {
			t.Errorf("%s category = %q, want %q", tool, got, want)
		}
	}
}

func TestIsolatedInvokeError_CustomToolLogsNoFailureText(t *testing.T) {
	const secret = "alice@example.com leaked in a pod message"
	for _, tc := range []struct {
		name       string
		ours       bool
		failure    *isolatedJobFailure
		err        error
		wantSecret bool
	}{
		{"custom pod lost", false, &isolatedJobFailure{Reason: "Evicted", Detail: secret, PodLost: true}, nil, false},
		{"custom startup failure", false, &isolatedJobFailure{Reason: "ImagePullBackOff", Detail: secret, Startup: true}, nil, false},
		{"custom unclassified failure", false, nil, fmt.Errorf("%s", secret), false},
		{"builtin pod lost", true, &isolatedJobFailure{Reason: "Evicted", Detail: secret, PodLost: true}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			err := tc.err
			if tc.failure != nil {
				err = tc.failure
			}

			isolatedInvokeError("t", 30, err, false, tc.ours, log)

			if got := strings.Contains(buf.String(), "alice@example.com"); got != tc.wantSecret {
				t.Errorf("log carries the failure text = %v, want %v: %s", got, tc.wantSecret, buf.String())
			}
			if buf.Len() == 0 {
				t.Error("nothing was logged for the failure")
			}
		})
	}
}

func TestFailureLogAttrs(t *testing.T) {
	err := fmt.Errorf("alice@example.com: %w", context.DeadlineExceeded)

	custom := fmt.Sprint(failureLogAttrs(false, err)...)
	if strings.Contains(custom, "alice") || !strings.Contains(custom, "deadline") {
		t.Errorf("custom attrs = %q, want the class and no text", custom)
	}
	if ours := fmt.Sprint(failureLogAttrs(true, err)...); !strings.Contains(ours, "alice") {
		t.Errorf("builtin attrs = %q, want the error", ours)
	}
}

func TestOutputArtifactFailureAttrs_NeverLogsNameOrErrorText(t *testing.T) {
	err := fmt.Errorf("open /out/alice@example.com.txt: %w", context.DeadlineExceeded)

	got := fmt.Sprint(outputArtifactFailureAttrs("alice@example.com.txt", err)...)

	if strings.Contains(got, "alice") || strings.Contains(got, "/out") {
		t.Errorf("attrs = %q, want no file name or error text", got)
	}
	for _, want := range []string{"errorClass", "deadline", "errorLen", "fileNameLen", "21"} {
		if !strings.Contains(got, want) {
			t.Errorf("attrs = %q, want %q", got, want)
		}
	}
}

func TestNewInvokeError_RecordsItsCallSite(t *testing.T) {
	got := newInvokeError("INTERNAL_ERROR", "boom %d", 1)

	if got.Message != "boom 1" {
		t.Errorf("message = %q, want boom 1", got.Message)
	}
	if !strings.HasPrefix(got.Site, "internal/str/tool_origin_test.go:") {
		t.Errorf("site = %q, want this test file", got.Site)
	}
}

func TestBuiltinRoots_CustomerDirsAreNeverBuiltin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       WorkerConfig
		wantRoots []string
	}{
		{
			name:      "separate directories keep both roots",
			cfg:       WorkerConfig{BuiltinToolsDir: "/opt/sam/builtin-tools", BuiltinSkillsDir: "/opt/sam/builtin-skills", ToolsetsDir: "/data/toolsets", SkillsDir: "/data/skills"},
			wantRoots: []string{"/opt/sam/builtin-tools", "/opt/sam/builtin-skills"},
		},
		{
			name:      "a customer toolsets dir inside a root removes that root",
			cfg:       WorkerConfig{BuiltinToolsDir: "/opt/sam", BuiltinSkillsDir: "/opt/sam/builtin-skills", ToolsetsDir: "/opt/sam/toolsets", SkillsDir: "/data/skills"},
			wantRoots: []string{"/opt/sam/builtin-skills"},
		},
		{
			name:      "a customer skills dir equal to the builtin skills dir removes it",
			cfg:       WorkerConfig{BuiltinToolsDir: "/opt/sam/builtin-tools", BuiltinSkillsDir: "/data/skills", SkillsDir: "/data/skills"},
			wantRoots: []string{"/opt/sam/builtin-tools"},
		},
		{
			name:      "no builtin tools dir leaves only the skills root",
			cfg:       WorkerConfig{BuiltinSkillsDir: "/opt/sam/builtin-skills", ToolsetsDir: "/data/toolsets"},
			wantRoots: []string{"/opt/sam/builtin-skills"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &Worker{cfg: tc.cfg}

			got := w.builtinRoots()

			if !slices.Equal(got, tc.wantRoots) {
				t.Errorf("roots = %v, want %v", got, tc.wantRoots)
			}
			m := &Manifest{}
			m.SetBuiltinRoots(got)
			for _, customer := range []string{
				filepath.Join(tc.cfg.ToolsetsDir, "pkg", "manifest.yaml"),
				filepath.Join(tc.cfg.SkillsDir, "skill", "tools", "manifest.yaml"),
				"/somewhere/else/manifest.yaml",
			} {
				if customer != "" && m.originOf(customer) != ToolOriginCustom {
					t.Errorf("%s classed builtin under roots %v", customer, got)
				}
			}
		})
	}
}

func TestSkillRegistry_BundledToolOriginFollowsTheSkillsRoot(t *testing.T) {
	builtinDir, customerDir := t.TempDir(), t.TempDir()
	for dir, skill := range map[string]string{builtinDir: "sam-ours", customerDir: "theirs"} {
		writeSkillMD(t, dir, skill, "---\nname: "+skill+"\ndescription: d\n---\nbody")
		toolsDir := filepath.Join(dir, skill, "tools")
		if err := os.MkdirAll(toolsDir, 0o755); err != nil {
			t.Fatalf("create %s: %v", toolsDir, err)
		}
		manifest := "version: 1\ntools:\n  run_" + skill + ":\n    executable: run\n    description: d\n"
		if err := os.WriteFile(filepath.Join(toolsDir, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
			t.Fatalf("write tool manifest for %s: %v", skill, err)
		}
	}

	reg := NewSkillRegistryWithBuiltins(customerDir, builtinDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load registry: %v", err)
	}

	entries := reg.AllBundledToolEntries()
	if got := entries["sam-ours"+SkillToolSep+"run_sam-ours"].toolCategory(); got != taxonomy.ToolCategoryBuiltin {
		t.Errorf("builtin skill script category = %q, want builtin", got)
	}
	if got := entries["theirs"+SkillToolSep+"run_theirs"].toolCategory(); got != taxonomy.ToolCategoryCustom {
		t.Errorf("customer skill script category = %q, want custom", got)
	}
}

type failingSaveService struct{ artifact.Service }

func (failingSaveService) Save(context.Context, artifact.Locator, []byte, string) (int, error) {
	return 0, fmt.Errorf("SECRET-SAVE-ERROR from the store")
}

func TestFinishResult_NoToolLogsOutputOrFilenames(t *testing.T) {
	const garbage = "SECRET-GARBAGE-OUTPUT"
	const oddName = "SECRET-odd-name.txt"
	for _, tc := range []struct {
		name       string
		ours       bool
		resultJSON string
	}{
		{"custom garbage result.json", false, garbage},
		{"builtin garbage result.json", true, garbage},
		{"custom odd output filename on a failed save", false, `{"result":{"status":"success"}}`},
		{"builtin odd output filename on a failed save", true, `{"result":{"status":"success"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			runner := NewSandboxRunner(SandboxConfig{}, log)
			dir := t.TempDir()
			outputDir := filepath.Join(dir, "output")
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				t.Fatalf("create output dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(outputDir, oddName), []byte("x"), 0o600); err != nil {
				t.Fatalf("write output artifact: %v", err)
			}
			resultFile := filepath.Join(dir, "result.json")
			if err := os.WriteFile(resultFile, []byte(tc.resultJSON), 0o600); err != nil {
				t.Fatalf("write result.json: %v", err)
			}
			entry := &ManifestEntry{ToolName: "probe"}
			if tc.ours {
				entry.Origin = ToolOriginBuiltin
			}

			runner.finishResult(t.Context(), toolCall{
				params:      &remotetool.InvokeParams{AppName: "app", UserID: "u", SessionID: "s"},
				entry:       entry,
				artifactSvc: &artifact.ScopedService{Inner: failingSaveService{}, Scope: "namespace", Namespace: "app"},
				log:         log,
			}, resultFile, outputDir, 1)

			out := buf.String()
			if out == "" {
				t.Fatal("nothing was logged for the failure")
			}
			leaked := strings.Contains(out, garbage) || strings.Contains(out, "SECRET-odd-name") || strings.Contains(out, "SECRET-SAVE-ERROR")
			if leaked {
				t.Errorf("log carries customer text: %s", out)
			}
		})
	}
}

func TestRunSetupScript_CustomDirPathStaysOutOfTheLog(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ours     bool
		wantPath bool
	}{
		{"customer directory", false, false},
		{"builtin directory", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "SECRET-customer-dir")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("create tool dir: %v", err)
			}
			script := []byte("echo hi\n")
			if err := os.WriteFile(filepath.Join(dir, setupScriptName), script, 0o600); err != nil {
				t.Fatalf("write setup.sh: %v", err)
			}
			marker := fmt.Sprintf("%x\n", sha256.Sum256(script))
			if err := os.WriteFile(filepath.Join(dir, setupMarkerName), []byte(marker), 0o600); err != nil {
				t.Fatalf("write setup marker: %v", err)
			}
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			if err := runSetupScript(t.Context(), nil, dir, tc.ours, log); err != nil {
				t.Fatalf("runSetupScript with a matching marker: %v", err)
			}

			if got := strings.Contains(buf.String(), "SECRET-customer-dir"); got != tc.wantPath {
				t.Errorf("log carries the directory path = %v, want %v: %s", got, tc.wantPath, buf.String())
			}
			if buf.Len() == 0 {
				t.Error("nothing was logged")
			}
		})
	}
}
