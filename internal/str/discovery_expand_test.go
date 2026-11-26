// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func writeMultiToolStub(t *testing.T, path string, toolNames ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(schemaStubScript("d", toolNames...)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestExpandMultiToolBinaryInheritsSandboxState(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "multi")
	writeMultiToolStub(t, exe, "alpha", "beta")

	placeholder := &ManifestEntry{
		ToolName:       "placeholder",
		Executable:     exe,
		ToolDir:        dir,
		TimeoutSeconds: 42,
		SandboxProfile: "restrictive",
		VolumeParams: []VolumeParam{{
			Name: "data", Mode: volumeModeReadOnly, MountPath: "/data",
		}},
		Capabilities:     map[string]any{"cap": true},
		Unservable:       true,
		UnservableReason: "stale",
		BareToolName:     "stale",
	}
	entries := map[string]*ManifestEntry{"placeholder": placeholder}

	discoverToolsInto(entries, nil, testLogger())

	if _, stillThere := entries["placeholder"]; stillThere {
		t.Error("placeholder entry should have been replaced by the expansion")
	}
	for _, name := range []string{"alpha", "beta"} {
		e, ok := entries[name]
		if !ok {
			t.Fatalf("expanded entry %q missing; got %v", name, entryKeysOf(entries))
		}
		if len(e.VolumeParams) != 1 || e.VolumeParams[0].Mode != volumeModeReadOnly {
			t.Errorf("%s: VolumeParams = %v, want the placeholder's readonly slot inherited; "+
				"without it volumeModeFromManifest returns \"\" and the volume is mounted "+
				"read-write under an exclusive lock", name, e.VolumeParams)
		}
		if e.Capabilities["cap"] != true {
			t.Errorf("%s: Capabilities = %v, want the placeholder's inherited", name, e.Capabilities)
		}
		if e.SandboxProfile != "restrictive" {
			t.Errorf("%s: SandboxProfile = %q, want %q", name, e.SandboxProfile, "restrictive")
		}
		if e.TimeoutSeconds != 42 {
			t.Errorf("%s: TimeoutSeconds = %d, want 42", name, e.TimeoutSeconds)
		}
		if e.Unservable {
			t.Errorf("%s: inherited Unservable from the placeholder", name)
		}
		if e.BareToolName != "" {
			t.Errorf("%s: BareToolName = %q, want empty (the name came from --schema)", name, e.BareToolName)
		}
		if e.UnservableReason != "" {
			t.Errorf("%s: inherited UnservableReason %q from the placeholder", name, e.UnservableReason)
		}
		if !e.ToolNameObserved {
			t.Errorf("%s: ToolNameObserved = false, but the name came straight from --schema", name)
		}
	}
}

func TestRenameToMatchDiscoveryRekeysEntry(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sole")
	writeMultiToolStub(t, exe, "actual_name")

	entries := map[string]*ManifestEntry{
		"manifest_name": {
			ToolName:       "manifest_name",
			Executable:     exe,
			ToolDir:        dir,
			SandboxProfile: "restrictive",
		},
	}

	discoverToolsInto(entries, nil, testLogger())

	if _, stillThere := entries["manifest_name"]; stillThere {
		t.Error("entry kept its manifest key; the rename never re-keyed it")
	}
	renamed, ok := entries["actual_name"]
	if !ok {
		t.Fatalf("no entry under the reported name; got %v", entryKeysOf(entries))
	}
	if renamed.ToolName != "actual_name" {
		t.Errorf("ToolName = %q, want %q", renamed.ToolName, "actual_name")
	}
	if !renamed.ToolNameObserved {
		t.Error("ToolNameObserved = false, but the name came from --schema")
	}
	if renamed.SandboxProfile != "restrictive" {
		t.Errorf("SandboxProfile = %q, want the original entry's %q — the rename copies the whole entry",
			renamed.SandboxProfile, "restrictive")
	}
}

