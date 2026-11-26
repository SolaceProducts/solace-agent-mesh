// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestEffectConstants_AgreeWithSDK(t *testing.T) {
	t.Parallel()
	if string(samtoolsdk.EffectReadOnly) != remotetool.EffectReadOnly ||
		string(samtoolsdk.EffectSideEffecting) != remotetool.EffectSideEffecting {
		t.Fatalf("samtoolsdk effects (%q, %q) drifted from the wire (%q, %q)",
			samtoolsdk.EffectReadOnly, samtoolsdk.EffectSideEffecting,
			remotetool.EffectReadOnly, remotetool.EffectSideEffecting)
	}
}

func TestManifestLoad_PerToolEffect(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeManifest(t, dir, `version: 1
tools:
  reader:
    executable: reader
    description: "Reads"
    effect: read_only
  writer:
    executable: writer
    description: "Writes"
    effect: side_effecting
  silent:
    executable: silent
    description: "Declares nothing and carries a key this parser has never heard of"
    a_key_from_the_future: {nested: true}
`)
	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"reader": remotetool.EffectReadOnly,
		"writer": remotetool.EffectSideEffecting,
		"silent": "",
	} {
		entry := m.GetTool(name)
		if entry == nil {
			t.Fatalf("%s not loaded", name)
		}
		if entry.Effect != want {
			t.Errorf("%s effect = %q, want %q", name, entry.Effect, want)
		}
	}
}

func TestManifestLoad_InvalidEffectSkipsOnlyThatTool(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeManifest(t, dir, `version: 1
tools:
  typo:
    executable: typo
    description: "Misspelled effect"
    effect: readonly
  fine:
    executable: fine
    description: "Fine"
    effect: read_only
`)
	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.GetTool("typo") != nil {
		t.Error("a tool with an unrecognised effect was loaded; it must be refused like a bad volume mode")
	}
	if entry := m.GetTool("fine"); entry == nil || entry.Effect != remotetool.EffectReadOnly {
		t.Errorf("fine = %+v, want loaded with read_only", entry)
	}
}

func TestApplyDiscoveredSchema_EffectOnlyTightens(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, manifest, schema, want string
	}{
		{"schema tightens a declared read_only", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, remotetool.EffectSideEffecting},
		{"schema read_only cannot loosen a declared side_effecting", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, remotetool.EffectSideEffecting},
		{"silent schema keeps manifest", remotetool.EffectReadOnly, "", remotetool.EffectReadOnly},
		{"invalid schema value is ignored", remotetool.EffectSideEffecting, "maybe", remotetool.EffectSideEffecting},
		{"schema fills an undeclared manifest", "", remotetool.EffectReadOnly, remotetool.EffectReadOnly},
		{"schema read_only cannot rescue an invalid manifest value", "readonly", remotetool.EffectReadOnly, remotetool.EffectSideEffecting},
		{"a silent schema leaves an invalid manifest value strict", "side-effecting", "", remotetool.EffectSideEffecting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := &ManifestEntry{ToolName: "t", Effect: tc.manifest, DeclaredEffect: tc.manifest}
			applyDiscoveredSchema(entry, &DiscoveredTool{Description: "d", Effect: tc.schema})
			if entry.Effect != tc.want {
				t.Fatalf("effect = %q, want %q", entry.Effect, tc.want)
			}
		})
	}
}

func TestComputeFingerprint_ChangesWithEffect(t *testing.T) {
	t.Parallel()
	a := &ManifestEntry{ToolName: "t", Description: "d"}
	b := &ManifestEntry{ToolName: "t", Description: "d", Effect: remotetool.EffectReadOnly}
	if computeFingerprint(a) == computeFingerprint(b) {
		t.Fatal("declaring an effect did not change the fingerprint, so a reload would not republish it")
	}
}

