// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillsecret"
)

func refusalBody(message string) string {
	b, _ := json.Marshal(map[string]string{"message": message})
	return string(b)
}

func skillDownloadFailureServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	mux := skillsMux(t, wire.SkillResponse{ID: "skill-1", Name: "web", Description: new("Search the web")})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills/skill-1/bundle", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return newV2Platform(t, mux)
}

func skillsMux(t *testing.T, skills ...wire.SkillResponse) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("origin") != "custom" {
			t.Errorf("skills listed with %q, want only custom ones", r.URL.RawQuery)
		}
		writeV2Page(w, skills)
	})
	return mux
}

func seedLocalSkill(t *testing.T, out, content string) string {
	t.Helper()
	path := filepath.Join(out, "skills", "web", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func pullSkillsAt(t *testing.T, srv *httptest.Server, out string) (*Result, []fileEntry, []string) {
	t.Helper()
	c := reconcile.NewClient(target.Resolved{URL: srv.URL})
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullSkills(t.Context(), c, Options{OutputDir: out}, res)
	if err != nil {
		t.Fatalf("pullSkills: %v", err)
	}
	return res, entries, names
}

const (
	refusalWithLocal    = `skill "web": platform refused to export SKILL.md (frontmatter it cannot redact safely, e.g. YAML anchors); left the local copy unchanged`
	refusalWithoutLocal = `skill "web": platform refused to export SKILL.md (frontmatter it cannot redact safely, e.g. YAML anchors); no local copy exists, so plan/apply will fail until you add skills/<name>/ or remove it from the manifest (removing it lets --prune delete the skill)`
)

func TestPullSkills_RefusalWithLocalCopyPreservesItAndKeepsTheManifestEntry(t *testing.T) {
	out := t.TempDir()
	seedLocalSkill(t, out, "local\n")
	srv := skillDownloadFailureServer(t, http.StatusUnprocessableEntity, refusalBody(skillsecret.RedactionRefusedMessage))

	res, entries, names := pullSkillsAt(t, srv, out)
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) {
		t.Fatalf("entries=%v names=%v", entries, names)
	}
	if !slices.Contains(res.Warnings, refusalWithLocal) || !slices.Equal(res.preservedDirs, []string{filepath.Join("skills", "web")}) {
		t.Fatalf("warnings=%v preserved=%v", res.Warnings, res.preservedDirs)
	}
}

func TestPullSkills_RefusalWithoutLocalCopyWarnsAccurately(t *testing.T) {
	srv := skillDownloadFailureServer(t, http.StatusUnprocessableEntity, refusalBody(skillsecret.RedactionRefusedMessage))

	res, entries, names := pullSkillsAt(t, srv, t.TempDir())
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) {
		t.Fatalf("the skill must stay in the manifest so --prune cannot delete it: entries=%v names=%v", entries, names)
	}
	if !slices.Contains(res.Warnings, refusalWithoutLocal) || len(res.preservedDirs) != 0 {
		t.Fatalf("warnings=%v preserved=%v", res.Warnings, res.preservedDirs)
	}
}

func TestPullSkills_OtherUnprocessableErrorsQuoteTheServerMessage(t *testing.T) {
	out := t.TempDir()
	seedLocalSkill(t, out, "local\n")
	srv := skillDownloadFailureServer(t, http.StatusUnprocessableEntity, refusalBody("skill bundle is not a readable ZIP"))

	res, entries, names := pullSkillsAt(t, srv, out)
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) || len(res.preservedDirs) != 1 {
		t.Fatalf("entries=%v names=%v preserved=%v", entries, names, res.preservedDirs)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "skill bundle is not a readable ZIP") || strings.Contains(joined, "refused to export SKILL.md") {
		t.Fatalf("warning must quote the server's message and not claim a redaction refusal: %v", res.Warnings)
	}
}

