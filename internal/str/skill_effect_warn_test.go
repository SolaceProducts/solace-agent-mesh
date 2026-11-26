// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func writeSkillWithReadOnlyTool(t *testing.T, probe string, exposeGlobally bool) (skillsDir string) {
	t.Helper()
	skillsDir = t.TempDir()
	toolsDir := filepath.Join(skillsDir, "kit", "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", toolsDir, err)
	}
	skillMD := "---\nname: kit\ndescription: a kit\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "kit", "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	manifest := "version: 1\n"
	if exposeGlobally {
		manifest += "expose_globally: true\n"
	}
	manifest += "tools:\n  lookup:\n    executable: lookup\n    effect: read_only\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write skill tool manifest: %v", err)
	}
	prepareProbe(t, toolsDir, probe)
	return skillsDir
}

func unverifiedRecords(t *testing.T, logged *bytes.Buffer) []map[string]any {
	t.Helper()
	var matched []map[string]any
	for _, record := range logRecords(t, logged) {
		if msg, _ := record["msg"].(string); strings.HasPrefix(msg, "tool effect unverified") {
			matched = append(matched, record)
		}
	}
	return matched
}

func countUnverifiedWarnings(t *testing.T, logged *bytes.Buffer) int {
	t.Helper()
	return len(unverifiedRecords(t, logged))
}

func TestSkillReload_AnUnchangedSkillLogsNoUnverifiedEffectWarningOnRescan(t *testing.T) {
	for _, probe := range []string{probeListsOthers, probeListsOneOther, probeForeignBinary, probeMissing} {
		t.Run(probe, func(t *testing.T) {
			var parsed bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&parsed, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			reg := NewSkillRegistry(writeSkillWithReadOnlyTool(t, probe, false))
			if err := reg.Load(); err != nil {
				t.Fatalf("Load: %v", err)
			}
			for i := range 5 {
				diff, err := reg.Reload()
				if err != nil {
					t.Fatalf("Reload %d: %v", i, err)
				}
				if diff != nil {
					t.Fatalf("Reload %d reported a change %+v although nothing changed", i, diff)
				}
			}
			if got := countUnverifiedWarnings(t, &parsed); got != 0 {
				t.Errorf("six scans of an unchanged skill logged %d unverified-effect warnings, want none from the parse", got)
			}
		})
	}
}

func TestSkillToolManifest_AnInvalidEffectDropsTheToolWithAWarnThatLogsOnlyItsLength(t *testing.T) {
	const authorsValue = "maybe-read-only-canary"
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	manifestPath, toolsDir := writeSkillToolsDir(t, "version: 1\ntools:\n  lookup:\n    executable: lookup\n    effect: "+authorsValue+"\n  other:\n    executable: other\n")
	entries, _, err := parseSkillToolManifest(nil, manifestPath, toolsDir, "orders-kit")
	if err != nil {
		t.Fatalf("parseSkillToolManifest: %v", err)
	}
	if entries["lookup"] != nil || entries["other"] == nil {
		t.Errorf("entries = %v, want only the tool without an invalid effect", slices.Collect(maps.Keys(entries)))
	}
	record := recordWithMessage(logRecords(t, &logged), "skipping bundled skill tool: invalid manifest entry")
	if record == nil {
		t.Fatalf("a tool dropped for an invalid effect logged nothing; log: %s", logged.String())
	}
	if record["level"] != "WARN" || record["tool"] != "lookup" || record["skill"] != "orders-kit" || record["valueLength"] != float64(len(authorsValue)) {
		t.Errorf("warning fields = %v, want level WARN, tool lookup, skill orders-kit, valueLength %d", record, len(authorsValue))
	}
	if strings.Contains(logged.String(), authorsValue) {
		t.Errorf("the skill author's effect string reached the log: %s", logged.String())
	}
}

func writeSkillWithToolManifest(t *testing.T, toolManifest string) (skillsDir, manifestPath string) {
	t.Helper()
	skillsDir = t.TempDir()
	toolsDir := filepath.Join(skillsDir, "kit", "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", toolsDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "kit", "SKILL.md"), []byte("---\nname: kit\ndescription: a kit\n---\nbody\n"), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	manifestPath = filepath.Join(toolsDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(toolManifest), 0o600); err != nil {
		t.Fatalf("write skill tool manifest: %v", err)
	}
	return skillsDir, manifestPath
}

func invalidEntryRecords(t *testing.T, logged *bytes.Buffer) []map[string]any {
	t.Helper()
	var matched []map[string]any
	for _, record := range logRecords(t, logged) {
		if msg, _ := record["msg"].(string); strings.HasPrefix(msg, "skipping bundled skill tool: invalid manifest entry") {
			matched = append(matched, record)
		}
	}
	return matched
}

