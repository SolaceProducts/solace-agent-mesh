// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func writeEffectStub(t *testing.T, path string, effects map[string]string) {
	t.Helper()
	tools := make(map[string]any, len(effects))
	for name, effect := range effects {
		tool := map[string]any{"description": "d", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}
		if effect != "" {
			tool["effect"] = effect
		}
		tools[name] = tool
	}
	schema, err := json.Marshal(map[string]any{"tools": tools})
	if err != nil {
		t.Fatalf("marshal schema stub for %v: %v", effects, err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = \"--schema\" ]; then\n  printf '%s\\n' '" + string(schema) + "'\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write schema stub %s: %v", path, err)
	}
}

func loadSingleToolManifest(t *testing.T, declared string, schemaEffects map[string]string) (*Manifest, string) {
	t.Helper()
	return loadSingleToolManifestLogging(t, declared, schemaEffects, testLogger())
}

func loadSingleToolManifestLogging(t *testing.T, declared string, schemaEffects map[string]string, logger *slog.Logger) (*Manifest, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "lookup")
	writeEffectStub(t, exe, schemaEffects)
	manifest := "version: 1\ntools:\n  lookup:\n    executable: " + exe + "\n"
	if declared != "" {
		manifest += "    effect: " + declared + "\n"
	}
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest %s: %v", manifestPath, err)
	}
	m := NewManifest(manifestPath, logger)
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m, exe
}

func TestManifestLoad_AnEffectResolvesToTheStricterOfManifestAndSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, declared, schema, want string
	}{
		{"schema read_only cannot loosen a declared side_effecting", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, remotetool.EffectSideEffecting},
		{"schema side_effecting tightens a declared read_only", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, remotetool.EffectSideEffecting},
		{"agreeing read_only stays read_only", remotetool.EffectReadOnly, remotetool.EffectReadOnly, remotetool.EffectReadOnly},
		{"a silent schema keeps the manifest declaration", remotetool.EffectReadOnly, "", remotetool.EffectReadOnly},
		{"a schema fills an undeclared manifest", "", remotetool.EffectReadOnly, remotetool.EffectReadOnly},
		{"neither declares", "", "", ""},
		{"an unparsable schema effect cannot keep a declared read_only", remotetool.EffectReadOnly, "side-effecting", remotetool.EffectSideEffecting},
		{"an unparsable schema effect under a declared side_effecting", remotetool.EffectSideEffecting, "maybe", remotetool.EffectSideEffecting},
		{"an unparsable schema effect with nothing declared", "", "maybe", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, _ := loadSingleToolManifest(t, tc.declared, map[string]string{"lookup": tc.schema})
			entry := m.GetTool("lookup")
			if entry == nil {
				t.Fatal("lookup not loaded")
			}
			if entry.Effect != tc.want {
				t.Fatalf("manifest %q + schema %q: effect = %q, want %q", tc.declared, tc.schema, entry.Effect, tc.want)
			}
		})
	}
}

func TestManifestLoad_AnInvalidManifestEffectIsRefusedWhateverTheSchemaSays(t *testing.T) {
	t.Parallel()
	for _, declared := range []string{"readonly", "side-effecting"} {
		m, _ := loadSingleToolManifest(t, declared, map[string]string{"lookup": remotetool.EffectReadOnly})
		if entry := m.GetTool("lookup"); entry != nil {
			t.Errorf("manifest effect %q with a read_only schema loaded as %q; the tool must be refused", declared, entry.Effect)
		}
	}
}

func TestRefreshToolSchema_EffectFollowsTheLatestSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, declared, refreshed, want string
		wantChanged                     bool
	}{
		{"a dropped read_only is not kept", "", "", "", true},
		{"an invalid schema value does not keep read_only", "", "maybe", "", true},
		{"a dropped schema effect falls back to the declaration", remotetool.EffectReadOnly, "", remotetool.EffectReadOnly, false},
		{"a schema that turns side_effecting takes effect", "", remotetool.EffectSideEffecting, remotetool.EffectSideEffecting, true},
		{"a schema read_only cannot loosen a declared side_effecting", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, remotetool.EffectSideEffecting, false},
		{"an unparsable schema effect cannot keep a declared read_only", remotetool.EffectReadOnly, "maybe", remotetool.EffectSideEffecting, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			firstSchema := remotetool.EffectReadOnly
			if tc.declared == remotetool.EffectSideEffecting {
				firstSchema = ""
			}
			m, exe := loadSingleToolManifest(t, tc.declared, map[string]string{"lookup": firstSchema})
			before := m.GetTool("lookup").Effect
			writeEffectStub(t, exe, map[string]string{"lookup": tc.refreshed})
			refresh, err := m.RefreshToolSchema("lookup")
			if err != nil {
				t.Fatalf("RefreshToolSchema: %v", err)
			}
			if got := m.GetTool("lookup").Effect; got != tc.want {
				t.Fatalf("after refresh (was %q), effect = %q, want %q", before, got, tc.want)
			}
			if refresh.Changed != tc.wantChanged {
				t.Fatalf("refresh.Changed = %v, want %v (effect %q to %q)", refresh.Changed, tc.wantChanged, before, tc.want)
			}
		})
	}
}

func TestExpandMultiToolBinary_OnlySideEffectingIsInheritedFromTheManifestEntry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, declared string
		want           map[string]string
	}{
		{
			name:     "a declared read_only is not inherited by a sub-tool that declares nothing",
			declared: remotetool.EffectReadOnly,
			want:     map[string]string{"alpha": "", "beta": remotetool.EffectSideEffecting},
		},
		{
			name:     "a declared side_effecting is inherited by a sub-tool that declares nothing",
			declared: remotetool.EffectSideEffecting,
			want:     map[string]string{"alpha": remotetool.EffectSideEffecting, "beta": remotetool.EffectSideEffecting},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			exe := filepath.Join(dir, "multi")
			writeEffectStub(t, exe, map[string]string{"alpha": "", "beta": remotetool.EffectSideEffecting})
			entries := map[string]*ManifestEntry{
				"placeholder": {ToolName: "placeholder", Executable: exe, ToolDir: dir, Effect: tc.declared, DeclaredEffect: tc.declared},
			}
			discoverToolsInto(entries, nil, testLogger())
			for name, want := range tc.want {
				e, ok := entries[name]
				if !ok {
					t.Fatalf("expanded entry %q missing; got %v", name, entryKeysOf(entries))
				}
				if e.Effect != want {
					t.Errorf("manifest %q: %s effect = %q, want %q", tc.declared, name, e.Effect, want)
				}
			}
		})
	}
}

