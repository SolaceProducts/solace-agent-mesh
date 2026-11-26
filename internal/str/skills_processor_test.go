// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

func buildSkillZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%s): %v", name, err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("zip write(%s): %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

func writeSkillZIP(t *testing.T, skillsDir, skillName string, zipData []byte) string {
	t.Helper()
	skillDir := filepath.Join(skillsDir, skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	zipPath := filepath.Join(skillDir, "skill.zip")
	if err := os.WriteFile(zipPath, zipData, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return zipPath
}

func TestSkillProcessor_ValidZIP_ExtractsAndRenames(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "my-skill"

	zipData := buildSkillZIP(t, map[string][]byte{
		"SKILL.md": []byte("# My Skill\n"),
		"tools.py": []byte("# tools\n"),
	})
	zipPath := writeSkillZIP(t, skillsDir, skillName, zipData)

	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))
	changed, err := p.ProcessAll()
	if err != nil {
		t.Fatalf("ProcessAll: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for a successful extraction")
	}

	if _, statErr := os.Stat(zipPath); !os.IsNotExist(statErr) {
		t.Errorf("skill.zip should have been renamed, but Stat returned: %v", statErr)
	}
	extractedPath := zipPath + ".extracted"
	if _, statErr := os.Stat(extractedPath); statErr != nil {
		t.Errorf("skill.zip.extracted not found: %v", statErr)
	}

	skillMDPath := filepath.Join(skillsDir, skillName, "SKILL.md")
	if _, statErr := os.Stat(skillMDPath); statErr != nil {
		t.Errorf("SKILL.md not extracted: %v", statErr)
	}
}

func TestSkillProcessor_ExtractedSkillSettles(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "settled-skill"
	writeSkillZIP(t, skillsDir, skillName, buildSkillZIP(t, map[string][]byte{
		"SKILL.md": []byte("# Settled Skill\n"),
	}))

	var logs bytes.Buffer
	p := NewSkillProcessor(skillsDir, captureLogger(&logs))
	if _, err := p.ProcessAll(); err != nil {
		t.Fatalf("first ProcessAll: %v", err)
	}

	skillMDPath := filepath.Join(skillsDir, skillName, "SKILL.md")
	const editedSinceUnpack = "# edited after the first pass\n"
	if err := os.WriteFile(skillMDPath, []byte(editedSinceUnpack), 0o644); err != nil {
		t.Fatalf("WriteFile SKILL.md: %v", err)
	}

	changed, err := p.ProcessAll()
	if err != nil {
		t.Fatalf("second ProcessAll: %v", err)
	}
	if changed {
		t.Error("second pass reported a change for a skill already unpacked")
	}
	got, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("ReadFile SKILL.md: %v", err)
	}
	if string(got) != editedSinceUnpack {
		t.Error("second pass unpacked the archive again over the extracted skill")
	}
	if strings.Contains(logs.String(), "failed to rename extracted skill ZIP") {
		t.Errorf("archive was not renamed after unpacking:\n%s", logs.String())
	}
}

func TestSkillProcessor_FailedRenameBacksOffInsteadOfUnpackingEveryTick(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "stuck-skill"
	zipPath := writeSkillZIP(t, skillsDir, skillName, buildSkillZIP(t, map[string][]byte{
		"SKILL.md": []byte("# Stuck Skill\n"),
	}))
	skillDir := filepath.Dir(zipPath)
	obstruction := zipPath + ".extracted"
	writeTree(t, obstruction, map[string]string{"occupied": "x"})

	var logs bytes.Buffer
	p := NewSkillProcessor(skillsDir, captureLogger(&logs))
	clock := time.Now()
	p.now = func() time.Time { return clock }

	if changed, err := p.ProcessAll(); err != nil || !changed {
		t.Fatalf("first ProcessAll = %v, %v; want the skill unpacked", changed, err)
	}
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	const editedSinceUnpack = "# edited after the first pass\n"
	if err := os.WriteFile(skillMDPath, []byte(editedSinceUnpack), 0o644); err != nil {
		t.Fatalf("WriteFile SKILL.md: %v", err)
	}

	clock = clock.Add(swapRetryBase / 2)
	changed, err := p.ProcessAll()
	if err != nil {
		t.Fatalf("second ProcessAll: %v", err)
	}
	if changed {
		t.Error("second pass reported a change while backing off")
	}
	assertContent(t, skillMDPath, editedSinceUnpack)
	if got := strings.Count(logs.String(), "failed to rename extracted skill ZIP"); got != 1 {
		t.Errorf("rename failures logged within the backoff = %d, want 1:\n%s", got, logs.String())
	}

	if err := os.RemoveAll(obstruction); err != nil {
		t.Fatalf("remove obstruction: %v", err)
	}
	clock = clock.Add(time.Hour)
	if changed, err := p.ProcessAll(); err != nil || !changed {
		t.Fatalf("ProcessAll after the backoff = %v, %v; want the retry to unpack", changed, err)
	}
	if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
		t.Errorf("skill.zip still in place after the obstruction was removed, Stat = %v", err)
	}
	assertContent(t, filepath.Join(skillDir, skillruntime.BundleHashFile), digestRecord(buildSkillZIP(t, map[string][]byte{
		"SKILL.md": []byte("# Stuck Skill\n"),
	})))
	if changed, err := p.ProcessAll(); err != nil || changed {
		t.Errorf("ProcessAll once settled = %v, %v; want no change", changed, err)
	}
}