func TestRefreshToolSchemaRefusesSoleToolGuessAcrossEntries(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pkgbin")

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n" +
		"  pkg__slack_send:\n    executable: " + exe + "\n" +
		"  pkg__slack_update:\n    executable: " + exe + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManifest(manifestPath, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	writeMultiToolStub(t, exe, "slack_send")

	if _, err := m.RefreshToolSchema("pkg__slack_update"); err == nil {
		t.Fatal("refresh accepted a sole-tool guess across entries sharing a binary; " +
			"pkg__slack_update would dispatch slack_send and return a plausible wrong result")
	}
	entry := m.GetTool("pkg__slack_update")
	if got := m.SnapshotEntry(entry).BareToolName; got != "" {
		t.Errorf("BareToolName = %q, want empty; refusing must record nothing rather than "+
			"point this entry at another tool", got)
	}
	if unservable, _ := m.UnservableState(entry); !unservable {
		t.Error("entry became servable despite the refusal; it would be advertised and " +
			"subscribed with no resolved dispatch name")
	}

	if _, err := m.RefreshToolSchema("pkg__slack_send"); err != nil {
		t.Fatalf("refresh of the entry the binary does report failed: %v", err)
	}
	sendEntry := m.GetTool("pkg__slack_send")
	if got := m.SnapshotEntry(sendEntry).BareToolName; got != "slack_send" {
		t.Errorf("pkg__slack_send BareToolName = %q, want %q", got, "slack_send")
	}
}

func TestParseSchemaOutputDropsEmptyToolName(t *testing.T) {
	out := []byte(`{"tools":{"":{"description":"junk"},"good":{"description":"ok"}}}`)
	tools, err := parseSchemaOutput(out, "x")
	if err != nil {
		t.Fatalf("parseSchemaOutput: %v; one malformed entry must not fail the whole probe", err)
	}
	if _, present := tools[""]; present {
		t.Error("empty tool name survived; it collides with the no-match sentinel")
	}
	if _, present := tools["good"]; !present {
		t.Error("the sibling tool was dropped along with the malformed one")
	}

	if _, err := parseSchemaOutput([]byte(`{"tools":{"":{}}}`), "x"); err == nil {
		t.Error("expected an error when the empty name was the only tool")
	}
}

func TestRefreshToolSchemaBoundCountsInProcessSiblings(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "shared")

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n  pkg__beta:\n    executable: " + exe + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManifest(manifestPath, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	alpha := samtoolsdk.NewTool("alpha", "Alpha",
		func(context.Context, struct{}, *samtoolsdk.ToolContext) (*samtoolsdk.Result, error) {
			return &samtoolsdk.Result{}, nil
		})
	m.Register("sam-x__alpha", &ManifestEntry{
		ToolName:   "alpha",
		Executable: exe,
		inProcess:  alpha,
	})

	writeMultiToolStub(t, exe, "alpha")

	if _, err := m.RefreshToolSchema("pkg__beta"); err == nil {
		t.Fatal("pkg__beta accepted a sole-tool inference for a name belonging to its " +
			"in-process sibling; it would dispatch alpha")
	}
	if got := m.SnapshotEntry(m.GetTool("pkg__beta")).BareToolName; got != "" {
		t.Errorf("BareToolName = %q, want empty", got)
	}
}

func TestRefreshToolSchemaBailsWhenExecutableChangedMidProbe(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	replacement := filepath.Join(dir, "replacement")

	probing := filepath.Join(dir, "probing")
	gate := filepath.Join(dir, "gate")
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--schema\" ]; then\n" +
		"  : > " + probing + "\n" +
		"  while [ ! -f " + gate + " ]; do sleep 0.01; done\n" +
		"  printf '{\"tools\":{\"probe_tool\":{\"description\":\"orig\"}}}\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(original, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMultiToolStub(t, replacement, "replacement_tool")

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n  probe_tool:\n    executable: " + original + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(gate, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManifest(manifestPath, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(probing)

	errCh := make(chan error, 1)
	go func() {
		_, err := m.RefreshToolSchema("probe_tool")
		errCh <- err
	}()

	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(probing); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the --schema probe never started; the handshake file was never written")
		}
		time.Sleep(5 * time.Millisecond)
	}

	m.Register("probe_tool", &ManifestEntry{
		ToolName:   "probe_tool",
		Executable: replacement,
	})
	if err := os.WriteFile(gate, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("refresh applied output from a binary this key no longer names")
		}
		if !strings.Contains(err.Error(), "executable changed during the schema probe") {
			t.Errorf("error = %v, want it to name the executable change", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("RefreshToolSchema never returned")
	}

	got := m.SnapshotEntry(m.GetTool("probe_tool"))
	if got.Executable != replacement {
		t.Errorf("Executable = %q, want the swapped-in %q", got.Executable, replacement)
	}
	if got.BareToolName != "" {
		t.Errorf("BareToolName = %q, want empty; it would have been resolved from the old "+
			"binary's schema output", got.BareToolName)
	}
	if got.Description == "orig" {
		t.Error("the old binary's description was applied to the replacement entry")
	}
}

func entryKeysOf(m map[string]*ManifestEntry) []string {
	return slices.Sorted(maps.Keys(m))
}

func TestEnvWithPythonPath_MatchesInvocationInterpreter(t *testing.T) {
	t.Setenv("PYTHONNOUSERSITE", "0")
	env := envWithPythonPath("/tools/x/python")
	if !slices.Contains(env, "PYTHONNOUSERSITE=1") || slices.Contains(env, "PYTHONNOUSERSITE=0") {
		t.Fatalf("env must carry exactly PYTHONNOUSERSITE=1: %v", env)
	}
	if !slices.Contains(env, "PYTHONPATH=/tools/x/python") {
		t.Fatalf("env lacks PYTHONPATH: %v", env)
	}
}