func TestInitParams_EffectIsTheStricterOfManifestAndConfigAwareSchema(t *testing.T) {
	t.Parallel()
	w := &Worker{logger: testLogger(), cfg: WorkerConfig{Sandbox: SandboxConfig{MaxTimeoutSeconds: 1800}}}
	for _, tc := range []struct {
		name, declared, stale, schema, want string
	}{
		{"schema read_only cannot loosen a declared side_effecting", remotetool.EffectSideEffecting, remotetool.EffectSideEffecting, remotetool.EffectReadOnly, remotetool.EffectSideEffecting},
		{"a silent schema falls back to the declaration", remotetool.EffectReadOnly, remotetool.EffectReadOnly, "", remotetool.EffectReadOnly},
		{"a silent schema does not keep an effect an earlier probe found", "", remotetool.EffectReadOnly, "", ""},
		{"an invalid schema value does not keep an effect an earlier probe found", "", remotetool.EffectReadOnly, "maybe", ""},
		{"a schema side_effecting tightens a declared read_only", remotetool.EffectReadOnly, remotetool.EffectReadOnly, remotetool.EffectSideEffecting, remotetool.EffectSideEffecting},
		{"an unparsable schema effect cannot keep a declared read_only", remotetool.EffectReadOnly, remotetool.EffectReadOnly, "maybe", remotetool.EffectSideEffecting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := &ManifestEntry{ToolName: "lookup", DeclaredEffect: tc.declared, Effect: tc.stale}
			got, err := w.buildInitParamsFromDiscovered(entry, map[string]DiscoveredTool{"lookup": {Description: "d", Effect: tc.schema}})
			if err != nil {
				t.Fatalf("buildInitParamsFromDiscovered: %v", err)
			}
			if got.Effect != tc.want {
				t.Fatalf("declared %q, schema %q: init effect = %q, want %q", tc.declared, tc.schema, got.Effect, tc.want)
			}
		})
	}
}

func TestInitParams_ASoleToolListedUnderAnotherNameDoesNotKeepTheDeclaration(t *testing.T) {
	t.Parallel()
	w := &Worker{logger: testLogger(), cfg: WorkerConfig{Sandbox: SandboxConfig{MaxTimeoutSeconds: 1800}}}
	for _, tc := range []struct {
		name, declared, otherDeclares, want string
	}{
		{"a read_only declared for lookup is not carried to other", remotetool.EffectReadOnly, "", ""},
		{"other's own read_only vouches for other", remotetool.EffectReadOnly, remotetool.EffectReadOnly, remotetool.EffectReadOnly},
		{"a declared side_effecting is carried, even over other's read_only", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, remotetool.EffectSideEffecting},
		{"other's side_effecting tightens a declared read_only", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, remotetool.EffectSideEffecting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := &ManifestEntry{ToolName: "lookup", DeclaredEffect: tc.declared, Effect: remotetool.EffectSideEffecting, effectDistrust: unverifiedSchemaFailed}
			got, err := w.buildInitParamsFromDiscovered(entry, map[string]DiscoveredTool{"other": {Description: "d", Effect: tc.otherDeclares}})
			if err != nil {
				t.Fatalf("buildInitParamsFromDiscovered with the binary listing only other: %v", err)
			}
			if got.ToolName != "other" || got.Effect != tc.want {
				t.Fatalf("declared %q, other declares %q: init for %q with effect %q, want other with effect %q", tc.declared, tc.otherDeclares, got.ToolName, got.Effect, tc.want)
			}
		})
	}
}

func TestComputeFingerprint_AnUndeclaredToolKeepsItsFingerprint(t *testing.T) {
	t.Parallel()
	entry := &ManifestEntry{ToolName: "t", Description: "d", Instructions: "i", Parameters: map[string]any{"type": "object"}}
	const beforeEffects = "eba3fcd8a12218df587c6d613c665038693ee5074f125d1daa7f215a58686be4"
	if got := computeFingerprint(entry); got != beforeEffects {
		t.Fatalf("computeFingerprint(undeclared) = %s, want %s, so an upgrade republishes nothing for tools that declare no effect", got, beforeEffects)
	}
}

func TestMergeDiscoveredSchemaIntoSkillEntry_EffectOnlyTightens(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, declared, schema, want string
	}{
		{"schema tightens a declared read_only", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, remotetool.EffectSideEffecting},
		{"schema read_only cannot loosen a declared side_effecting", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, remotetool.EffectSideEffecting},
		{"a silent schema keeps the declaration", remotetool.EffectReadOnly, "", remotetool.EffectReadOnly},
		{"a schema fills an undeclared entry", "", remotetool.EffectReadOnly, remotetool.EffectReadOnly},
		{"an unparsable schema effect cannot keep a declared read_only", remotetool.EffectReadOnly, "maybe", remotetool.EffectSideEffecting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := &ManifestEntry{ToolName: "skill__lookup", Effect: tc.declared, DeclaredEffect: tc.declared}
			mergeDiscoveredSchemaIntoSkillEntry(entry, DiscoveredTool{Description: "d", Effect: tc.schema})
			if entry.Effect != tc.want {
				t.Fatalf("declared %q, schema %q: effect = %q, want %q", tc.declared, tc.schema, entry.Effect, tc.want)
			}
		})
	}
}