func TestSkillProcessor_ArchiveReplacedWhileUnpacking_IsKept(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "swapped-skill"
	zipPath := writeSkillZIP(t, skillsDir, skillName, buildSkillZIP(t, map[string][]byte{
		"SKILL.md": []byte("# v1\n"),
	}))
	skillDir := filepath.Dir(zipPath)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	opened, _, err := p.unpackArchiveAt(zipPath, skillDir)
	if err != nil {
		t.Fatalf("unpackArchiveAt: %v", err)
	}

	replacement := buildSkillZIP(t, map[string][]byte{"SKILL.md": []byte("# v2\n")})
	staged := zipPath + ".tmp"
	if err = os.WriteFile(staged, replacement, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err = os.Rename(staged, zipPath); err != nil {
		t.Fatalf("replace skill.zip: %v", err)
	}

	if p.keepUnpacked(zipPath, opened, skillName) {
		t.Error("keepUnpacked = true for an archive replaced while unpacking, want false")
	}
	current, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatalf("replacement archive no longer at skill.zip: %v", err)
	}
	if !bytes.Equal(current, replacement) {
		t.Error("skill.zip no longer holds the replacement archive")
	}
	if _, err := os.Stat(zipPath + ".extracted"); !os.IsNotExist(err) {
		t.Errorf("skill.zip.extracted exists after a swap, Stat = %v", err)
	}
}

func TestSkillProcessor_MarkerPresent_NoOp(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "already-extracted"

	skillDir := filepath.Join(skillsDir, skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	markerPath := filepath.Join(skillDir, "skill.zip.extracted")
	if err := os.WriteFile(markerPath, []byte("original zip bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile marker: %v", err)
	}

	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))
	changed, err := p.ProcessAll()
	if err != nil {
		t.Fatalf("ProcessAll: %v", err)
	}
	if changed {
		t.Error("expected changed=false when no skill.zip is present")
	}

	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("ReadFile marker: %v", err)
	}
	if string(data) != "original zip bytes" {
		t.Errorf("marker content changed unexpectedly: %q", string(data))
	}
}

func TestSkillProcessor_MalformedZIP_WritesErrorMarker(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "bad-skill"

	zipPath := writeSkillZIP(t, skillsDir, skillName, []byte("not a real zip"))

	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))
	changed, err := p.ProcessAll()
	if err != nil {
		t.Fatalf("ProcessAll returned unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false on malformed ZIP")
	}

	if _, statErr := os.Stat(zipPath); statErr != nil {
		t.Errorf("skill.zip should still exist after error: %v", statErr)
	}

	errorMarker := zipPath + ".extract-error"
	if _, statErr := os.Stat(errorMarker); statErr != nil {
		t.Errorf("extract-error marker not found: %v", statErr)
	}
}

