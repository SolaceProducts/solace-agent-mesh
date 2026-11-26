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
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

func skillArchive(t *testing.T, name, body string, extra map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{"SKILL.md": "---\nname: " + name + "\ndescription: a test skill\n---\n\n" + body + "\n"}
	for k, v := range extra {
		files[k] = v
	}
	for path, content := range files {
		w, err := zw.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func mirror(t *testing.T, skillsDir, name string, archive []byte) {
	t.Helper()
	dir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.zip"), archive, 0o644); err != nil {
		t.Fatal(err)
	}
}

func servedHash(t *testing.T, skillsDir, name string) any {
	t.Helper()
	if _, err := NewSkillProcessor(skillsDir, slog.Default()).ProcessAll(); err != nil {
		t.Fatal(err)
	}
	reg := NewSkillRegistry(skillsDir)
	if err := reg.Load(); err != nil {
		t.Fatal(err)
	}
	w := &Worker{logger: slog.Default(), skillRegistry: reg}
	res, invokeErr := w.executeBuiltinSkillTool(t.Context(),
		&remotetool.InvokeParams{ToolName: "load_skill", Args: map[string]any{"skill_name": name}}, nil)
	if invokeErr != nil || res == nil {
		t.Fatalf("load %s: %v", name, invokeErr)
	}
	return res.ToolResult["content_hash"]
}

func TestSkillArchive_LoadReturnsTheDigestOfTheArchiveItServed(t *testing.T) {
	skillsDir := t.TempDir()
	v1, v1Digest := skillArchive(t, "uploaded", "version one", nil)
	mirror(t, skillsDir, "uploaded", v1)
	if got := servedHash(t, skillsDir, "uploaded"); got != v1Digest {
		t.Errorf("an unpacked upload's content_hash = %v, want the archive's digest %s", got, v1Digest)
	}
	v2, v2Digest := skillArchive(t, "uploaded", "version two", nil)
	mirror(t, skillsDir, "uploaded", v2)
	if got := servedHash(t, skillsDir, "uploaded"); got != v2Digest {
		t.Errorf("after a re-upload content_hash = %v, want the new archive's digest %s", got, v2Digest)
	}

	authored := filepath.Join(skillsDir, "authored")
	if err := os.MkdirAll(authored, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: authored\ndescription: a test skill\n---\n\nAuthored as a directory.\n"
	if err := os.WriteFile(filepath.Join(authored, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := servedHash(t, skillsDir, "authored"); got != nil {
		t.Errorf("a skill authored as a directory reported content_hash %v", got)
	}
}

func TestSkillArchive_CannotNameItsOwnDigest(t *testing.T) {
	skillsDir := t.TempDir()
	spoof := "0000000000000000000000000000000000000000000000000000000000000000"
	archive, digest := skillArchive(t, "nested", "with an archive inside", map[string]string{
		"skill.zip":                 "an inner archive",
		"skill.zip.extracted":       "another inner archive",
		skillruntime.BundleHashFile: spoof,
		"SKILL.ZIP":                 "an inner archive in another case",
		"Skill.Zip.Sha256":          spoof,
	})
	mirror(t, skillsDir, "nested", archive)
	if got := servedHash(t, skillsDir, "nested"); got != digest {
		t.Errorf("content_hash = %v, want the outer archive's digest %s", got, digest)
	}
	kept, err := os.ReadFile(filepath.Join(skillsDir, "nested", "skill.zip.extracted"))
	if err != nil || !bytes.Equal(kept, archive) {
		t.Errorf("the kept archive is not the one uploaded (%v)", err)
	}
	for _, name := range []string{"skill.zip", "SKILL.ZIP"} {
		if _, err := os.Stat(filepath.Join(skillsDir, "nested", name)); !os.IsNotExist(err) {
			t.Errorf("an inner %s was unpacked (%v), which the next pass would take for a new upload", name, err)
		}
	}
}

func TestSkillArchive_FailedReuploadLeavesNoDigest(t *testing.T) {
	skillsDir := t.TempDir()
	v1, v1Digest := skillArchive(t, "broken", "version one", nil)
	mirror(t, skillsDir, "broken", v1)
	if got := servedHash(t, skillsDir, "broken"); got != v1Digest {
		t.Fatalf("v1 content_hash = %v, want %s", got, v1Digest)
	}
	v2, _ := skillArchive(t, "broken", "version two", map[string]string{"../escape": "outside the skill"})
	mirror(t, skillsDir, "broken", v2)
	if got := servedHash(t, skillsDir, "broken"); got != nil {
		t.Errorf("after a re-upload that failed to unpack content_hash = %v, want none", got)
	}
}

func TestSkillArchive_UnpackedBeforeDigestsHasNone(t *testing.T) {
	skillsDir := t.TempDir()
	archive, _ := skillArchive(t, "legacy", "unpacked earlier", nil)
	mirror(t, skillsDir, "legacy", archive)
	if _, err := NewSkillProcessor(skillsDir, slog.Default()).ProcessAll(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(skillsDir, "legacy", skillruntime.BundleHashFile)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := servedHash(t, skillsDir, "legacy"); got != nil {
			t.Errorf("content_hash = %v, want none for a skill unpacked before digests", got)
		}
	}
}

func TestSkillArchive_UnreadableIsReportedOnce(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mode bits that deny reading; Chmod only sets the read-only attribute")
	}
	skillsDir := t.TempDir()
	archive, _ := skillArchive(t, "locked", "unreadable", nil)
	mirror(t, skillsDir, "locked", archive)
	zipPath := filepath.Join(skillsDir, "locked", "skill.zip")
	if err := os.Chmod(zipPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(zipPath, 0o644) })
	var logs bytes.Buffer
	p := NewSkillProcessor(skillsDir, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	for range 3 {
		if _, err := p.ProcessAll(); err != nil {
			t.Fatal(err)
		}
	}
	if n := bytes.Count(logs.Bytes(), []byte("skill ZIP extraction failed")); n != 1 {
		t.Errorf("an unreadable archive was reported %d times over three passes, want once: %s", n, logs.String())
	}
}