func TestPackageManifestEffect_ASchemaReadOnlyCannotLoosenADeclaredSideEffecting(t *testing.T) {
	t.Parallel()
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "orders")
	exePath := filepath.Join(venvBinPath(t, pkgDir), "lookup")
	writeEffectStub(t, exePath, map[string]string{"lookup": remotetool.EffectReadOnly})
	writePackageManifestYAML(t, pkgDir, packageLookupManifestHead+"side_effecting\n")
	p := NewPackageProcessor(toolsDir, testLogger())
	tools, errs := p.processPackage(t.Context(), pkgDir, "orders")
	if len(errs) > 0 || len(tools) != 1 {
		t.Fatalf("processPackage = %d tools, errors %v; want 1 tool", len(tools), errs)
	}
	if err := p.writeMergedManifest(tools); err != nil {
		t.Fatalf("writeMergedManifest: %v", err)
	}
	m := NewManifestFromSources([]string{filepath.Join(toolsDir, "manifest.yaml")}, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := m.GetTool("orders" + PackageToolSep + "lookup")
	if entry == nil || entry.Effect != remotetool.EffectSideEffecting {
		t.Fatalf("round-tripped entry = %+v, want side_effecting: the package declares side_effecting but its --schema says read_only", entry)
	}
}

func TestDiscoverToolsInto_WarnsWhenManifestAndSchemaDisagreeOnEffect(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, declared, schema string
		wantWarn               bool
	}{
		{"a loosening schema", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, true},
		{"a tightening schema", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, true},
		{"agreement", remotetool.EffectReadOnly, remotetool.EffectReadOnly, false},
		{"a silent schema", remotetool.EffectReadOnly, "", false},
		{"an unparsable schema effect under read_only", remotetool.EffectReadOnly, "maybe", true},
		{"an unparsable schema effect with nothing declared", "", "maybe", true},
		{"an invalid manifest effect", "readonly", remotetool.EffectReadOnly, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			exe := filepath.Join(dir, "lookup")
			writeEffectStub(t, exe, map[string]string{"lookup": tc.schema})
			entries := map[string]*ManifestEntry{
				"lookup": {ToolName: "lookup", Executable: exe, ToolDir: dir, Effect: tc.declared, DeclaredEffect: tc.declared},
			}
			var logged bytes.Buffer
			discoverToolsInto(entries, nil, captureLogger(&logged))
			if got := bytes.Contains(logged.Bytes(), []byte("tool effect disagreement")); got != tc.wantWarn {
				t.Fatalf("manifest %q, schema %q: warned = %v, want %v; log: %s", tc.declared, tc.schema, got, tc.wantWarn, logged.String())
			}
		})
	}
}

func TestPackageProcessing_EffectIsTheStricterOfManifestAndSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, declared, schema, want string
		wantWarn                     bool
	}{
		{"a schema side_effecting tightens a declared read_only", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, remotetool.EffectSideEffecting, true},
		{"a schema read_only cannot loosen a declared side_effecting", remotetool.EffectSideEffecting, remotetool.EffectReadOnly, remotetool.EffectSideEffecting, true},
		{"agreement", remotetool.EffectReadOnly, remotetool.EffectReadOnly, remotetool.EffectReadOnly, false},
		{"a silent schema keeps the declaration", remotetool.EffectReadOnly, "", remotetool.EffectReadOnly, false},
		{"an unparsable schema effect cannot keep a declared read_only", remotetool.EffectReadOnly, "maybe", remotetool.EffectSideEffecting, true},
		{"a schema effect is not baked in as a declaration", "", remotetool.EffectReadOnly, "", false},
		{"an invalid manifest effect cannot be rescued by a schema read_only", "readonly", remotetool.EffectReadOnly, remotetool.EffectSideEffecting, true},
		{"an invalid manifest effect with a silent schema is side_effecting", "side-effecting", "", remotetool.EffectSideEffecting, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toolsDir := t.TempDir()
			pkgDir := filepath.Join(toolsDir, "orders")
			writeEffectStub(t, filepath.Join(venvBinPath(t, pkgDir), "lookup"), map[string]string{"lookup": tc.schema})
			manifest := "version: 1\ntools:\n  lookup:\n    runtime: go\n    executable: ./lookup\n"
			if tc.declared != "" {
				manifest += "    effect: " + tc.declared + "\n"
			}
			writePackageManifestYAML(t, pkgDir, manifest)
			var logged bytes.Buffer
			p := NewPackageProcessor(toolsDir, captureLogger(&logged))
			tools, errs := p.processPackage(t.Context(), pkgDir, "orders")
			if len(errs) > 0 || len(tools) != 1 {
				t.Fatalf("processPackage = %d tools, errors %v; want 1 tool", len(tools), errs)
			}
			if tools[0].effect != tc.want {
				t.Errorf("manifest %q, schema %q: processed effect = %q, want %q", tc.declared, tc.schema, tools[0].effect, tc.want)
			}
			if got := bytes.Contains(logged.Bytes(), []byte("tool effect disagreement")); got != tc.wantWarn {
				t.Errorf("manifest %q, schema %q: warned = %v, want %v; log: %s", tc.declared, tc.schema, got, tc.wantWarn, logged.String())
			}
		})
	}
}

func TestPackageProcessing_AnInvalidManifestEffectOnAToolTheSchemaDoesNotListIsSideEffecting(t *testing.T) {
	t.Parallel()
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "orders")
	writeEffectStub(t, filepath.Join(venvBinPath(t, pkgDir), "lookup"), map[string]string{"other": remotetool.EffectReadOnly})
	writePackageManifestYAML(t, pkgDir, packageLookupManifestHead+"readonly\n")
	p := NewPackageProcessor(toolsDir, testLogger())
	tools, errs := p.processPackage(t.Context(), pkgDir, "orders")
	var lookup *processedTool
	for i := range tools {
		if tools[i].name == "lookup" {
			lookup = &tools[i]
		}
	}
	if lookup == nil {
		t.Fatalf("processPackage = %d tools, errors %v; want the manifest-only lookup tool", len(tools), errs)
	}
	if lookup.effect != remotetool.EffectSideEffecting {
		t.Fatalf("manifest-only tool with effect %q: processed effect = %q, want side_effecting", "readonly", lookup.effect)
	}
}