func TestSkillProcessor_PathTraversal_Rejected(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "traversal-skill"

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, createErr := w.Create("../escape.txt")
	if createErr != nil {
		t.Fatalf("zip.Create: %v", createErr)
	}
	if _, writeErr := fw.Write([]byte("escaped")); writeErr != nil {
		t.Fatalf("zip write: %v", writeErr)
	}
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("zip.Close: %v", closeErr)
	}

	zipPath := writeSkillZIP(t, skillsDir, skillName, buf.Bytes())

	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))
	changed, err := p.ProcessAll()
	if err != nil {
		t.Fatalf("ProcessAll returned unexpected error: %v", err)
	}
	if changed {
		t.Error("expected changed=false on path traversal ZIP")
	}

	if _, statErr := os.Stat(zipPath); statErr != nil {
		t.Errorf("skill.zip should still exist after path traversal rejection: %v", statErr)
	}

	errorMarker := zipPath + ".extract-error"
	if _, statErr := os.Stat(errorMarker); statErr != nil {
		t.Errorf("extract-error marker not found after path traversal rejection: %v", statErr)
	}

	escapedPath := filepath.Join(skillsDir, "escape.txt")
	if _, statErr := os.Stat(escapedPath); !os.IsNotExist(statErr) {
		t.Errorf("path traversal was not blocked: escape.txt exists at %s", escapedPath)
	}
}

type skillZipFile struct {
	name    string
	content string
}

func buildOrderedSkillZIP(t *testing.T, entries ...skillZipFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		fw, err := w.Create(e.name)
		if err != nil {
			t.Fatalf("zip.Create(%s): %v", e.name, err)
		}
		if _, err := fw.Write([]byte(e.content)); err != nil {
			t.Fatalf("zip write(%s): %v", e.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

func processSkillZIP(t *testing.T, p *SkillProcessor, skillsDir, skillName string, zipData []byte) bool {
	t.Helper()
	writeSkillZIP(t, skillsDir, skillName, zipData)
	changed, err := p.ProcessAll()
	if err != nil {
		t.Fatalf("ProcessAll: %v", err)
	}
	return changed
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s must not exist, Lstat returned: %v", path, err)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s = %q, %v; want %q", path, got, err, want)
	}
}

func digestRecord(zipData []byte) string {
	sum := sha256.Sum256(zipData)
	return hex.EncodeToString(sum[:]) + "\n"
}

func TestSkillProcessor_ReExtract_RemovesFilesDroppedFromNewZIP(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"legacy.txt", "old\n"},
		skillZipFile{"scripts/run.sh", "old\n"},
		skillZipFile{"references/stale-page.md", "outdated\n"},
		skillZipFile{"references/kept-page.md", "v1\n"},
	))
	sidecars := []string{".skill.zip.tmp.x", "skill.zip.tmp", "skill.zip.sam-meta"}
	for _, name := range sidecars {
		if err := os.WriteFile(filepath.Join(skillDir, name), []byte("sidecar"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	v2 := buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v2\n"},
		skillZipFile{"references/kept-page.md", "v2\n"},
	)
	if !processSkillZIP(t, p, skillsDir, skillName, v2) {
		t.Error("expected changed=true for the re-extraction")
	}

	assertMissing(t, filepath.Join(skillDir, "legacy.txt"))
	assertMissing(t, filepath.Join(skillDir, "scripts"))
	assertMissing(t, filepath.Join(skillDir, "references", "stale-page.md"))
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
	assertContent(t, filepath.Join(skillDir, "references", "kept-page.md"), "v2\n")
	for _, name := range sidecars {
		assertContent(t, filepath.Join(skillDir, name), "sidecar")
	}
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))

	listed := skillruntime.ListResources(skillruntime.SkillContext{Name: skillName, LocalPath: skillDir})
	if listed.Count != 1 || listed.Files[0].Path != "references/kept-page.md" {
		t.Errorf("ListResources = %+v; want only references/kept-page.md", listed.Files)
	}
	assertContent(t, filepath.Join(skillDir, skillruntime.BundleHashFile), digestRecord(v2))
}

func TestSkillProcessor_ReExtract_ReplacesFilesWithDirectoriesAndBack(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"was-file", "file\n"},
		skillZipFile{"was-dir/inner.md", "inner\n"},
	))
	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v2\n"},
		skillZipFile{"was-file/inner.md", "now a dir\n"},
		skillZipFile{"was-dir", "now a file\n"},
	))

	assertContent(t, filepath.Join(skillDir, "was-file", "inner.md"), "now a dir\n")
	assertContent(t, filepath.Join(skillDir, "was-dir"), "now a file\n")
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
}

