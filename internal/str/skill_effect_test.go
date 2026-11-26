// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"gopkg.in/yaml.v3"
)

func writeSkillToolsDir(t *testing.T, manifest string) (manifestPath, toolsDir string) {
	t.Helper()
	toolsDir = t.TempDir()
	manifestPath = filepath.Join(toolsDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write skill manifest %s: %v", manifestPath, err)
	}
	return manifestPath, toolsDir
}

func otherPlatformBinaryHeader() []byte {
	header := make([]byte, 64)
	if runtime.GOOS == "linux" {
		copy(header, []byte{0xcf, 0xfa, 0xed, 0xfe})
		return header
	}
	copy(header, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	return header
}

const (
	probeExitsNonZero  = "exit 1"
	probePrintsNonJSON = "not JSON"
	probeListsOthers   = "lists other tools"
	probeListsOneOther = "lists one other tool"
	probeMissing       = "missing executable"
	probeForeignBinary = "foreign binary"
	probeConfirmsNone  = "declares nothing"
	probeSideEffecting = "declares side_effecting"
)

func prepareProbe(t *testing.T, dir, kind string) {
	t.Helper()
	exe := filepath.Join(dir, "lookup")
	switch kind {
	case probeExitsNonZero:
		writeProbeScript(t, exe, "#!/bin/sh\nexit 1\n")
	case probePrintsNonJSON:
		writeProbeScript(t, exe, "#!/bin/sh\nprintf 'not json'\n")
	case probeListsOthers:
		writeEffectStub(t, exe, map[string]string{"alpha": "", "beta": ""})
	case probeListsOneOther:
		writeEffectStub(t, exe, map[string]string{"other": ""})
	case probeForeignBinary:
		if err := os.WriteFile(exe, otherPlatformBinaryHeader(), 0o755); err != nil {
			t.Fatalf("write foreign binary: %v", err)
		}
	case probeConfirmsNone:
		writeEffectStub(t, exe, map[string]string{"lookup": ""})
	case probeSideEffecting:
		writeEffectStub(t, exe, map[string]string{"lookup": remotetool.EffectSideEffecting})
	}
}

func TestSkillToolManifest_AReadOnlyDeclarationNeedsAProbeThatConfirmsIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ probe, want string }{
		{probeExitsNonZero, remotetool.EffectSideEffecting},
		{probePrintsNonJSON, remotetool.EffectSideEffecting},
		{probeListsOthers, remotetool.EffectSideEffecting},
		{probeListsOneOther, ""},
		{probeMissing, remotetool.EffectSideEffecting},
		{probeForeignBinary, remotetool.EffectSideEffecting},
		{probeConfirmsNone, remotetool.EffectReadOnly},
		{probeSideEffecting, remotetool.EffectSideEffecting},
	} {
		t.Run(tc.probe, func(t *testing.T) {
			t.Parallel()
			manifestPath, toolsDir := writeSkillToolsDir(t, "version: 1\ntools:\n  lookup:\n    executable: lookup\n    effect: read_only\n")
			prepareProbe(t, toolsDir, tc.probe)
			entries, _, err := parseSkillToolManifest(nil, manifestPath, toolsDir, "orders-kit")
			if err != nil {
				t.Fatalf("parseSkillToolManifest: %v", err)
			}
			entry := entries["lookup"]
			if entry == nil {
				t.Fatal("lookup not parsed")
			}
			if entry.Effect != tc.want {
				t.Fatalf("with %s, effect = %q, want %q", tc.probe, entry.Effect, tc.want)
			}
		})
	}
}