func TestPullSkills_DownloadFailureWithLocalCopyPreservesIt(t *testing.T) {
	out := t.TempDir()
	seedLocalSkill(t, out, "local\n")
	srv := skillDownloadFailureServer(t, http.StatusInternalServerError, `{"message":"boom"}`)

	res, entries, names := pullSkillsAt(t, srv, out)
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) || len(res.preservedDirs) != 1 {
		t.Fatalf("no stub may overwrite a local copy: entries=%v names=%v preserved=%v", entries, names, res.preservedDirs)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "left the local copy unchanged") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestPullSkills_NoBundleAndNoLocalCopyWritesTheStub(t *testing.T) {
	srv := skillDownloadFailureServer(t, http.StatusConflict, refusalBody(entplatform.SkillNoBundleMessage))
	res, entries, names := pullSkillsAt(t, srv, t.TempDir())
	if len(entries) != 1 || len(names) != 1 || len(res.preservedDirs) != 0 {
		t.Fatalf("entries=%v names=%v preserved=%v", entries, names, res.preservedDirs)
	}
}

func TestPullSkills_UnpackFailureWithLocalCopyPreservesIt(t *testing.T) {
	out := t.TempDir()
	seedLocalSkill(t, out, "local\n")
	mux := skillsMux(t, wire.SkillResponse{ID: "skill-1", Name: "web"})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills/skill-1/bundle", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not a zip"))
	})
	srv := newV2Platform(t, mux)

	res, entries, names := pullSkillsAt(t, srv, out)
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) || len(res.preservedDirs) != 1 {
		t.Fatalf("entries=%v names=%v preserved=%v", entries, names, res.preservedDirs)
	}
}