func processedEffects(tools []processedTool) map[string]string {
	effects := make(map[string]string, len(tools))
	for _, tl := range tools {
		effects[tl.name] = tl.effect
	}
	return effects
}

func TestPackageProcessing_AManifestThatCannotBeDecodedMakesItsToolsSideEffecting(t *testing.T) {
	t.Parallel()
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "orders")
	writeEffectStub(t, filepath.Join(venvBinPath(t, pkgDir), "lookup"), map[string]string{
		"a": remotetool.EffectReadOnly, "b": remotetool.EffectReadOnly,
	})
	writePackageManifestYAML(t, pkgDir, `version: 1
tools:
  a:
    runtime: go
    executable: ./lookup
    effect: [read_only]
  b:
    runtime: go
    executable: ./lookup
    effect: side_effecting
`)
	var logged bytes.Buffer
	tools, errs := NewPackageProcessor(toolsDir, captureLogger(&logged)).processPackage(t.Context(), pkgDir, "orders")
	if len(tools) != 2 {
		t.Fatalf("processPackage = %d tools, errors %v; want a and b", len(tools), errs)
	}
	for name, effect := range processedEffects(tools) {
		if effect != remotetool.EffectSideEffecting {
			t.Errorf("tool %s: effect %q after its package manifest failed to decode, want side_effecting", name, effect)
		}
	}
	if !bytes.Contains(logged.Bytes(), []byte("package manifest could not be decoded")) {
		t.Errorf("the decode failure was not logged; log: %s", logged.String())
	}
}

func TestPackageProcessing_AMissingManifestIsNotAnError(t *testing.T) {
	t.Parallel()
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "orders")
	writeEffectStub(t, filepath.Join(venvBinPath(t, pkgDir), "lookup"), map[string]string{"a": remotetool.EffectReadOnly})
	var logged bytes.Buffer
	tools, _ := NewPackageProcessor(toolsDir, captureLogger(&logged)).processPackage(t.Context(), pkgDir, "orders")
	if len(tools) != 1 || tools[0].effect != "" {
		t.Fatalf("processPackage = %+v, want one schema-only tool with no declared effect", tools)
	}
	if bytes.Contains(logged.Bytes(), []byte("package manifest could not be decoded")) {
		t.Errorf("a package with no manifest logged a decode failure: %s", logged.String())
	}
}

const packageLookupManifestHead = "version: 1\ntools:\n  lookup:\n    runtime: go\n    executable: ./lookup\n    effect: "

func logRecords(t *testing.T, logged *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logged.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func recordWithMessage(records []map[string]any, prefix string) map[string]any {
	for _, record := range records {
		if msg, _ := record["msg"].(string); strings.HasPrefix(msg, prefix) {
			return record
		}
	}
	return nil
}

func TestEffectWarnsLogLengthsAndNeverTheRawValue(t *testing.T) {
	t.Parallel()
	const rawManifest, rawSchema = "Raw-Manifest-Effect", "Raw-Schema-Effect"

	t.Run("an invalid package manifest effect", func(t *testing.T) {
		t.Parallel()
		toolsDir := t.TempDir()
		pkgDir := filepath.Join(toolsDir, "orders")
		writeEffectStub(t, filepath.Join(venvBinPath(t, pkgDir), "lookup"), map[string]string{"lookup": remotetool.EffectReadOnly})
		writePackageManifestYAML(t, pkgDir, packageLookupManifestHead+rawManifest+"\n")
		var logged bytes.Buffer
		NewPackageProcessor(toolsDir, slog.New(slog.NewJSONHandler(&logged, nil))).processPackage(t.Context(), pkgDir, "orders")
		record := recordWithMessage(logRecords(t, &logged), "tool effect disagreement: the package manifest effect")
		if record == nil {
			t.Fatalf("no warning for the invalid manifest effect; log: %s", logged.String())
		}
		if record["level"] != "WARN" || record["tool"] != "lookup" || record["pkg"] != "orders" || record["valueLength"] != float64(len(rawManifest)) {
			t.Errorf("warning fields = %v, want level WARN, tool lookup, pkg orders, valueLength %d", record, len(rawManifest))
		}
		if strings.Contains(logged.String(), rawManifest) {
			t.Errorf("the raw manifest value reached the log: %s", logged.String())
		}
	})

	t.Run("an unparsable schema effect", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		exe := filepath.Join(dir, "lookup")
		writeEffectStub(t, exe, map[string]string{"lookup": rawSchema})
		entries := map[string]*ManifestEntry{
			"lookup": {ToolName: "lookup", Executable: exe, ToolDir: dir, Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly},
		}
		var logged bytes.Buffer
		discoverToolsInto(entries, nil, slog.New(slog.NewJSONHandler(&logged, nil)))
		record := recordWithMessage(logRecords(t, &logged), "tool effect disagreement: --schema emitted an effect")
		if record == nil {
			t.Fatalf("no warning for the unparsable schema effect; log: %s", logged.String())
		}
		if record["level"] != "WARN" || record["tool"] != "lookup" || record["manifestValue"] != remotetool.EffectReadOnly || record["schemaValueLength"] != float64(len(rawSchema)) {
			t.Errorf("warning fields = %v, want tool lookup, manifestValue read_only, schemaValueLength %d", record, len(rawSchema))
		}
		if strings.Contains(logged.String(), rawSchema) {
			t.Errorf("the raw schema value reached the log: %s", logged.String())
		}
	})

	t.Run("a package manifest that cannot be decoded", func(t *testing.T) {
		t.Parallel()
		toolsDir := t.TempDir()
		pkgDir := filepath.Join(toolsDir, "orders")
		writeEffectStub(t, filepath.Join(venvBinPath(t, pkgDir), "lookup"), map[string]string{"lookup": remotetool.EffectReadOnly})
		writePackageManifestYAML(t, pkgDir, packageLookupManifestHead+"["+rawManifest+"]\n")
		var logged bytes.Buffer
		NewPackageProcessor(toolsDir, slog.New(slog.NewJSONHandler(&logged, nil))).processPackage(t.Context(), pkgDir, "orders")
		record := recordWithMessage(logRecords(t, &logged), "package manifest could not be decoded")
		if record == nil {
			t.Fatalf("no warning for the undecodable manifest; log: %s", logged.String())
		}
		if record["level"] != "WARN" || record["pkg"] != "orders" || record["errorClass"] == nil || record["errorLen"] == nil {
			t.Errorf("warning fields = %v, want level WARN, pkg orders, errorClass and errorLen", record)
		}
		if strings.Contains(logged.String(), rawManifest) {
			t.Errorf("the raw manifest text reached the log: %s", logged.String())
		}
	})
}