func TestDistrustUnverifiedEffect_WarnsWithTheToolAndAStableReason(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	entry := &ManifestEntry{ToolName: "lookup", Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly}
	distrustUnverifiedEffect(slog.New(slog.NewJSONHandler(&logged, nil)), entry, unverifiedSchemaFailed)
	record := recordWithMessage(logRecords(t, &logged), "tool effect unverified")
	if record == nil {
		t.Fatalf("no unverified-effect warning; log: %s", logged.String())
	}
	if record["level"] != "WARN" || record["tool"] != "lookup" || record["declaredEffect"] != "read_only" || record["effect"] != "side_effecting" || record["reason"] != "schema_probe_failed" {
		t.Errorf("warning fields = %v, want level WARN, tool lookup, declaredEffect read_only, effect side_effecting, reason schema_probe_failed", record)
	}
	if entry.Effect != remotetool.EffectSideEffecting || entry.DeclaredEffect != remotetool.EffectReadOnly {
		t.Errorf("entry = effect %q declared %q, want the effect downgraded and the declaration kept", entry.Effect, entry.DeclaredEffect)
	}

	undeclared := &ManifestEntry{ToolName: "plain", Effect: remotetool.EffectSideEffecting}
	logged.Reset()
	distrustUnverifiedEffect(slog.New(slog.NewJSONHandler(&logged, nil)), undeclared, unverifiedSchemaFailed)
	if logged.Len() != 0 {
		t.Errorf("an entry that declared nothing logged %s", logged.String())
	}
}

func TestGenerateBundledToolManifest_CarriesTheDeclaredEffect(t *testing.T) {
	t.Parallel()
	entries := map[string]*ManifestEntry{
		"sk__strict":   {ToolName: "sk__strict", Executable: "/bin/strict", Effect: remotetool.EffectSideEffecting, DeclaredEffect: remotetool.EffectSideEffecting},
		"sk__reader":   {ToolName: "sk__reader", Executable: "/bin/reader", Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly},
		"sk__demoted":  {ToolName: "sk__demoted", Executable: "/bin/demoted", Effect: remotetool.EffectSideEffecting, DeclaredEffect: remotetool.EffectReadOnly},
		"sk__silent":   {ToolName: "sk__silent", Executable: "/bin/silent", Effect: remotetool.EffectReadOnly},
		"sk__nothing":  {ToolName: "sk__nothing", Executable: "/bin/nothing"},
		"sk__injected": {ToolName: "sk__injected", Executable: "/bin/injected", Effect: remotetool.EffectReadOnly},
	}
	path := filepath.Join(t.TempDir(), "bundled.yaml")
	if err := generateBundledToolManifest(entries, path, testLogger()); err != nil {
		t.Fatalf("generateBundledToolManifest: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	var generated struct {
		Tools map[string]map[string]any `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &generated); err != nil {
		t.Fatalf("parse generated manifest: %v\n%s", err, data)
	}
	for name, want := range map[string]any{
		"sk__strict":  remotetool.EffectSideEffecting,
		"sk__reader":  remotetool.EffectReadOnly,
		"sk__demoted": remotetool.EffectReadOnly,
		"sk__silent":  nil,
		"sk__nothing": nil,
	} {
		if got := generated.Tools[name]["effect"]; got != want {
			t.Errorf("%s: generated effect = %v, want %v\n%s", name, got, want, strings.TrimSpace(string(data)))
		}
	}
}

func writeProbeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script %s: %v", path, err)
	}
}

func TestRegisterBundledSkillTools_AToolTheProbeDoesNotListIsNotReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "kit")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", toolDir, err)
	}
	exe := filepath.Join(toolDir, "lookup")
	writeEffectStub(t, exe, map[string]string{"other": ""})

	reg := NewSkillRegistry(dir)
	reg.mu.Lock()
	reg.entries["kit"] = &SkillEntry{
		Name: "kit",
		BundledToolManifest: map[string]*ManifestEntry{
			"lookup": {ToolName: "lookup", Executable: exe, ToolDir: toolDir, Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly},
		},
	}
	reg.mu.Unlock()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := &Worker{cfg: WorkerConfig{SkillsDir: dir, Namespace: "ns"}, logger: discard, manifest: NewManifest("", discard), skillRegistry: reg}

	w.registerBundledSkillTools(t.Context())
	entry := w.manifest.GetTool("kit__lookup")
	if entry == nil {
		t.Fatal("lookup was not registered")
	}
	if entry.Effect != remotetool.EffectSideEffecting {
		t.Fatalf("registered effect = %q, want side_effecting: the probe does not list the tool", entry.Effect)
	}
}
