// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
)

func TestConciergeFS_Accessible(t *testing.T) {
	f := ConciergeFS()
	if _, err := fs.Stat(f, conciergeEntryPoint+"/SKILL.md"); err != nil {
		t.Fatalf("%s not embedded: %v", conciergeEntryPoint, err)
	}
}

func TestWriteStaticSkillTree(t *testing.T) {
	src := fstest.MapFS{
		"sam-x/SKILL.md":        {Data: []byte("---\nname: sam-x\n---\n# x\n")},
		"sam-x/references/a.md": {Data: []byte("detail")},
	}
	dir := t.TempDir()
	if err := writeStaticSkillTree(src, dir, "v9"); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(dir, "sam-x", "SKILL.md")
	got, _ := os.ReadFile(skillFile)
	if !bytes.Contains(got, []byte("metadata:\n  version: v9\n")) {
		t.Fatalf("SKILL.md not stamped under metadata:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "sam-x", "references", "a.md")); err != nil {
		t.Fatalf("reference not written: %v", err)
	}
}

func TestInstallSuite_WritesFullBundleStamped(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v9.9.9", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range allSuiteSkills() {
		skillFile := filepath.Join(dir, name, "SKILL.md")
		body, err := os.ReadFile(skillFile)
		if err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
		fm, err := skill.ParseFrontmatter(skillFile)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fm.Version() != "v9.9.9" {
			t.Errorf("%s stamped %q, want the CLI version", name, fm.Version())
		}
		if bytes.Contains(body, []byte("\nversion:")) {
			t.Errorf("%s carries a top-level version key, which the Agent Skills specification forbids", name)
		}
	}
}

var docsWithOnePage = fstest.MapFS{
	"concepts/artifacts.md": {Data: []byte("# Artifacts\n")},
}

func corpusPath(dir string, rel ...string) string {
	return filepath.Join(slices.Concat([]string{dir, samDocsName, "references", "documentation"}, rel)...)
}

func TestInstallSuite_WritesDocsCorpusIntoSamDocs(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, docsWithOnePage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(corpusPath(dir, "concepts", "artifacts.md")); err != nil {
		t.Fatalf("corpus page not written under sam-docs/references/documentation: %v", err)
	}
}

func TestInstallSuite_ForceReplacesStaleCorpusPages(t *testing.T) {
	dir := t.TempDir()
	old := fstest.MapFS{"concepts/old-name.md": {Data: []byte("old")}}
	if err := InstallSuite(dir, "v1", false, old); err != nil {
		t.Fatal(err)
	}
	renamed := fstest.MapFS{"concepts/new-name.md": {Data: []byte("new")}}
	if err := InstallSuite(dir, "v2", true, renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(corpusPath(dir, "concepts", "old-name.md")); !os.IsNotExist(err) {
		t.Errorf("page renamed between CLI versions lingered after force reinstall: %v", err)
	}
	if _, err := os.Stat(corpusPath(dir, "concepts", "new-name.md")); err != nil {
		t.Errorf("renamed page not written: %v", err)
	}
}

func TestInstallSuite_NilDocsKeepsExistingCorpus(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, docsWithOnePage); err != nil {
		t.Fatal(err)
	}
	if err := InstallSuite(dir, "v2", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(corpusPath(dir, "concepts", "artifacts.md")); err != nil {
		t.Fatalf("a CLI without embedded docs must not remove the corpus a previous install wrote: %v", err)
	}
}

func TestInstallSuite_EmptyDocsRefusesToWipeCorpus(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, docsWithOnePage); err != nil {
		t.Fatal(err)
	}
	if err := InstallSuite(dir, "v2", true, fstest.MapFS{}); err == nil {
		t.Fatal("an embedded corpus with no files must fail the install, not silently wipe the installed one")
	}
	if _, err := os.Stat(corpusPath(dir, "concepts", "artifacts.md")); err != nil {
		t.Fatalf("installed corpus page was wiped by an empty embedded corpus: %v", err)
	}
}