func TestParseSchemaOutput_ANonStringEffectIsUnparsableForThatToolOnly(t *testing.T) {
	t.Parallel()
	tools, err := parseSchemaOutput([]byte(`{"tools":{
		"a":{"description":"d","effect":5},
		"b":{"description":"d","effect":"side_effecting"},
		"c":{"description":"d","effect":null},
		"d":{"description":"d","effect":["read_only"]},
		"e":{"description":"d"}}}`), "probe")
	if err != nil {
		t.Fatalf("parseSchemaOutput: %v; one tool's malformed effect must not fail the whole probe", err)
	}
	for name, wantValid := range map[string]bool{"a": false, "b": true, "c": true, "d": false, "e": true} {
		effect := tools[name].Effect
		switch name {
		case "b":
			if effect != remotetool.EffectSideEffecting {
				t.Errorf("tool b: effect %q, want side_effecting", effect)
			}
		case "c", "e":
			if effect != "" {
				t.Errorf("tool %s: effect %q, want none", name, effect)
			}
		default:
			if effect == "" || remotetool.ValidEffect(effect) != wantValid {
				t.Errorf("tool %s: effect %q, want a non-empty unparsable marker", name, effect)
			}
		}
	}
}

func TestManifestLoad_ANonStringSchemaEffectCannotLeaveAManifestReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := filepath.Join(dir, "lookup")
	writeSchemaStub(t, exe, `{"tools":{"lookup":{"description":"d","parameters":{"type":"object","properties":{}},"effect":5},"other":{"description":"d","effect":"side_effecting"}}}`)
	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n  lookup:\n    executable: " + exe + "\n    effect: read_only\n    parameters:\n      type: object\n      properties: {}\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest %s: %v", manifestPath, err)
	}
	m := NewManifest(manifestPath, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := m.GetTool("lookup")
	if entry == nil {
		t.Fatal("lookup not loaded")
	}
	if entry.Effect != remotetool.EffectSideEffecting {
		t.Fatalf("manifest read_only with a non-string --schema effect: effect = %q, want side_effecting", entry.Effect)
	}
}

const lookupWithDeclaredReadOnlyAndParameters = "    effect: read_only\n    parameters:\n      type: object\n      properties: {}\n"

func loadManifestWithBrokenProbe(t *testing.T, schemaOutput string, logger *slog.Logger) (*Manifest, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "lookup")
	writeSchemaStub(t, exe, schemaOutput)
	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n  lookup:\n    executable: " + exe + "\n" + lookupWithDeclaredReadOnlyAndParameters
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest %s: %v", manifestPath, err)
	}
	m := NewManifest(manifestPath, logger)
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m, exe
}