func TestRun_ForceKeepsARefusedSkillAndItsManifestEntry(t *testing.T) {
	out := t.TempDir()
	local := seedLocalSkill(t, out, "local copy with tools\n")
	if err := os.WriteFile(filepath.Join(out, "stale.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := skillDownloadFailureServer(t, http.StatusUnprocessableEntity, refusalBody(skillsecret.RedactionRefusedMessage))

	res, err := Run(t.Context(), target.Target{URL: srv.URL}, Options{OutputDir: out, OnlyKind: "skill", Force: true, ManifestName: "pulled.yaml"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(local)
	if err != nil || string(got) != "local copy with tools\n" {
		t.Fatalf("the local copy must survive --force byte-identical: %q err=%v", got, err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "stale.txt")); statErr == nil {
		t.Error("--force should still wipe unrelated files")
	}
	manifest, err := os.ReadFile(filepath.Join(out, "manifests", "pulled.yaml"))
	if err != nil || !strings.Contains(string(manifest), "web") {
		t.Fatalf("manifest must still list the skill: %q err=%v", manifest, err)
	}
	if !slices.Contains(res.Warnings, refusalWithLocal) {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestRun_RefusedSkillWithoutLocalCopyStaysInTheManifest(t *testing.T) {
	out := t.TempDir()
	srv := skillDownloadFailureServer(t, http.StatusUnprocessableEntity, refusalBody(skillsecret.RedactionRefusedMessage))
	res, err := Run(t.Context(), target.Target{URL: srv.URL}, Options{OutputDir: out, OnlyKind: "skill", ManifestName: "pulled.yaml"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(out, "manifests", "pulled.yaml"))
	if err != nil || !strings.Contains(string(manifest), "web") {
		t.Fatalf("manifest must still list the skill: %q err=%v", manifest, err)
	}
	if !slices.Contains(res.Warnings, refusalWithoutLocal) {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestPullSkills_ServerErrorWithoutLocalCopyWritesNothingButKeepsTheManifestEntry(t *testing.T) {
	srv := skillDownloadFailureServer(t, http.StatusInternalServerError, `{"message":"boom"}`)
	res, entries, names := pullSkillsAt(t, srv, t.TempDir())
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) {
		t.Fatalf("a transient failure must not write a stub: entries=%v names=%v", entries, names)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no local copy exists, so plan/apply will fail") || !strings.Contains(res.Warnings[0], "500") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestRun_ServerErrorWithoutLocalCopyKeepsTheSkillInTheManifest(t *testing.T) {
	out := t.TempDir()
	srv := skillDownloadFailureServer(t, http.StatusBadGateway, `{"message":"upstream"}`)
	if _, err := Run(t.Context(), target.Target{URL: srv.URL}, Options{OutputDir: out, OnlyKind: "skill", ManifestName: "pulled.yaml"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "skills", "web")); err == nil {
		t.Error("no skill files may be written")
	}
	manifest, err := os.ReadFile(filepath.Join(out, "manifests", "pulled.yaml"))
	if err != nil || !strings.Contains(string(manifest), "web") {
		t.Fatalf("manifest must list the skill: %q err=%v", manifest, err)
	}
}

func TestPullSkills_UnpackFailureWithoutLocalCopyWritesNothing(t *testing.T) {
	mux := skillsMux(t, wire.SkillResponse{ID: "skill-1", Name: "web"})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills/skill-1/bundle", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not a zip")) })
	srv := newV2Platform(t, mux)

	res, entries, names := pullSkillsAt(t, srv, t.TempDir())
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) || len(res.Warnings) != 1 {
		t.Fatalf("entries=%v names=%v warnings=%v", entries, names, res.Warnings)
	}
}

func TestPullSkills_SkillGoneIs404ButNotTheNoBundleCase(t *testing.T) {
	srv := skillDownloadFailureServer(t, http.StatusNotFound, `{"message":"skill not found: skill-1"}`)
	res, entries, names := pullSkillsAt(t, srv, t.TempDir())
	if len(entries) != 0 || !slices.Equal(names, []string{"web"}) {
		t.Fatalf("a vanished skill must not get a stub: entries=%v names=%v", entries, names)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no local copy exists, so plan/apply will fail") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestPullSkills_CaseVariantOfKeptSkillIsLossy(t *testing.T) {
	zipBytes := buildZIP(t, map[string]string{"SKILL.md": "---\nname: foo\n---\n"})
	mux := skillsMux(t, wire.SkillResponse{ID: "kept", Name: "Foo"}, wire.SkillResponse{ID: "pulled", Name: "foo"})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills/kept/bundle", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusInternalServerError, "boom")
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/skills/pulled/bundle", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(zipBytes)
	})
	srv := newV2Platform(t, mux)

	out := t.TempDir()
	keptSkillMD := filepath.Join(out, "skills", "Foo", skillMDFilename)
	if err := os.MkdirAll(filepath.Dir(keptSkillMD), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keptSkillMD, []byte("---\nname: Foo\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := reconcile.NewClient(target.Resolved{URL: srv.URL})
	_, res, err := fetchAndTranslate(t.Context(), c, Options{OutputDir: out, OnlyKind: "skill"})
	if err == nil {
		t.Fatal("fetchAndTranslate succeeded; want the pull to fail on the case clash")
	}
	assertLossy(t, res, "the local copy kept unchanged")
}

func TestPullSkills_V2NoBundleAndNoLocalCopyWritesTheStub(t *testing.T) {
	srv := skillDownloadFailureServer(t, http.StatusConflict, refusalBody(entplatform.SkillNoBundleMessage+"; upload one first"))
	res, entries, _ := pullSkillsAt(t, srv, t.TempDir())
	if len(entries) != 1 || !strings.Contains(string(entries[0].content), "Search the web") {
		t.Fatalf("entries = %v, want the synthesized SKILL.md", entries)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no uploaded bundle") {
		t.Errorf("warnings = %v, want the no-bundle warning", res.Warnings)
	}
}

func TestPullSkills_V2RefusalWithLocalCopyPreservesIt(t *testing.T) {
	out := t.TempDir()
	seedLocalSkill(t, out, "local\n")
	srv := skillDownloadFailureServer(t, http.StatusUnprocessableEntity, refusalBody(skillsecret.RedactionRefusedMessage))
	res, entries, _ := pullSkillsAt(t, srv, out)
	if len(entries) != 0 || !slices.Contains(res.Warnings, refusalWithLocal) {
		t.Errorf("entries = %v, warnings = %v, want the refusal warning and the local copy kept", entries, res.Warnings)
	}
}