func TestInstallSuite_RefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, nil); err != nil {
		t.Fatal(err)
	}
	err := InstallSuite(dir, "v1", false, nil)
	if err == nil {
		t.Fatal("expected conflict error on re-install without --force")
	}
	if !strings.Contains(err.Error(), "sam-concierge") {
		t.Errorf("error should name conflicting skills, got: %v", err)
	}
}

func TestCheckSuite_DetectsMissingAndMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, nil); err != nil {
		t.Fatal(err)
	}
	res, err := CheckSuite(dir, "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("expected in-sync, got: %+v", res)
	}
	res, _ = CheckSuite(dir, "v2", false)
	if res.OK() {
		t.Fatal("expected out-of-sync after version bump")
	}
	if err := os.RemoveAll(filepath.Join(dir, "sam-operate")); err != nil {
		t.Fatal(err)
	}
	res, _ = CheckSuite(dir, "v1", false)
	if !slices.Contains(res.MissingSkills, "sam-operate") {
		t.Fatalf("expected sam-operate missing, got: %+v", res)
	}
}

func TestCheckSuite_CRLFSkillIsNotReportedMissing(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sam-operate", "SKILL.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, bytes.ReplaceAll(body, []byte("\n"), []byte("\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckSuite(dir, "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(res.MissingSkills, "sam-operate") {
		t.Errorf("CRLF SKILL.md reported as missing: %+v", res)
	}
	if !res.OK() {
		t.Errorf("expected in-sync, got: %+v", res)
	}
}

func TestCheckSuite_SurfacesUnreadableCorpusInsteadOfReportingMissing(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, docsWithOnePage); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(corpusPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corpusPath(dir), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckSuite(dir, "v1", true)
	if err == nil {
		t.Fatal("a corpus path that cannot be read as a directory must be an error, not a missing corpus")
	}
	if res.DocsCorpusMissing {
		t.Fatalf("an I/O fault was misreported as a missing corpus: %+v", res)
	}
}

func TestCheckSuite_ReportsMissingCorpusOnlyWhenThisCLICarriesOne(t *testing.T) {
	dir := t.TempDir()
	if err := InstallSuite(dir, "v1", false, docsWithOnePage); err != nil {
		t.Fatal(err)
	}
	res, err := CheckSuite(dir, "v1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("expected in-sync with corpus present, got: %+v", res)
	}
	if err := os.RemoveAll(corpusPath(dir)); err != nil {
		t.Fatal(err)
	}
	res, _ = CheckSuite(dir, "v1", true)
	if !res.DocsCorpusMissing || res.OK() {
		t.Fatalf("expected the missing corpus reported when this CLI carries docs, got: %+v", res)
	}
	res, _ = CheckSuite(dir, "v1", false)
	if res.DocsCorpusMissing || !res.OK() {
		t.Fatalf("a CLI without embedded docs must not report the corpus missing, got: %+v", res)
	}
}

func TestSuiteEmbedComplete(t *testing.T) {
	f := ConciergeFS()
	skills := ConciergeSkills()
	if len(skills) == 0 {
		t.Fatal("no concierge skills embedded from skills/")
	}
	for _, name := range skills {
		if _, err := fs.Stat(f, name+"/SKILL.md"); err != nil {
			t.Fatalf("concierge skill %q has no SKILL.md: %v", name, err)
		}
	}
}

var wantConciergeSkills = []string{
	"sam-concierge",
	"sam-author-agent",
	"sam-connectors",
	"sam-deploy",
	"sam-docs",
	"sam-entrypoints",
	"sam-install-run",
	"sam-operate",
	"sam-projects",
	"sam-tools-and-skills",
	"sam-troubleshoot",
}

func TestConciergeSkillSet(t *testing.T) {
	if got := ConciergeSkills(); !slices.Equal(got, wantConciergeSkills) {
		t.Errorf("concierge skill set drifted:\n got: %v\nwant: %v", got, wantConciergeSkills)
	}
}