func TestSkillProcessor_ReExtract_IgnoresArchiveEntriesNamedLikeRuntimeFiles(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"references/a.md", "a\n"},
		skillZipFile{"scripts/x.sh", "x\n"},
	))

	changed := processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v2\n"},
		skillZipFile{".extract-staging/foo.md", "foo\n"},
		skillZipFile{".EXTRACT-STAGING/bar.md", "bar\n"},
		skillZipFile{".skill.zip.tmp.evil", "evil\n"},
	))

	if !changed {
		t.Error("expected changed=true: entries named like runtime files must be ignored, not fail the unpack")
	}
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
	assertMissing(t, filepath.Join(skillDir, "references"))
	assertMissing(t, filepath.Join(skillDir, "scripts"))
	assertMissing(t, filepath.Join(skillDir, ".skill.zip.tmp.evil"))
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))
	assertMissing(t, filepath.Join(skillDir, "skill.zip.extract-error"))
}

func TestSkillProcessor_ReExtract_ClearsStagingLeftByACrash(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t, skillZipFile{"SKILL.md", "# v1\n"}))
	for _, leftover := range []string{"new/junk.md", "old/SKILL.md"} {
		path := filepath.Join(skillDir, skillStagingDir, leftover)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("seed leftover staging: %v", err)
		}
		if err := os.WriteFile(path, []byte("junk"), 0o644); err != nil {
			t.Fatalf("seed leftover staging: %v", err)
		}
	}

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t, skillZipFile{"SKILL.md", "# v2\n"}))

	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
	assertMissing(t, filepath.Join(skillDir, "junk.md"))
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))
}

func TestSkillProcessor_ReExtract_KeepsFilesThatOnlyChangeCase(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"README.md", "v1\n"},
	))
	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v2\n"},
		skillZipFile{"Readme.md", "v2\n"},
	))

	assertContent(t, filepath.Join(skillDir, "Readme.md"), "v2\n")
	names := dirNames(t, skillDir)
	if !slices.Contains(names, "Readme.md") || slices.Contains(names, "README.md") {
		t.Errorf("skill dir entries = %v; want the new name Readme.md only", names)
	}
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
}

func TestSkillProcessor_ReExtract_IgnoresEntriesWithALeadingSlash(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"references/a.md", "a\n"},
	))
	v2 := buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v2\n"},
		skillZipFile{"/.extract-staging/x", "x\n"},
		skillZipFile{"/skill.zip", "not the archive\n"},
	)

	if !processSkillZIP(t, p, skillsDir, skillName, v2) {
		t.Error("expected changed=true: entries named like runtime files must be ignored, not fail the unpack")
	}

	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
	assertMissing(t, filepath.Join(skillDir, "references"))
	assertContent(t, filepath.Join(skillDir, "skill.zip.extracted"), string(v2))
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))
	assertMissing(t, filepath.Join(skillDir, "skill.zip.extract-error"))
}

func TestSkillProcessor_ReExtract_FailedZIPKeepsPreviousContent(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))

	v1 := buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"references/page-a.md", "a\n"},
	)
	processSkillZIP(t, p, skillsDir, skillName, v1)

	changed := processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v2\n"},
		skillZipFile{"references/new.md", "new\n"},
		skillZipFile{"../../escape.txt", "x\n"},
	))

	if changed {
		t.Error("expected changed=false when the new ZIP fails to extract")
	}
	assertMissing(t, filepath.Join(skillDir, skillruntime.BundleHashFile))
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v1\n")
	assertContent(t, filepath.Join(skillDir, "references", "page-a.md"), "a\n")
	assertMissing(t, filepath.Join(skillDir, "references", "new.md"))
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))
	if _, err := os.Stat(filepath.Join(skillDir, "skill.zip.extract-error")); err != nil {
		t.Errorf("a ZIP that cannot be extracted must be marked failed: %v", err)
	}
}

