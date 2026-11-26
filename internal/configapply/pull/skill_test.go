// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"archive/zip"
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestSynthesizeSkillMD(t *testing.T) {
	got := synthesizeSkillMD("support", "Customer-support skill")
	if !strings.HasPrefix(got, "---\nname: support\n") {
		t.Errorf("missing leading frontmatter:\n%s", got)
	}
	if !strings.Contains(got, "description: Customer-support skill\n") {
		t.Errorf("missing description: %s", got)
	}
	if !strings.HasSuffix(got, "---\n\n# support\n") {
		t.Errorf("missing closing frontmatter + heading placeholder: %s", got)
	}
}

func TestSynthesizeSkillMD_NoDescription(t *testing.T) {
	got := synthesizeSkillMD("bare", "")
	if strings.Contains(got, "description:") {
		t.Errorf("empty description should be omitted: %s", got)
	}
	if !strings.Contains(got, "---\nname: bare\n---") {
		t.Errorf("expected name-only frontmatter: %s", got)
	}
}

func TestHasYAMLFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "---\nname: x\n---\n", true},
		{"crlf", "---\r\nname: x\r\n---\r\n", true},
		{"with bom", "\xef\xbb\xbf---\nname: x\n---\n", true},
		{"no header", "# heading\n\nbody\n", false},
		{"three dashes mid body", "name: x\n---\nfoo\n", false},
		{"empty", "", false},
		{"just dashes no newline", "---", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasYAMLFrontmatter([]byte(tc.in)); got != tc.want {
				t.Errorf("hasYAMLFrontmatter(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestYamlScalar(t *testing.T) {
	cases := map[string]string{
		"simple text":      "simple text",
		"":                 `""`,
		"with: colon":      `"with: colon"`,
		"has \"quotes\"":   `"has \"quotes\""`,
		"line1\nline2":     `"line1\nline2"`,
		"- leading dash":   `"- leading dash"`,
		"{leading brace":   `"{leading brace"`,
		"# leading hash":   `"# leading hash"`,
		"trailing colon: ": `"trailing colon: "`,
	}
	for in, want := range cases {
		if got := yamlScalar(in); got != want {
			t.Errorf("yamlScalar(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestSkillMDFrontmatter(t *testing.T) {
	got := skillMDFrontmatter("a", "b: c")
	if got != "---\nname: a\ndescription: \"b: c\"\n---\n\n" {
		t.Errorf("unexpected: %q", got)
	}
}

func TestEnsureSkillMDFrontmatter_AlreadyPresent(t *testing.T) {
	original := []byte("---\nname: a\ndescription: x\n---\n# heading\n")
	entries := []fileEntry{{
		relPath: filepath.Join("skills", "a", skillMDFilename),
		content: original,
	}}
	out := ensureSkillMDFrontmatter(entries, "a", "a", "platform-side description")
	if len(out) != 1 {
		t.Fatalf("len = %d; want 1", len(out))
	}
	if !bytes.Equal(out[0].content, original) {
		t.Errorf("existing frontmatter should be preserved verbatim, got %s", out[0].content)
	}
}

func TestEnsureSkillMDFrontmatter_Prepended(t *testing.T) {
	entries := []fileEntry{{
		relPath: filepath.Join("skills", "a", skillMDFilename),
		content: []byte("# heading\n\nbody\n"),
	}}
	out := ensureSkillMDFrontmatter(entries, "a", "a", "synthesized desc")
	s := string(out[0].content)
	if !strings.HasPrefix(s, "---\nname: a\ndescription: synthesized desc\n---\n\n") {
		t.Errorf("expected synthesized frontmatter prepended, got: %s", s)
	}
	if !strings.HasSuffix(s, "# heading\n\nbody\n") {
		t.Errorf("expected original body preserved, got: %s", s)
	}
}

func TestEnsureSkillMDFrontmatter_MissingSKILLMDIsSynthesized(t *testing.T) {
	out := ensureSkillMDFrontmatter(nil, "a", "a", "from-platform")
	if len(out) != 1 {
		t.Fatalf("len = %d; want 1", len(out))
	}
	if out[0].relPath != filepath.Join("skills", "a", skillMDFilename) {
		t.Errorf("path = %s", out[0].relPath)
	}
	if !strings.Contains(string(out[0].content), "name: a") {
		t.Errorf("content missing name: %s", out[0].content)
	}
}

func TestUnpackSkillZIP_FlatLayout(t *testing.T) {
	zipBytes := buildZIP(t, map[string]string{
		"SKILL.md":          "---\nname: s\n---\n",
		"references/foo.md": "ref",
	})
	out, err := unpackSkillZIP("s", zipBytes)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d; want 2", len(out))
	}
	paths := map[string]bool{}
	for _, e := range out {
		paths[e.relPath] = true
	}
	for _, want := range []string{filepath.Join("skills", "s", "SKILL.md"), filepath.Join("skills", "s", "references", "foo.md")} {
		if !paths[want] {
			t.Errorf("missing %s; got %v", want, paths)
		}
	}
}

func TestUnpackSkillZIP_NestedLayoutStripped(t *testing.T) {
	zipBytes := buildZIP(t, map[string]string{
		"s/SKILL.md":          "---\nname: s\n---\n",
		"s/references/foo.md": "ref",
	})
	out, err := unpackSkillZIP("s", zipBytes)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	for _, e := range out {
		if strings.HasPrefix(e.relPath, filepath.Join("skills", "s", "s")) {
			t.Errorf("nested top-level dir not stripped: %s", e.relPath)
		}
	}
}

func TestUnpackSkillZIP_RejectsTraversal(t *testing.T) {
	zipBytes := buildZIP(t, map[string]string{
		"../etc/passwd": "danger",
	})
	if _, err := unpackSkillZIP("s", zipBytes); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("expected unsafe-path error, got %v", err)
	}
}

func TestUnpackSkillZIP_RejectsEmpty(t *testing.T) {
	if _, err := unpackSkillZIP("s", nil); err == nil || !strings.Contains(err.Error(), "empty zip") {
		t.Fatalf("expected empty-zip error, got %v", err)
	}
}

func TestUnpackSkillZIP_RejectsCorrupt(t *testing.T) {
	if _, err := unpackSkillZIP("s", []byte("not a zip")); err == nil {
		t.Fatal("expected corrupt-zip error, got nil")
	}
}

func buildZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

const redactedWebSkillMD = `---
name: web
description: Search the web
tools:
  - tool_type: builtin
    tool_name: web_search_google
    tool_config:
      google_search_api_key: <REDACTED>
      google_cse_id: e11bfake0000
---
# Web

Body text.
`

const cleanWebSkillMD = "---\nname:   web\ndescription: clean\ntools:\n    - tool_name: t\n      tool_config:\n          api_key: ${T_KEY}\n---\nbody\n"

func skillPullMock(t *testing.T, skillMD string) *reconcile.Client {
	t.Helper()
	return skillPullMockBundle(t, map[string]string{"SKILL.md": skillMD})
}

func skillPullMockBundle(t *testing.T, files map[string]string) *reconcile.Client {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipBytes := buf.Bytes()
	mux := skillsMux(t, wire.SkillResponse{ID: "skill-1", Name: "web", Description: new("Search the web")})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills/skill-1/bundle", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes)
	})
	return newV2PlatformClient(t, mux)
}

func TestPullSkills_RedactedValuesBecomePlaceholders(t *testing.T) {
	c := skillPullMock(t, redactedWebSkillMD)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	entries, names, err := pullSkills(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullSkills: %v", err)
	}
	if len(names) != 1 || len(entries) != 1 {
		t.Fatalf("names=%v entries=%d", names, len(entries))
	}
	got := string(entries[0].content)
	const placeholder = "${SKILL_WEB_WEB_SEARCH_GOOGLE_GOOGLE_SEARCH_API_KEY}"
	if !strings.Contains(got, placeholder) || strings.Contains(got, "<REDACTED>") {
		t.Fatalf("SKILL.md should carry the placeholder, not the sentinel:\n%s", got)
	}
	if !strings.HasSuffix(got, "# Web\n\nBody text.\n") {
		t.Fatalf("body changed:\n%s", got)
	}
	want := SecretWarning{
		Kind:         "skill",
		ResourceName: "web",
		FieldPath:    "tools[web_search_google].tool_config.google_search_api_key",
		Placeholder:  placeholder,
	}
	if len(res.SecretPlaceholders) != 1 || res.SecretPlaceholders[0] != want {
		t.Fatalf("SecretPlaceholders = %+v, want [%+v]", res.SecretPlaceholders, want)
	}
}

func TestPullSkills_CleanSkillWrittenByteIdentically(t *testing.T) {
	c := skillPullMock(t, cleanWebSkillMD)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	entries, _, err := pullSkills(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullSkills: %v", err)
	}
	if len(entries) != 1 || string(entries[0].content) != cleanWebSkillMD {
		t.Fatalf("clean SKILL.md changed: %+v", entries)
	}
	if len(res.SecretPlaceholders) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("unexpected placeholders/warnings: %+v %v", res.SecretPlaceholders, res.Warnings)
	}
}

