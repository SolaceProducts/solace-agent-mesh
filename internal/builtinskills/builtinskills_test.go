// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builtinskills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func TestNamesIncludesConciergeDeps(t *testing.T) {
	got := Names()
	for _, want := range []string{"sam-knowledge", "sam-docs", "sam-build-redirect", "sam-test-harness", "sam-build-apply"} {
		if !slices.Contains(got, want) {
			t.Fatalf("Names() missing %q (got %v)", want, got)
		}
	}
}

func TestBundledExecutableMode(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"SKILL.md", false},
		{"references/foo.md", false},
		{"tools/manifest.yaml", false},
		{"tools/test_harness/test_harness", true},
		{"tools/test_harness/assets/data.json", true},
	}
	for _, tc := range cases {
		if got := IsBundledExecutable(tc.rel); got != tc.want {
			t.Errorf("IsBundledExecutable(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

func TestSKILLMDNameMatchesDirName(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			data, err := fs.ReadFile(fsys, "files/"+name+"/SKILL.md")
			if err != nil {
				t.Fatalf("read SKILL.md: %v", err)
			}
			declared := skillMDFrontmatterName(string(data))
			if declared == "" {
				t.Fatalf("SKILL.md has no `name:` field in frontmatter")
			}
			if declared != name {
				t.Errorf("SKILL.md frontmatter `name: %s` does not match directory name %q — load_skill(%q) will return 'not found' because SkillRegistry keys entries by frontmatter name",
					declared, name, name)
			}
		})
	}
}

func TestTestHarnessSkillCarriesWorkflowInputContract(t *testing.T) {
	data, err := fs.ReadFile(fsys, "files/sam-test-harness/SKILL.md")
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	doc := strings.Join(strings.Fields(string(data)), " ")
	for _, want := range []string{
		"When the target is a workflow, also pass `inputSchema`",
		"The tool uses it to produce a `sampleInput` the workflow accepts as written",
		"every property except `input_artifact`, all listed under `required`",
		"Rebuild it from the tool's current definition each time",
		"Don't paraphrase",
		"never rename, remap, drop, or add fields",
		"the `sampleInput` you actually invoked the component with",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("sam-test-harness SKILL.md lost %q", want)
		}
	}
}

func skillMDFrontmatterName(content string) string {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return ""
	}
	rest := strings.TrimPrefix(content, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "name:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func TestHasName(t *testing.T) {
	if !HasName("sam-knowledge") {
		t.Error("HasName(sam-knowledge) = false, want true")
	}
	if HasName("not-a-builtin") {
		t.Error("HasName(not-a-builtin) = true, want false")
	}
}

func TestBuildZipDeterministic(t *testing.T) {
	for _, name := range Names() {
		first, err := BuildZip(name)
		if err != nil {
			t.Fatalf("BuildZip(%s): %v", name, err)
		}
		second, err := BuildZip(name)
		if err != nil {
			t.Fatalf("BuildZip(%s) second: %v", name, err)
		}
		if !bytes.Equal(first, second) {
			t.Errorf("BuildZip(%s) is non-deterministic — same content produced different bytes", name)
		}
		h1 := sha256.Sum256(first)
		h2 := sha256.Sum256(second)
		if h1 != h2 {
			t.Errorf("BuildZip(%s) hash differs across calls: %x vs %x", name, h1, h2)
		}
	}
}

func TestBuildZipContainsSkillMD(t *testing.T) {
	for _, name := range Names() {
		data, err := BuildZip(name)
		if err != nil {
			t.Fatalf("BuildZip(%s): %v", name, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("open zip for %s: %v", name, err)
		}
		var found bool
		for _, f := range zr.File {
			if f.Name == "SKILL.md" {
				found = true
				rc, openErr := f.Open()
				if openErr != nil {
					t.Fatalf("open SKILL.md in %s: %v", name, openErr)
				}
				body, _ := io.ReadAll(rc)
				_ = rc.Close()
				if len(body) == 0 {
					t.Errorf("SKILL.md in %s is empty", name)
				}
			}
		}
		if !found {
			t.Errorf("BuildZip(%s) ZIP missing SKILL.md", name)
		}
	}
}

func TestHashZipMatchesSHA256(t *testing.T) {
	for _, name := range Names() {
		zipBytes, hashHex, err := HashZip(name)
		if err != nil {
			t.Fatalf("HashZip(%s): %v", name, err)
		}
		want := sha256.Sum256(zipBytes)
		if got := hex.EncodeToString(want[:]); got != hashHex {
			t.Errorf("HashZip(%s) hash %s != %s", name, hashHex, got)
		}
	}
}

func TestBuildZipExcludesBundledExecutable(t *testing.T) {
	for _, name := range Names() {
		data, err := BuildZip(name)
		if err != nil {
			t.Fatalf("BuildZip(%s): %v", name, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("open zip for %s: %v", name, err)
		}
		for _, f := range zr.File {
			if strings.HasSuffix(f.Name, "/") {
				continue
			}
			if IsBundledExecutable(f.Name) {
				t.Errorf("BuildZip(%s) ZIP includes bundled executable %q — object store must ship metadata only", name, f.Name)
			}
		}
	}

	for _, name := range toolBearingSkills(t) {
		data, err := BuildZip(name)
		if err != nil {
			t.Fatalf("BuildZip(%s): %v", name, err)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("open zip for %s: %v", name, err)
		}
		want := map[string]bool{"SKILL.md": false, "tools/manifest.yaml": false}
		for _, f := range zr.File {
			if _, ok := want[f.Name]; ok {
				want[f.Name] = true
			}
		}
		for entryName, found := range want {
			if !found {
				t.Errorf("BuildZip(%s) ZIP missing metadata entry %q", name, entryName)
			}
		}
	}
}

func TestBuildZipUnknownSkill(t *testing.T) {
	if _, err := BuildZip("does-not-exist"); err == nil {
		t.Error("BuildZip(does-not-exist) returned nil error")
	}
}

func toolBearingSkills(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, name := range Names() {
		if _, err := fs.Stat(FS(), name+"/tools/manifest.yaml"); err == nil {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		t.Fatal("no tool-bearing built-in skills found — the detection is broken, not the tree")
	}
	return out
}

func TestFSExposesEverySkillAtRoot(t *testing.T) {
	bfs := FS()
	for _, name := range Names() {
		if _, err := fs.Stat(bfs, name+"/SKILL.md"); err != nil {
			t.Errorf("FS() missing %s/SKILL.md: %v", name, err)
		}
	}
}
