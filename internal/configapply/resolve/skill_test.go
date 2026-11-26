// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package resolve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/sources"
)

func TestResolveSkills_FrontmatterOnly(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/support/SKILL.md":     "---\nname: support\ndescription: Customer support\n---\n# Support\n",
		"skills/support/references/x": "extra content",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "support"}},
	}}
	got, err := ResolveSkills(root, m)
	if err != nil {
		t.Fatalf("ResolveSkills: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources; want 1", len(got))
	}
	if got[0].Name != "support" || got[0].Description != "Customer support" {
		t.Errorf("identity = %s / %q", got[0].Name, got[0].Description)
	}
	if got[0].BundleDir == "" || !strings.HasSuffix(got[0].BundleDir, "skills/support") {
		t.Errorf("BundleDir = %q", got[0].BundleDir)
	}
	if !strings.HasSuffix(got[0].SourceFile, "skills/support/SKILL.md") {
		t.Errorf("SourceFile should point at SKILL.md, got %q", got[0].SourceFile)
	}
}

func TestResolveSkills_NoEntries(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"manifests/m.yaml": "kind: manifest\nname: m\ntarget:\n  url: x\n",
	})
	got, err := ResolveSkills(root, &manifest.Manifest{})
	if err != nil {
		t.Fatalf("ResolveSkills: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 resources, got %d", len(got))
	}
}

func TestResolveSkills_MissingBundleDirectory(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/other/SKILL.md": "---\nname: other\ndescription: x\n---\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "lonely"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "no bundle directory") {
		t.Fatalf("expected bundle-dir error, got %v", err)
	}
}

func TestResolveSkills_MissingSKILLMD(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/naked/.gitkeep": "",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "naked"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "missing SKILL.md") {
		t.Fatalf("expected SKILL.md error, got %v", err)
	}
}

func TestResolveSkills_LegacyYAMLHeaderRejected(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/legacy.yaml":     "kind: skill\nname: legacy\ndescription: x\n",
		"skills/legacy/SKILL.md": "---\nname: legacy\ndescription: x\n---\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "legacy"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil {
		t.Fatalf("expected legacy-header rejection, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "legacy skill header") || !strings.Contains(msg, "agentskills.io") {
		t.Errorf("expected migration-hint error, got %v", err)
	}
}

func TestResolveSkills_LegacyYMLHeaderRejected(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/legacy.yml":      "kind: skill\nname: legacy\n",
		"skills/legacy/SKILL.md": "---\nname: legacy\ndescription: x\n---\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "legacy"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "legacy skill header") {
		t.Fatalf("expected legacy-header rejection, got %v", err)
	}
}

func TestResolveSkills_FrontmatterMissing(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/raw/SKILL.md": "# Raw\n\nNo frontmatter here.\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "raw"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "must start with '---'") {
		t.Fatalf("expected missing-frontmatter error, got %v", err)
	}
}

func TestResolveSkills_FrontmatterUnclosed(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/open/SKILL.md": "---\nname: open\ndescription: still open\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "open"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "closing '---'") {
		t.Fatalf("expected unclosed-frontmatter error, got %v", err)
	}
}

func TestResolveSkills_FrontmatterMalformedYAML(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/bad/SKILL.md": "---\nname: bad\n  bad indent: nope\n---\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "bad"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "malformed SKILL.md frontmatter") {
		t.Fatalf("expected malformed-frontmatter error, got %v", err)
	}
}

func TestResolveSkills_NameMissingInFrontmatter(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/nameless/SKILL.md": "---\ndescription: no name\n---\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "nameless"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "missing required field 'name'") {
		t.Fatalf("expected missing-name error, got %v", err)
	}
}

func TestResolveSkills_FrontmatterEmpty(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/blank/SKILL.md": "",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "blank"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "SKILL.md is empty") {
		t.Fatalf("expected empty-file error, got %v", err)
	}
}

func TestResolveSkills_StrictEnvSurfacesMissing(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/support/SKILL.md": "---\nname: support\ndescription: ${MISSING_DESC}\n---\n# Support\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "support"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil {
		t.Fatal("expected UnresolvedVarsError, got nil")
	}
	uve, ok := err.(*UnresolvedVarsError)
	if !ok {
		t.Fatalf("err = %T (%v), want *UnresolvedVarsError", err, err)
	}
	var vars []string
	for _, vs := range uve.Files {
		vars = append(vars, vs...)
	}
	if len(vars) != 1 || vars[0] != "MISSING_DESC" {
		t.Errorf("missing = %v, want [MISSING_DESC]", vars)
	}
}

func TestResolveSkills_NameMismatch(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/foo/SKILL.md": "---\nname: bar\ndescription: x\n---\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "foo"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil || !strings.Contains(err.Error(), "manifest expected") {
		t.Fatalf("expected name-mismatch error, got %v", err)
	}
}

func TestResolveSkills_ImportRejectedByPackageHelper(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"skills/support/SKILL.md": "---\nname: support\ndescription: x\n---\n",
	})
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
		"skills": {{Name: "support", From: "support", Source: "remote"}},
	}}
	_, err := ResolveSkills(root, m)
	if err == nil {
		t.Fatal("expected import-not-supported error")
	}
	if !strings.Contains(err.Error(), "resolve.New") {
		t.Errorf("error should direct callers to resolve.New, got %v", err)
	}
}