func TestSkillReload_AnUnchangedBadToolManifestWarnsOncePerChange(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	const badEffect = "version: 1\ntools:\n  lookup:\n    executable: lookup\n    effect: bogus\n"
	skillsDir, manifestPath := writeSkillWithToolManifest(t, badEffect)
	reg := NewSkillRegistry(skillsDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := range 4 {
		if _, err := reg.Reload(); err != nil {
			t.Fatalf("Reload %d: %v", i, err)
		}
	}
	if got := len(invalidEntryRecords(t, &logged)); got != 1 {
		t.Fatalf("five scans of an unchanged bad manifest logged %d warnings, want 1", got)
	}

	changed := badEffect + "  other:\n    executable: other\n"
	if err := os.WriteFile(manifestPath, []byte(changed), 0o600); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
	if _, err := reg.Reload(); err != nil {
		t.Fatalf("Reload after change: %v", err)
	}
	if got := len(invalidEntryRecords(t, &logged)); got != 2 {
		t.Errorf("after the manifest changed the log holds %d warnings, want 2: a changed manifest warns again", got)
	}
}

func TestSkillToolManifest_AFailureThatIsNotTheEffectNamesItsFieldAndNoValueLength(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	manifestPath, toolsDir := writeSkillToolsDir(t, "version: 1\ntools:\n  lookup:\n    executable: lookup\n    volume_params:\n      - name: data\n        mount_path: /data\n        mode: weird\n")
	entries, _, err := parseSkillToolManifest(nil, manifestPath, toolsDir, "orders-kit")
	if err != nil {
		t.Fatalf("parseSkillToolManifest: %v", err)
	}
	if entries["lookup"] != nil {
		t.Fatalf("a tool with an invalid volume mode was kept: %v", entries["lookup"])
	}
	records := invalidEntryRecords(t, &logged)
	if len(records) != 1 {
		t.Fatalf("got %d warnings, want 1; log: %s", len(records), logged.String())
	}
	if _, present := records[0]["valueLength"]; present || records[0]["field"] != "other" {
		t.Errorf("warning fields = %v, want field other and no valueLength", records[0])
	}
}

func registerSkillTools(t *testing.T, skillsDir string) (*Worker, *bytes.Buffer) {
	t.Helper()
	reg := NewSkillRegistry(skillsDir)
	if err := reg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	var registered bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&registered, nil))
	w := &Worker{cfg: WorkerConfig{SkillsDir: skillsDir, Namespace: "ns"}, logger: logger, manifest: NewManifest("", logger), skillRegistry: reg}
	w.registerBundledSkillToolsForSkills(t.Context(), nil)
	return w, &registered
}

func TestRegisterBundledSkillTools_AnUnverifiedReadOnlyWarnsOnceAndAVerifiedOneNever(t *testing.T) {
	for _, tc := range []struct {
		name           string
		probe          string
		exposeGlobally bool
		keys           []string
		effect         string
		warns          int
	}{
		{"kept in the skill", probeListsOthers, false, []string{"kit__lookup"}, remotetool.EffectSideEffecting, 1},
		{"exposed globally", probeListsOthers, true, []string{"kit__lookup", "lookup"}, remotetool.EffectSideEffecting, 1},
		{"verified", probeConfirmsNone, false, []string{"kit__lookup"}, remotetool.EffectReadOnly, 0},
		{"foreign binary", probeForeignBinary, false, nil, "", 0},
		{"missing executable", probeMissing, false, nil, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, registered := registerSkillTools(t, writeSkillWithReadOnlyTool(t, tc.probe, tc.exposeGlobally))
			if registeredAny := w.manifest.GetTool("kit__lookup") != nil; registeredAny != (len(tc.keys) > 0) {
				t.Fatalf("kit__lookup registered = %v, want %v", registeredAny, len(tc.keys) > 0)
			}
			for _, key := range tc.keys {
				entry := w.manifest.GetTool(key)
				if entry == nil {
					t.Fatalf("%s was not registered", key)
				}
				if entry.Effect != tc.effect {
					t.Errorf("effect registered under %s = %q, want %q", key, entry.Effect, tc.effect)
				}
			}
			records := unverifiedRecords(t, registered)
			if len(records) != tc.warns {
				t.Fatalf("registration under %d key(s) logged %d unverified-effect warnings, want %d: %v", len(tc.keys), len(records), tc.warns, records)
			}
			for _, record := range records {
				key, _ := record["key"].(string)
				if record["level"] != "WARN" || !slices.Contains(tc.keys, key) || record["tool"] != "lookup" ||
					record["declaredEffect"] != "read_only" || record["effect"] != "side_effecting" || record["reason"] != "tool_not_listed" {
					t.Errorf("warning fields = %v, want WARN, a key among %v, tool lookup, declaredEffect read_only, effect side_effecting, reason tool_not_listed", record, tc.keys)
				}
			}
		})
	}
}

func TestRegisterBundledSkillTools_ADroppedDeclarationWarnsOnceEvenWhenExposedGlobally(t *testing.T) {
	for _, exposeGlobally := range []bool{false, true} {
		name := "kept in the skill"
		if exposeGlobally {
			name = "exposed globally"
		}
		t.Run(name, func(t *testing.T) {
			w, registered := registerSkillTools(t, writeSkillWithReadOnlyTool(t, probeListsOneOther, exposeGlobally))
			if w.manifest.GetTool("kit__lookup") == nil {
				t.Fatal("kit__lookup was not registered")
			}
			if exposeGlobally && w.manifest.GetTool("lookup") == nil {
				t.Fatal("the globally exposed lookup was not registered, so the test never reached a second registration")
			}
			records := unverifiedRecords(t, registered)
			if len(records) != 1 {
				t.Fatalf("registration logged %d unverified-effect warnings, want 1: %v", len(records), records)
			}
			record := records[0]
			if record["level"] != "WARN" || record["tool"] != "lookup" || record["declaredEffect"] != "read_only" ||
				record["effect"] != "" || record["reason"] != "tool_renamed" || record["listedNameLength"] != float64(len("other")) {
				t.Errorf("warning fields = %v, want WARN, tool lookup, declaredEffect read_only, no resulting effect, reason tool_renamed, listedNameLength 5", record)
			}
			if msg, _ := record["msg"].(string); strings.Contains(msg, "and the tool is side_effecting") {
				t.Errorf("message %q claims side_effecting although the tool can end with any effect", msg)
			}
		})
	}
}