func TestManifestLoad_AFailedProbeNeverLeavesAManifestReadOnly(t *testing.T) {
	t.Parallel()
	for name, schemaOutput := range map[string]string{
		"a sibling tool with a bad field": `{"tools":{"lookup":{"description":"d","effect":"side_effecting"},"other":{"description":5}}}`,
		"no tools":                        `{"tools":{}}`,
		"not JSON":                        `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var logged bytes.Buffer
			m, _ := loadManifestWithBrokenProbe(t, schemaOutput, slog.New(slog.NewJSONHandler(&logged, nil)))
			entry := m.GetTool("lookup")
			if entry == nil {
				t.Fatal("lookup not loaded")
			}
			if entry.Effect != remotetool.EffectSideEffecting {
				t.Fatalf("after a failed probe, effect = %q, want side_effecting", entry.Effect)
			}
			record := recordWithMessage(logRecords(t, &logged), "tool effect unverified")
			if record == nil {
				t.Fatalf("no unverified-effect warning; log: %s", logged.String())
			}
			if record["level"] != "WARN" || record["tool"] != "lookup" || record["declaredEffect"] != remotetool.EffectReadOnly {
				t.Errorf("warning fields = %v, want level WARN, tool lookup, declaredEffect read_only", record)
			}
		})
	}
}

func TestRefreshToolSchema_ASuccessfulProbeRestoresTheDeclaredEffectAfterAFailedOne(t *testing.T) {
	t.Parallel()
	m, exe := loadManifestWithBrokenProbe(t, `not json`, testLogger())
	if got := m.GetTool("lookup").Effect; got != remotetool.EffectSideEffecting {
		t.Fatalf("after the failed probe, effect = %q, want side_effecting", got)
	}
	writeEffectStub(t, exe, map[string]string{"lookup": ""})
	if _, err := m.RefreshToolSchema("lookup"); err != nil {
		t.Fatalf("RefreshToolSchema: %v", err)
	}
	if got := m.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
		t.Fatalf("after a successful probe that declares nothing, effect = %q, want the manifest's read_only", got)
	}
}

func TestPackageProcessing_ATransientProbeFailureIsNotBakedIntoTheMergedManifest(t *testing.T) {
	t.Parallel()
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "orders")
	exe := filepath.Join(venvBinPath(t, pkgDir), "lookup")
	writeProbeScript(t, exe, "#!/bin/sh\nexit 1\n")
	writePackageManifestYAML(t, pkgDir, packageLookupManifestHead+"read_only\n    parameters:\n      - name: q\n        type: string\n")
	p := NewPackageProcessor(toolsDir, testLogger())
	tools, errs := p.processPackage(t.Context(), pkgDir, "orders")
	if got := processedEffects(tools)["lookup"]; got != remotetool.EffectReadOnly {
		t.Fatalf("processed effects = %v (errors %v), want lookup to keep its declared read_only: the downgrade belongs to the load that sees the probe fail", processedEffects(tools), errs)
	}
	if err := p.writeMergedManifest(tools); err != nil {
		t.Fatalf("writeMergedManifest: %v", err)
	}
	m := NewManifestFromSources([]string{filepath.Join(toolsDir, "manifest.yaml")}, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	key := "orders" + PackageToolSep + "lookup"
	entry := m.GetTool(key)
	if entry == nil || entry.Effect != remotetool.EffectSideEffecting || entry.DeclaredEffect != remotetool.EffectReadOnly {
		t.Fatalf("after the failed probe, entry = %+v, want side_effecting with the declared read_only kept", entry)
	}

	writeEffectStub(t, exe, map[string]string{"lookup": ""})
	if _, err := m.RefreshToolSchema(key); err != nil {
		t.Fatalf("RefreshToolSchema: %v", err)
	}
	if got := m.GetTool(key).Effect; got != remotetool.EffectReadOnly {
		t.Fatalf("after a later probe that succeeds, effect = %q, want the declared read_only restored", got)
	}
}

func TestWarnEffectConflictLogsTheDeclaredEffectAsALengthWhenItIsInvalid(t *testing.T) {
	t.Parallel()
	const rawDeclared = "Raw-Declared-Effect"
	var logged bytes.Buffer
	warnEffectConflict(slog.New(slog.NewJSONHandler(&logged, nil)), "lookup", rawDeclared, remotetool.EffectReadOnly)
	record := recordWithMessage(logRecords(t, &logged), "tool effect disagreement: the manifest effect is neither")
	if record == nil {
		t.Fatalf("no warning for an invalid declared effect; log: %s", logged.String())
	}
	if record["level"] != "WARN" || record["tool"] != "lookup" || record["manifestValueLength"] != float64(len(rawDeclared)) {
		t.Errorf("warning fields = %v, want level WARN, tool lookup, manifestValueLength %d", record, len(rawDeclared))
	}
	if strings.Contains(logged.String(), rawDeclared) {
		t.Errorf("the raw declared value reached the log: %s", logged.String())
	}
}

func TestWarnEffectConflictLogsBothValidEffectsWhenTheyDisagree(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	warnEffectConflict(slog.New(slog.NewJSONHandler(&logged, nil)), "lookup", remotetool.EffectReadOnly, remotetool.EffectSideEffecting)
	record := recordWithMessage(logRecords(t, &logged), "tool effect disagreement: the manifest and --schema declare different effects")
	if record == nil {
		t.Fatalf("no conflict warning; log: %s", logged.String())
	}
	if record["tool"] != "lookup" || record["manifestValue"] != remotetool.EffectReadOnly || record["schemaValue"] != remotetool.EffectSideEffecting {
		t.Errorf("warning fields = %v, want tool lookup, manifestValue read_only, schemaValue side_effecting", record)
	}
}

func TestRefreshToolSchema_AFailedRefreshDowngradesAPreviouslyVerifiedReadOnly(t *testing.T) {
	t.Parallel()
	for _, probe := range []string{probeExitsNonZero, probeListsOthers} {
		t.Run(probe, func(t *testing.T) {
			t.Parallel()
			m, exe := loadSingleToolManifest(t, remotetool.EffectReadOnly, map[string]string{"lookup": ""})
			if got := m.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
				t.Fatalf("before the refresh, effect = %q, want the verified read_only", got)
			}
			prepareProbe(t, filepath.Dir(exe), probe)
			if _, err := m.RefreshToolSchema("lookup"); err == nil {
				t.Fatal("RefreshToolSchema reported success although the probe could not confirm the tool")
			}
			if got := m.GetTool("lookup").Effect; got != remotetool.EffectSideEffecting {
				t.Fatalf("after a failed refresh (%s), effect = %q, want side_effecting", probe, got)
			}
			writeEffectStub(t, exe, map[string]string{"lookup": ""})
			if _, err := m.RefreshToolSchema("lookup"); err != nil {
				t.Fatalf("RefreshToolSchema after the probe recovered: %v", err)
			}
			if got := m.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
				t.Fatalf("after a probe that succeeds again, effect = %q, want read_only restored", got)
			}
		})
	}
}

func TestDiscoverToolsInto_ADeclarationMadeForAnotherNameIsNotCarriedToARenamedSoleTool(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := filepath.Join(dir, "lookup")
	writeEffectStub(t, exe, map[string]string{"other": ""})
	entries := map[string]*ManifestEntry{
		"lookup": {ToolName: "lookup", Executable: exe, ToolDir: dir, Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly},
	}
	discoverToolsInto(entries, nil, testLogger())
	renamed := entries["other"]
	if renamed == nil {
		t.Fatalf("the sole tool was not renamed; entries: %v", entryKeysOf(entries))
	}
	if renamed.Effect == remotetool.EffectReadOnly {
		t.Fatalf("renamed tool carries effect %q from a declaration made for lookup, want side_effecting or none", renamed.Effect)
	}
}

func TestRefreshToolSchema_ASoleToolListedUnderAnotherNameDoesNotKeepTheDeclaration(t *testing.T) {
	t.Parallel()
	m, exe := loadSingleToolManifest(t, remotetool.EffectReadOnly, map[string]string{"lookup": ""})
	if got := m.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
		t.Fatalf("before the refresh, effect = %q, want the verified read_only", got)
	}
	writeEffectStub(t, exe, map[string]string{"other": ""})
	if _, err := m.RefreshToolSchema("lookup"); err != nil {
		t.Fatalf("RefreshToolSchema with the binary now listing only other: %v", err)
	}
	if got := m.GetTool("lookup").Effect; got == remotetool.EffectReadOnly {
		t.Fatalf("after the binary started listing only other, effect = %q, want a declaration made for lookup not carried", got)
	}
}

func TestRefreshToolSchema_AReadOnlyTheSchemaDeclaredIsDowngradedWhenTheRefreshCannotConfirmIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ declared, probe string }{
		{"", probeExitsNonZero},
		{"", probeListsOthers},
		{"", probeMissing},
		{remotetool.EffectReadOnly, probeMissing},
	} {
		t.Run("declared "+tc.declared+" then "+tc.probe, func(t *testing.T) {
			t.Parallel()
			m, exe := loadSingleToolManifest(t, tc.declared, map[string]string{"lookup": remotetool.EffectReadOnly})
			if got := m.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
				t.Fatalf("before the refresh, effect = %q, want read_only", got)
			}
			if tc.probe == probeMissing {
				if err := os.Remove(exe); err != nil {
					t.Fatalf("remove %s: %v", exe, err)
				}
			} else {
				prepareProbe(t, filepath.Dir(exe), tc.probe)
			}
			_, _ = m.RefreshToolSchema("lookup")
			if got := m.GetTool("lookup").Effect; got != remotetool.EffectSideEffecting {
				t.Fatalf("after a refresh that %s, effect = %q, want side_effecting", tc.probe, got)
			}
			writeEffectStub(t, exe, map[string]string{"lookup": remotetool.EffectReadOnly})
			if _, err := m.RefreshToolSchema("lookup"); err != nil {
				t.Fatalf("RefreshToolSchema once the binary is back: %v", err)
			}
			if got := m.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
				t.Fatalf("after the binary is back, effect = %q, want read_only restored", got)
			}
		})
	}
}

func TestRefreshToolSchema_RepeatedFailuresWarnOnceUntilTheToolIsVerifiedAgain(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	m, exe := loadSingleToolManifestLogging(t, remotetool.EffectReadOnly, map[string]string{"lookup": ""}, slog.New(slog.NewJSONHandler(&logged, nil)))
	prepareProbe(t, filepath.Dir(exe), probeExitsNonZero)
	for range 4 {
		_, _ = m.RefreshToolSchema("lookup")
	}
	if got := countUnverifiedWarnings(t, &logged); got != 1 {
		t.Fatalf("four failed refreshes logged %d unverified-effect warnings, want 1", got)
	}
	writeEffectStub(t, exe, map[string]string{"lookup": ""})
	if _, err := m.RefreshToolSchema("lookup"); err != nil {
		t.Fatalf("RefreshToolSchema once the binary answers: %v", err)
	}
	prepareProbe(t, filepath.Dir(exe), probeExitsNonZero)
	_, _ = m.RefreshToolSchema("lookup")
	if got := countUnverifiedWarnings(t, &logged); got != 2 {
		t.Fatalf("a failure after a verified refresh made %d warnings in all, want 2", got)
	}
}

func TestRefreshToolSchema_ASoleToolThatCannotBeTiedToOneOfSeveralEntriesIsNotReadOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := filepath.Join(dir, "shared")
	writeEffectStub(t, exe, map[string]string{"lookup": remotetool.EffectReadOnly, "extra": remotetool.EffectReadOnly})
	manifest := "version: 1\ntools:\n  lookup:\n    executable: " + exe + "\n  extra:\n    executable: " + exe + "\n"
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest %s: %v", manifestPath, err)
	}
	m := NewManifest(manifestPath, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := m.GetTool("lookup").Effect; got != remotetool.EffectReadOnly {
		t.Fatalf("before the refresh, effect = %q, want read_only", got)
	}
	writeEffectStub(t, exe, map[string]string{"renamed": remotetool.EffectReadOnly})
	refresh, err := m.RefreshToolSchema("lookup")
	if err == nil {
		t.Fatal("RefreshToolSchema tied a sole listed tool to one of two entries sharing the binary")
	}
	if !refresh.Changed {
		t.Error("an ambiguous refresh that downgraded read_only reported Changed = false, so the downgrade is not published at once")
	}
	if got := m.GetTool("lookup").Effect; got != remotetool.EffectSideEffecting {
		t.Fatalf("after an ambiguous refresh, effect = %q, want side_effecting", got)
	}
}

func TestRefreshToolSchema_ADistrustFromAnEarlierReadOnlyDoesNotWarnForAToolThatIsStrictByItsOwnSchema(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	m, exe := loadSingleToolManifestLogging(t, "", map[string]string{"lookup": remotetool.EffectReadOnly}, slog.New(slog.NewJSONHandler(&logged, nil)))
	prepareProbe(t, filepath.Dir(exe), probeExitsNonZero)
	_, _ = m.RefreshToolSchema("lookup")
	writeEffectStub(t, exe, map[string]string{"lookup": remotetool.EffectSideEffecting})
	if _, err := m.RefreshToolSchema("lookup"); err != nil {
		t.Fatalf("RefreshToolSchema once the binary answers side_effecting: %v", err)
	}
	prepareProbe(t, filepath.Dir(exe), probeListsOthers)
	_, _ = m.RefreshToolSchema("lookup")
	if got := countUnverifiedWarnings(t, &logged); got != 1 {
		t.Fatalf("a tool that is side_effecting by its own schema logged %d unverified-effect warnings in all, want only the first", got)
	}
}

func TestRenamedSoleTool_ADeclaredSideEffectingIsCarriedEvenOverTheNewNamesReadOnly(t *testing.T) {
	t.Parallel()
	t.Run("plain manifest load", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		exe := filepath.Join(dir, "lookup")
		writeEffectStub(t, exe, map[string]string{"other": remotetool.EffectReadOnly})
		entries := map[string]*ManifestEntry{
			"lookup": {ToolName: "lookup", Executable: exe, ToolDir: dir, Effect: remotetool.EffectSideEffecting, DeclaredEffect: remotetool.EffectSideEffecting},
		}
		discoverToolsInto(entries, nil, testLogger())
		renamed := entries["other"]
		if renamed == nil {
			t.Fatalf("the sole tool was not renamed; entries: %v", entryKeysOf(entries))
		}
		if renamed.Effect != remotetool.EffectSideEffecting {
			t.Fatalf("renamed tool effect = %q, want the declared side_effecting kept over other's read_only", renamed.Effect)
		}
	})
	t.Run("schema refresh", func(t *testing.T) {
		t.Parallel()
		m, exe := loadSingleToolManifest(t, remotetool.EffectSideEffecting, map[string]string{"lookup": ""})
		writeEffectStub(t, exe, map[string]string{"other": remotetool.EffectReadOnly})
		if _, err := m.RefreshToolSchema("lookup"); err != nil {
			t.Fatalf("RefreshToolSchema with the binary now listing only other: %v", err)
		}
		if got := m.GetTool("lookup").Effect; got != remotetool.EffectSideEffecting {
			t.Fatalf("after the refresh, effect = %q, want the declared side_effecting kept over other's read_only", got)
		}
	})
	t.Run("skill manifest parse", func(t *testing.T) {
		t.Parallel()
		manifestPath, toolsDir := writeSkillToolsDir(t, "version: 1\ntools:\n  lookup:\n    executable: lookup\n    effect: side_effecting\n")
		writeEffectStub(t, filepath.Join(toolsDir, "lookup"), map[string]string{"other": remotetool.EffectReadOnly})
		entries, _, err := parseSkillToolManifest(nil, manifestPath, toolsDir, "orders-kit")
		if err != nil {
			t.Fatalf("parseSkillToolManifest: %v", err)
		}
		entry := entries["lookup"]
		if entry == nil {
			t.Fatal("lookup not parsed")
		}
		if entry.Effect != remotetool.EffectSideEffecting {
			t.Fatalf("skill tool effect = %q, want the declared side_effecting kept over other's read_only", entry.Effect)
		}
	})
}

func TestUnverifiedEffectReasons_AreTheStableLogValues(t *testing.T) {
	t.Parallel()
	for constant, want := range map[string]string{
		unverifiedSchemaFailed:     "schema_probe_failed",
		unverifiedToolNotListed:    "tool_not_listed",
		unverifiedExecutableAbsent: "executable_missing",
		unverifiedHostIncompatible: "host_incompatible",
		unverifiedToolRenamed:      "tool_renamed",
		unverifiedToolAmbiguous:    "tool_ambiguous",
	} {
		if constant != want {
			t.Errorf("reason constant = %q, want the stable log value %q", constant, want)
		}
	}
}

func TestRefreshToolSchema_AChangedReasonWhileStillDistrustedWarnsOnceMore(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	m, exe := loadSingleToolManifestLogging(t, remotetool.EffectReadOnly, map[string]string{"lookup": ""}, slog.New(slog.NewJSONHandler(&logged, nil)))
	prepareProbe(t, filepath.Dir(exe), probeExitsNonZero)
	for range 3 {
		_, _ = m.RefreshToolSchema("lookup")
	}
	prepareProbe(t, filepath.Dir(exe), probeListsOthers)
	for range 3 {
		_, _ = m.RefreshToolSchema("lookup")
	}
	records := unverifiedRecords(t, &logged)
	if len(records) != 2 {
		t.Fatalf("a probe failing for one reason and then another logged %d warnings, want 2: %v", len(records), records)
	}
	if records[0]["reason"] != "schema_probe_failed" || records[1]["reason"] != "tool_not_listed" {
		t.Errorf("warning reasons = %v and %v, want schema_probe_failed then tool_not_listed", records[0]["reason"], records[1]["reason"])
	}
}

func TestRenamedSoleTool_TheDroppedDeclarationWarnsOnceWithItsFields(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, logged *bytes.Buffer) {
		t.Helper()
		records := unverifiedRecords(t, logged)
		if len(records) != 1 {
			t.Fatalf("dropping the declaration logged %d warnings, want 1: %v", len(records), records)
		}
		record := records[0]
		if record["level"] != "WARN" || record["tool"] != "lookup" || record["declaredEffect"] != "read_only" ||
			record["effect"] != "" || record["reason"] != "tool_renamed" || record["listedNameLength"] != float64(len("other")) {
			t.Errorf("warning fields = %v, want WARN, tool lookup, declaredEffect read_only, no resulting effect, reason tool_renamed, listedNameLength 5", record)
		}
	}
	t.Run("plain manifest load", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		exe := filepath.Join(dir, "lookup")
		writeEffectStub(t, exe, map[string]string{"other": ""})
		var logged bytes.Buffer
		entries := map[string]*ManifestEntry{
			"lookup": {ToolName: "lookup", Executable: exe, ToolDir: dir, Effect: remotetool.EffectReadOnly, DeclaredEffect: remotetool.EffectReadOnly},
		}
		discoverToolsInto(entries, nil, slog.New(slog.NewJSONHandler(&logged, nil)))
		check(t, &logged)
	})
	t.Run("schema refresh", func(t *testing.T) {
		t.Parallel()
		var logged bytes.Buffer
		m, exe := loadSingleToolManifestLogging(t, remotetool.EffectReadOnly, map[string]string{"lookup": ""}, slog.New(slog.NewJSONHandler(&logged, nil)))
		writeEffectStub(t, exe, map[string]string{"other": ""})
		for range 3 {
			if _, err := m.RefreshToolSchema("lookup"); err != nil {
				t.Fatalf("RefreshToolSchema with the binary listing only other: %v", err)
			}
		}
		check(t, &logged)
	})
}