func TestPullSkills_UnparseableFrontMatterKeptWithWarning(t *testing.T) {
	raw := "---\nname: [unclosed\ntools: <REDACTED>\n---\nbody\n"
	c := skillPullMock(t, raw)
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	entries, _, err := pullSkills(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullSkills: %v", err)
	}
	if len(entries) != 1 || string(entries[0].content) != raw {
		t.Fatalf("bundle must be kept as downloaded: %+v", entries)
	}
	if len(res.Warnings) != 1 || len(res.SecretPlaceholders) != 0 {
		t.Fatalf("warnings=%v placeholders=%v", res.Warnings, res.SecretPlaceholders)
	}
}

func TestPullSkills_EverySkillMDGetsPlaceholdersWithDistinctNames(t *testing.T) {
	nested := strings.Replace(redactedWebSkillMD, "# Web", "# Nested", 1)
	c := skillPullMockBundle(t, map[string]string{
		"SKILL.md":            redactedWebSkillMD,
		"references/SKILL.md": nested,
	})
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	entries, _, err := pullSkills(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullSkills: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(string(e.content), "<REDACTED>") {
			t.Errorf("%s still carries the sentinel:\n%s", e.relPath, e.content)
		}
	}
	if len(res.SecretPlaceholders) != 2 {
		t.Fatalf("SecretPlaceholders = %+v", res.SecretPlaceholders)
	}
	paths := []string{res.SecretPlaceholders[0].FieldPath, res.SecretPlaceholders[1].FieldPath}
	slices.Sort(paths)
	wantPaths := []string{
		"references/SKILL.md:tools[web_search_google].tool_config.google_search_api_key",
		"tools[web_search_google].tool_config.google_search_api_key",
	}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("field paths = %v, want %v", paths, wantPaths)
	}
	if res.SecretPlaceholders[0].Placeholder == res.SecretPlaceholders[1].Placeholder {
		t.Fatalf("placeholder names collide: %+v", res.SecretPlaceholders)
	}
}