func TestInitParams_CarryEffect(t *testing.T) {
	t.Parallel()
	w := &Worker{logger: slog.Default(), cfg: WorkerConfig{Sandbox: SandboxConfig{MaxTimeoutSeconds: 1800}}}

	static, err := w.resolveStaticToolSchema(&ManifestEntry{ToolName: "reader", Description: "d", Effect: remotetool.EffectReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	if static.Effect != remotetool.EffectReadOnly {
		t.Errorf("static init effect = %q, want read_only", static.Effect)
	}

	fromSchema, err := w.buildInitParamsFromDiscovered(
		&ManifestEntry{ToolName: "writer", Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly},
		map[string]DiscoveredTool{"writer": {Description: "d", Effect: remotetool.EffectSideEffecting}})
	if err != nil {
		t.Fatal(err)
	}
	if fromSchema.Effect != remotetool.EffectSideEffecting {
		t.Errorf("--schema init effect = %q, want the --schema declaration", fromSchema.Effect)
	}

	manifestOnly, err := w.buildInitParamsFromDiscovered(
		&ManifestEntry{ToolName: "reader", Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly},
		map[string]DiscoveredTool{"reader": {Description: "d"}})
	if err != nil {
		t.Fatal(err)
	}
	if manifestOnly.Effect != remotetool.EffectReadOnly {
		t.Errorf("init effect = %q, want the manifest declaration when --schema is silent", manifestOnly.Effect)
	}

	undeclared, err := w.resolveStaticToolSchema(&ManifestEntry{ToolName: "old", Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(undeclared)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"effect"`) {
		t.Errorf("an undeclared tool's init must omit effect so it looks like an older STR's: %s", data)
	}
}

func TestInProcessManifestEntry_CarriesSDKEffect(t *testing.T) {
	t.Parallel()
	td := samtoolsdk.NewTool("lookup", "Looks things up",
		func(_ context.Context, _ struct{}, _ *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			return nil, nil
		},
		samtoolsdk.WithEffect(samtoolsdk.EffectReadOnly))
	w := &Worker{logger: slog.Default()}
	entry, err := w.inProcessManifestEntry(td)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Effect != remotetool.EffectReadOnly {
		t.Fatalf("in-process entry effect = %q, want read_only", entry.Effect)
	}
}

func TestPackageManifestEffect_SurvivesTheAggregateManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exePath := filepath.Join(venvBinPath(t, dir), "lookup")
	writeSchemaStub(t, exePath,
		`{"tools":{"lookup":{"description":"looks up","parameters":{"type":"object","properties":{}}}}}`)
	writePackageManifestYAML(t, dir, `version: 1
tools:
  lookup:
    runtime: go
    executable: ./lookup
    effect: read_only
`)
	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), dir, "orders")
	if len(errs) > 0 || len(tools) != 1 {
		t.Fatalf("processPackage = %d tools, errors %v; want 1 tool", len(tools), errs)
	}
	if tools[0].effect != remotetool.EffectReadOnly {
		t.Fatalf("processed effect = %q, want read_only", tools[0].effect)
	}

	toolsDir := t.TempDir()
	agg := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	tool := tools[0]
	tool.toolDir = filepath.Join(toolsDir, "orders")
	if err := agg.writeMergedManifest([]processedTool{tool}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(toolsDir, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "effect: read_only") {
		t.Fatalf("aggregate manifest dropped effect:\n%s", data)
	}
	m := NewManifestFromSources([]string{filepath.Join(toolsDir, "manifest.yaml")}, captureLogger(new(bytes.Buffer)))
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	entry := m.GetTool("orders" + PackageToolSep + "lookup")
	if entry == nil || entry.Effect != remotetool.EffectReadOnly {
		t.Fatalf("round-tripped entry = %+v, want effect read_only", entry)
	}
}

func TestInitParams_CarryTheWorkersInstanceID(t *testing.T) {
	t.Parallel()
	w := &Worker{logger: slog.Default(), instanceID: "pod-7", cfg: WorkerConfig{Sandbox: SandboxConfig{MaxTimeoutSeconds: 1800}}}
	static, err := w.resolveStaticToolSchema(&ManifestEntry{ToolName: "lookup", Description: "d"})
	if err != nil {
		t.Fatalf("resolveStaticToolSchema: %v", err)
	}
	aware, err := w.buildInitParamsFromDiscovered(&ManifestEntry{ToolName: "lookup"}, map[string]DiscoveredTool{"lookup": {Description: "d"}})
	if err != nil {
		t.Fatalf("buildInitParamsFromDiscovered: %v", err)
	}
	for name, params := range map[string]*remotetool.InitParams{"static": static, "config-aware": aware} {
		if params.InstanceID != "pod-7" {
			t.Errorf("%s init carries instance %q, want pod-7", name, params.InstanceID)
		}
	}
}

func TestNewWorker_EachWorkerHasItsOwnInstanceID(t *testing.T) {
	t.Parallel()
	first, second := NewWorker(&testServices{logger: slog.Default()}, WorkerConfig{}), NewWorker(&testServices{logger: slog.Default()}, WorkerConfig{})
	if first.instanceID == "" || first.instanceID == second.instanceID {
		t.Fatalf("instance ids %q and %q, want two different non-empty ids", first.instanceID, second.instanceID)
	}
}