func TestSkillProcessor_ReExtract_UpdateFailureIsRetriedNotMarkedFailed(t *testing.T) {
	skipWithoutPermissionChecks(t)
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	p := NewSkillProcessor(skillsDir, captureLogger(new(bytes.Buffer)))
	clock := time.Now()
	p.now = func() time.Time { return clock }

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t, skillZipFile{"SKILL.md", "# v1\n"}))
	locked := filepath.Join(skillDir, skillStagingDir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("seed locked staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(locked, "x"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed locked staging: %v", err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	changed := processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t, skillZipFile{"SKILL.md", "# v2\n"}))

	if changed {
		t.Error("expected changed=false while the skill directory cannot be updated")
	}
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v1\n")
	assertMissing(t, filepath.Join(skillDir, "skill.zip.extract-error"))

	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	clock = clock.Add(time.Hour)
	if changed, err := p.ProcessAll(); err != nil || !changed {
		t.Fatalf("ProcessAll once the obstruction is gone = %v, %v; want the retry to succeed", changed, err)
	}
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
}

func TestSkillProcessor_ReExtract_FailedSwapIsRolledBackAndRetriedWithBackoff(t *testing.T) {
	skipWithoutPermissionChecks(t)
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	logs := new(bytes.Buffer)
	p := NewSkillProcessor(skillsDir, captureLogger(logs))
	clock := time.Now()
	p.now = func() time.Time { return clock }

	v1 := buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"references/a.md", "v1\n"},
		skillZipFile{"zscripts/run.sh", "v1\n"},
	)
	processSkillZIP(t, p, skillsDir, skillName, v1)
	scripts := filepath.Join(skillDir, "zscripts")
	if err := os.Chmod(scripts, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(scripts, 0o755) })

	v2 := buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v2\n"},
		skillZipFile{"references/a.md", "v2\n"},
		skillZipFile{"references/b.md", "v2\n"},
	)
	if processSkillZIP(t, p, skillsDir, skillName, v2) {
		t.Error("expected changed=false when the swap fails")
	}

	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v1\n")
	assertContent(t, filepath.Join(skillDir, "references", "a.md"), "v1\n")
	assertContent(t, filepath.Join(skillDir, "zscripts", "run.sh"), "v1\n")
	assertMissing(t, filepath.Join(skillDir, "references", "b.md"))
	assertContent(t, filepath.Join(skillDir, skillruntime.BundleHashFile), digestRecord(v1))
	assertMissing(t, filepath.Join(skillDir, "skill.zip.extract-error"))
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))

	if _, err := p.ProcessAll(); err != nil {
		t.Fatalf("ProcessAll while backing off: %v", err)
	}
	if got := strings.Count(logs.String(), "retrying later"); got != 1 {
		t.Errorf("attempts logged while backing off = %d, want 1", got)
	}

	if err := os.Chmod(scripts, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	clock = clock.Add(time.Hour)
	if changed, err := p.ProcessAll(); err != nil || !changed {
		t.Fatalf("ProcessAll after the backoff = %v, %v; want the retry to succeed", changed, err)
	}
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
	assertContent(t, filepath.Join(skillDir, "references", "b.md"), "v2\n")
	assertMissing(t, filepath.Join(skillDir, "zscripts"))
	assertContent(t, filepath.Join(skillDir, skillruntime.BundleHashFile), digestRecord(v2))
}