func TestWipeContents_KeepsPreservedDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"skills/web/SKILL.md", "skills/other/SKILL.md", "models/m.yaml", "manifest.yaml"} {
		full := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := wipeContents(dir, []string{filepath.Join("skills", "web")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "web", "SKILL.md")); err != nil {
		t.Errorf("preserved skill was removed: %v", err)
	}
	for _, gone := range []string{"skills/other", "models", "manifest.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s should have been wiped", gone)
		}
	}
}

func TestPullSkills_LowercaseSkillMDIsTheManifest(t *testing.T) {
	zipBytes := buildZIP(t, map[string]string{"skill.md": "# web\n"})
	srv := skillDownloadFailureServer(t, http.StatusOK, string(zipBytes))
	c := reconcile.NewClient(target.Resolved{URL: srv.URL})

	bundle, res, err := fetchAndTranslate(t.Context(), c, Options{OnlyKind: "skill"})
	if err != nil {
		t.Fatalf("fetchAndTranslate: %v (lossy %v)", err, res.lossy)
	}
	var skillFiles []string
	for _, e := range bundle {
		if strings.HasPrefix(e.relPath, filepath.Join("skills", "web")) {
			skillFiles = append(skillFiles, e.relPath)
		}
	}
	if want := []string{filepath.Join("skills", "web", skillMDFilename)}; !slices.Equal(skillFiles, want) {
		t.Errorf("skill files = %v; want %v", skillFiles, want)
	}
}

func TestRun_LowercaseSkillMDPullsAsResolvableSkillMD(t *testing.T) {
	zipBytes := buildZIP(t, map[string]string{"skill.md": "# web\n"})
	srv := skillDownloadFailureServer(t, http.StatusOK, string(zipBytes))
	out := filepath.Join(t.TempDir(), "repo")

	res, err := Run(t.Context(), target.Target{URL: srv.URL}, Options{OutputDir: out, OnlyKind: "skill"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dirEntries, err := os.ReadDir(filepath.Join(out, "skills", "web"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, d := range dirEntries {
		onDisk = append(onDisk, d.Name())
	}
	if want := []string{skillMDFilename}; !slices.Equal(onDisk, want) {
		t.Errorf("skills/web/ holds %v; want %v", onDisk, want)
	}

	m, err := manifest.Load(res.ManifestPath)
	if err != nil {
		t.Fatalf("load pulled manifest: %v", err)
	}
	skills, err := resolve.ResolveSkills(out, m)
	if err != nil {
		t.Fatalf("resolve pulled skill: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "web" {
		t.Errorf("resolved skills = %+v; want one skill named web", skills)
	}
}

func TestUnpackSkillZIP_RejectsFilesDifferingOnlyInCase(t *testing.T) {
	zipBytes := buildZIP(t, map[string]string{"SKILL.md": "x", "Notes.md": "a", "notes.md": "b"})
	if _, err := unpackSkillZIP("s", zipBytes); err == nil || !strings.Contains(err.Error(), "map to the same file") {
		t.Fatalf("expected a case-collision error, got %v", err)
	}
}