func newFileSource(t *testing.T, dir string) sources.Source {
	t.Helper()
	src, err := sources.Parse("file://" + dir)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	return src
}

func TestResolverSkills_ImportedSkillWithBundle(t *testing.T) {
	srcRoot := t.TempDir()
	mustWrite(t, srcRoot, map[string]string{
		"skills/gong/SKILL.md":            "---\nname: gong\ndescription: Gong API\n---\n# Gong\n",
		"skills/gong/references/setup.md": "setup notes\n",
		"skills/gong/assets/logo.png":     "binary",
		"skills/gong/tools/probe.py":      "print('hi')\n",
	})
	localRoot := writeRepo(t, map[string]string{})
	m := &manifest.Manifest{
		Sources: map[string]sources.Source{"bizops": newFileSource(t, srcRoot)},
		Resources: map[string][]manifest.ResourceEntry{
			"skills": {{Name: "gong", From: "gong", Source: "bizops"}},
		},
	}
	got, err := New(localRoot, m, Options{}).Skills(context.Background())
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources; want 1", len(got))
	}
	s := got[0]
	if s.Name != "gong" || s.Description != "Gong API" {
		t.Errorf("identity = %s / %q", s.Name, s.Description)
	}
	wantBundleDir := filepath.Join(srcRoot, "skills", "gong")
	if s.BundleDir != wantBundleDir {
		t.Errorf("BundleDir = %q; want %q", s.BundleDir, wantBundleDir)
	}
	for _, sub := range []string{"references/setup.md", "assets/logo.png", "tools/probe.py"} {
		if _, err := os.Stat(filepath.Join(s.BundleDir, sub)); err != nil {
			t.Errorf("missing %s in BundleDir: %v", sub, err)
		}
	}
	if s.Provenance == nil {
		t.Fatal("Provenance is nil; imported skill must carry source metadata")
	}
	if s.Provenance.Name != "bizops" || s.Provenance.OriginalName != "gong" {
		t.Errorf("Provenance = %+v", s.Provenance)
	}
	if !strings.HasPrefix(s.Provenance.Raw, "file://") {
		t.Errorf("Provenance.Raw = %q; want file:// prefix", s.Provenance.Raw)
	}
}

func TestResolverSkills_AliasedImport(t *testing.T) {
	srcRoot := t.TempDir()
	mustWrite(t, srcRoot, map[string]string{
		"skills/gong/SKILL.md": "---\nname: gong\ndescription: Gong API\n---\n",
	})
	localRoot := writeRepo(t, map[string]string{})
	m := &manifest.Manifest{
		Sources: map[string]sources.Source{"bizops": newFileSource(t, srcRoot)},
		Resources: map[string][]manifest.ResourceEntry{
			"skills": {{Name: "salesforce_gong", From: "gong", Source: "bizops"}},
		},
	}
	got, err := New(localRoot, m, Options{}).Skills(context.Background())
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(got) != 1 || got[0].Name != "salesforce_gong" {
		t.Fatalf("alias not applied: %+v", got)
	}
	if got[0].Description != "Gong API" {
		t.Errorf("description not carried over alias: %q", got[0].Description)
	}
	if got[0].Provenance == nil || got[0].Provenance.OriginalName != "gong" {
		t.Errorf("Provenance.OriginalName = %+v; want pre-alias name", got[0].Provenance)
	}
}

func TestResolverSkills_LocalVsImportCollision(t *testing.T) {
	srcRoot := t.TempDir()
	mustWrite(t, srcRoot, map[string]string{
		"skills/gong/SKILL.md": "---\nname: gong\ndescription: imported\n---\n",
	})
	localRoot := writeRepo(t, map[string]string{
		"skills/gong/SKILL.md": "---\nname: gong\ndescription: local\n---\n",
	})
	m := &manifest.Manifest{
		Sources: map[string]sources.Source{"bizops": newFileSource(t, srcRoot)},
		Resources: map[string][]manifest.ResourceEntry{
			"skills": {
				{Name: "gong", From: "gong"},
				{Name: "gong", From: "gong", Source: "bizops"},
			},
		},
	}
	_, err := New(localRoot, m, Options{}).Skills(context.Background())
	if err == nil {
		t.Fatal("expected collision error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "claimed by both") || !strings.Contains(msg, "gong") {
		t.Errorf("expected collision-naming error, got %v", err)
	}
}

func TestResolverSkills_ImportMissingInSource(t *testing.T) {
	srcRoot := t.TempDir()
	mustWrite(t, srcRoot, map[string]string{
		"skills/gong/SKILL.md": "---\nname: gong\ndescription: x\n---\n",
	})
	localRoot := writeRepo(t, map[string]string{})
	m := &manifest.Manifest{
		Sources: map[string]sources.Source{"bizops": newFileSource(t, srcRoot)},
		Resources: map[string][]manifest.ResourceEntry{
			"skills": {{Name: "atlassian", From: "atlassian", Source: "bizops"}},
		},
	}
	_, err := New(localRoot, m, Options{}).Skills(context.Background())
	if err == nil {
		t.Fatal("expected missing-in-source error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "atlassian") || !strings.Contains(msg, "bizops") {
		t.Errorf("error should name the missing skill and its source, got %v", err)
	}
}

func mustWrite(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, contents := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}