func TestReplaceSkillContents_ReportsEachFailedStep(t *testing.T) {
	write := func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, skillDir, incoming string)
	}{
		{"incoming directory missing", func(t *testing.T, skillDir, incoming string) {
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}},
		{"skill directory missing", func(t *testing.T, skillDir, incoming string) {
			write(t, filepath.Join(incoming, "SKILL.md"))
		}},
		{"skill directory missing with nothing incoming", func(t *testing.T, skillDir, incoming string) {
			if err := os.MkdirAll(incoming, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}},
		{"replaced directory missing for a replaced directory", func(t *testing.T, skillDir, incoming string) {
			write(t, filepath.Join(skillDir, "references", "old.md"))
			write(t, filepath.Join(incoming, "references", "new.md"))
		}},
		{"replaced directory missing for a dropped entry", func(t *testing.T, skillDir, incoming string) {
			write(t, filepath.Join(skillDir, "legacy.txt"))
			write(t, filepath.Join(incoming, "SKILL.md"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			skillDir := filepath.Join(root, "skill")
			incoming := filepath.Join(root, "incoming")
			tc.setup(t, skillDir, incoming)

			if err := replaceSkillContents(skillDir, incoming, filepath.Join(root, "missing-replaced")); err == nil {
				t.Error("replaceSkillContents succeeded; want the failed step reported")
			}
		})
	}
}

func skipWithoutPermissionChecks(t *testing.T) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.Mkdir(probe, 0o555); err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer func() { _ = os.Chmod(probe, 0o755) }()
	if err := os.WriteFile(filepath.Join(probe, "x"), nil, 0o644); err == nil {
		t.Skip("read-only directories accept writes here")
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

func lockDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestReplaceSkillContents_MovesTheDigestRecordAside(t *testing.T) {
	root := t.TempDir()
	skillDir, incoming, replaced := filepath.Join(root, "skill"), filepath.Join(root, "new"), filepath.Join(root, "old")
	writeTree(t, skillDir, map[string]string{"SKILL.md": "old", skillruntime.BundleHashFile: "old digest"})
	writeTree(t, incoming, map[string]string{"SKILL.md": "new"})
	if err := os.Mkdir(replaced, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := replaceSkillContents(skillDir, incoming, replaced); err != nil {
		t.Fatalf("replaceSkillContents: %v", err)
	}

	assertMissing(t, filepath.Join(skillDir, skillruntime.BundleHashFile))
	assertContent(t, filepath.Join(replaced, skillruntime.BundleHashFile), "old digest")
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "new")
}

func TestReplaceSkillContents_SKILLmdIsNeverAbsent(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill")
	tree := map[string]string{"SKILL.md": "skill", "references/a.md": "a", "assets/b.md": "b", "notes.md": "n"}
	writeTree(t, skillDir, tree)

	stop := make(chan struct{})
	var misses atomic.Int64
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md")); err != nil {
				misses.Add(1)
			}
		}
	})
	stopReader := sync.OnceFunc(func() {
		close(stop)
		wg.Wait()
	})
	defer stopReader()
	for i := range 50 {
		incoming, replaced := filepath.Join(root, fmt.Sprintf("new-%d", i)), filepath.Join(root, fmt.Sprintf("old-%d", i))
		writeTree(t, incoming, tree)
		if err := os.Mkdir(replaced, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := replaceSkillContents(skillDir, incoming, replaced); err != nil {
			t.Fatalf("replaceSkillContents: %v", err)
		}
	}
	stopReader()

	if got := misses.Load(); got != 0 {
		t.Errorf("SKILL.md was absent for %d reads during the swaps, want 0", got)
	}
}

func TestReplaceSkillContents_FailedPlacementRestoresEverything(t *testing.T) {
	skipWithoutPermissionChecks(t)
	root := t.TempDir()
	skillDir, incoming, replaced := filepath.Join(root, "skill"), filepath.Join(root, "new"), filepath.Join(root, "old")
	writeTree(t, skillDir, map[string]string{
		"SKILL.md": "old", "references/a.md": "old", skillruntime.BundleHashFile: "old digest",
	})
	writeTree(t, incoming, map[string]string{"SKILL.md": "new", "new.md": "new", "zlocked/x": "x"})
	lockDir(t, filepath.Join(incoming, "zlocked"))
	if err := os.Mkdir(replaced, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := replaceSkillContents(skillDir, incoming, replaced)

	if err == nil || errors.Is(err, errSkillDirDamaged) {
		t.Fatalf("replaceSkillContents = %v; want a failure that was undone", err)
	}
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "old")
	assertContent(t, filepath.Join(skillDir, "references", "a.md"), "old")
	assertContent(t, filepath.Join(skillDir, skillruntime.BundleHashFile), "old digest")
	assertMissing(t, filepath.Join(skillDir, "new.md"))
	assertMissing(t, filepath.Join(skillDir, "zlocked"))
}

func TestReplaceSkillContents_FailedUndoReportsADamagedSkill(t *testing.T) {
	skipWithoutPermissionChecks(t)
	root := t.TempDir()
	skillDir, incoming, replaced := filepath.Join(root, "skill"), filepath.Join(root, "new"), filepath.Join(root, "old")
	writeTree(t, skillDir, map[string]string{"SKILL.md": "old", "a/old.md": "old"})
	writeTree(t, incoming, map[string]string{"SKILL.md": "new", "a/sub/n.md": "new", "z/x": "x"})
	lockDir(t, filepath.Join(incoming, "a", "sub"))
	lockDir(t, filepath.Join(incoming, "z"))
	if err := os.Mkdir(replaced, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(skillDir, "a", "sub"), 0o755) })

	err := replaceSkillContents(skillDir, incoming, replaced)

	if !errors.Is(err, errSkillDirDamaged) {
		t.Fatalf("replaceSkillContents = %v; want errSkillDirDamaged", err)
	}
	assertContent(t, filepath.Join(replaced, "a", "old.md"), "old")
}

func TestSkillProcessor_ReExtract_DamagedSwapDropsTheDigestAndConverges(t *testing.T) {
	skillsDir := t.TempDir()
	skillName := "docs-skill"
	skillDir := filepath.Join(skillsDir, skillName)
	logs := new(bytes.Buffer)
	p := NewSkillProcessor(skillsDir, captureLogger(logs))
	clock := time.Now()
	p.now = func() time.Time { return clock }

	processSkillZIP(t, p, skillsDir, skillName, buildOrderedSkillZIP(t,
		skillZipFile{"SKILL.md", "# v1\n"},
		skillZipFile{"references/old.md", "v1\n"},
	))
	p.swap = func(skillDir, incoming, replaced string) error {
		if err := replaceSkillContents(skillDir, incoming, replaced); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(replaced, skillruntime.BundleHashFile), filepath.Join(skillDir, skillruntime.BundleHashFile)); err != nil {
			return err
		}
		return fmt.Errorf("%w: injected after restoring only the old digest", errSkillDirDamaged)
	}

	v2 := buildOrderedSkillZIP(t, skillZipFile{"SKILL.md", "# v2\n"}, skillZipFile{"references/new.md", "v2\n"})
	if processSkillZIP(t, p, skillsDir, skillName, v2) {
		t.Error("expected changed=false when the swap leaves the skill damaged")
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "skill directory left partially updated") {
		t.Errorf("a damaged skill must be logged at ERROR; logs:\n%s", logs.String())
	}
	assertMissing(t, filepath.Join(skillDir, skillruntime.BundleHashFile))
	assertMissing(t, filepath.Join(skillDir, "skill.zip.extract-error"))
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))

	p.swap = replaceSkillContents
	clock = clock.Add(time.Hour)
	if changed, err := p.ProcessAll(); err != nil || !changed {
		t.Fatalf("ProcessAll after the backoff = %v, %v; want the retry to succeed", changed, err)
	}
	assertContent(t, filepath.Join(skillDir, "SKILL.md"), "# v2\n")
	assertContent(t, filepath.Join(skillDir, "references", "new.md"), "v2\n")
	assertMissing(t, filepath.Join(skillDir, "references", "old.md"))
	assertContent(t, filepath.Join(skillDir, skillruntime.BundleHashFile), digestRecord(v2))
	assertMissing(t, filepath.Join(skillDir, skillStagingDir))
}

func TestSkillProcessor_BackoffGrowsToTheCapAndResetsWhenTheArchiveChanges(t *testing.T) {
	p := NewSkillProcessor(t.TempDir(), captureLogger(new(bytes.Buffer)))
	clock := time.Now()
	p.now = func() time.Time { return clock }
	archive := fakeArchiveInfo{modTime: clock, size: 10}

	var waits []time.Duration
	for range 40 {
		p.recordSwapFailure("s", archive)
		waits = append(waits, p.retries["s"].notBefore.Sub(clock))
	}
	if waits[0] != swapRetryBase {
		t.Errorf("first wait = %v, want %v", waits[0], swapRetryBase)
	}
	if !slices.IsSorted(waits) || waits[len(waits)-1] != swapRetryMax {
		t.Errorf("waits = %v; want non-decreasing, ending at %v", waits, swapRetryMax)
	}
	if !p.waitingToRetry("s", archive) {
		t.Error("expected the skill to wait before the next attempt")
	}
	if p.waitingToRetry("s", fakeArchiveInfo{modTime: clock.Add(time.Second), size: 10}) {
		t.Error("a rewritten archive must be tried without waiting")
	}
}

type fakeArchiveInfo struct {
	os.FileInfo
	modTime time.Time
	size    int64
}

func (f fakeArchiveInfo) ModTime() time.Time { return f.modTime }
func (f fakeArchiveInfo) Size() int64        { return f.size }
