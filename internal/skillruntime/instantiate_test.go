// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeStore struct {
	history map[string][]savedArtifact
	saved   map[string]savedArtifact
}

type savedArtifact struct {
	content []byte
	mime    string
}

func newFakeStore() *fakeStore {
	return &fakeStore{history: map[string][]savedArtifact{}, saved: map[string]savedArtifact{}}
}

func (f *fakeStore) put(name, mime string, content []byte) {
	f.history[name] = append(f.history[name], savedArtifact{content: content, mime: mime})
}

func (f *fakeStore) Load(_ context.Context, filename string, version *int) ([]byte, string, int, error) {
	versions := f.history[filename]
	if len(versions) == 0 {
		return nil, "", 0, nil
	}
	v := len(versions)
	if version != nil {
		v = *version
	}
	if v < 1 || v > len(versions) {
		return nil, "", 0, fmt.Errorf("version %d not found for %q", v, filename)
	}
	entry := versions[v-1]
	return entry.content, entry.mime, v, nil
}

func (f *fakeStore) Save(_ context.Context, filename string, content []byte, mimeType, _ string) (int, error) {
	f.put(filename, mimeType, content)
	f.saved[filename] = savedArtifact{content: content, mime: mimeType}
	return len(f.history[filename]), nil
}

func cleanResolve(_ context.Context, _ string) []string { return nil }

const quarterlyReportSidecar = `
template:
  file: report.html
  output_filename: quarterly_report.html
  mime_type: text/html
  description: Quarterly sales report
substitutions:
  report_title:
    description: H1 title
    default: "Quarterly Report"
  prepared_by:
    description: Author
    required: true
data_inputs:
  sales_rows:
    description: One row per month
    artifact: sales_data.json
    mime_type: application/json
    required: true
    schema:
      type: array
      items:
        type: object
        required: [month, revenue]
        properties:
          month: {type: string}
          revenue: {type: number}
`

const quarterlyReportBody = `<h1>@@report_title@@</h1><p>By @@prepared_by@@</p>` +
	"«««template_liquid: data=\"sales_data.json\"\n{% for r in data %}{{ r.month }}{% endfor %}»»»" +
	"<script>const x=`cell-${i}`;</script>"

func writeTemplateAsset(t *testing.T, skillDir, assetName, body, sidecar string) string {
	t.Helper()
	zipBytes, err := BuildTemplateZip(assetName, body, sidecar)
	if err != nil {
		t.Fatalf("BuildTemplateZip: %v", err)
	}
	samt := assetName + TemplateArtifactExt
	writeBytes(t, filepath.Join(skillDir, "assets", samt), zipBytes)
	return samt
}

func buildTemplateSkill(t *testing.T) SkillContext {
	t.Helper()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "quarterly_report")
	writeTemplateAsset(t, skillDir, "report.html", quarterlyReportBody, quarterlyReportSidecar)
	return SkillContext{Name: "quarterly_report", LocalPath: skillDir}
}

func TestInstantiate_TemplateHappyPath(t *testing.T) {
	sc := buildTemplateSkill(t)
	store := newFakeStore()
	store.put("sales_data.json", "application/json", []byte(`[{"month":"Jan","revenue":100}]`))

	res, findings, err := InstantiateSkillAsset(context.Background(), sc, "report.html.samt", "",
		map[string]string{"prepared_by": "Ed"},
		InstantiateDeps{Store: store, Resolve: cleanResolve})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("unexpected findings: %+v", findings)
	}
	if res.Filename != "quarterly_report.html" || res.Version != 1 {
		t.Fatalf("res = %+v", res)
	}
	saved, ok := store.saved["quarterly_report.html"]
	if !ok {
		t.Fatal("artifact not saved")
	}
	if saved.mime != "text/html" {
		t.Errorf("mime = %q", saved.mime)
	}
	body := string(saved.content)
	if !strings.Contains(body, "<h1>Quarterly Report</h1>") || !strings.Contains(body, "By Ed") {
		t.Errorf("substitution missing: %s", body)
	}
	if !strings.Contains(body, "template_liquid") || !strings.Contains(body, "`cell-${i}`") {
		t.Errorf("embeds/JS not preserved: %s", body)
	}
	if !strings.Contains(body, `data="sales_data.json:1"`) {
		t.Errorf("data reference not version-pinned: %s", body)
	}
}

func TestInstantiate_PlainCopyText(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	writeFile(t, filepath.Join(skillDir, "assets", "logo.txt"), "hello logo")
	sc := SkillContext{Name: "s", LocalPath: skillDir}
	store := newFakeStore()

	res, findings, err := InstantiateSkillAsset(context.Background(), sc, "logo.txt", "", nil,
		InstantiateDeps{Store: store, Resolve: cleanResolve})
	if err != nil || len(findings) > 0 {
		t.Fatalf("err=%v findings=%+v", err, findings)
	}
	if res.Filename != "logo.txt" {
		t.Fatalf("res = %+v", res)
	}
	if got := store.saved["logo.txt"]; string(got.content) != "hello logo" || got.mime != "text/plain" {
		t.Errorf("saved = %q %q", got.content, got.mime)
	}
}

func TestInstantiate_PlainCopyBinary(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}
	writeBytes(t, filepath.Join(skillDir, "assets", "logo.png"), pngMagic)
	sc := SkillContext{Name: "s", LocalPath: skillDir}
	store := newFakeStore()

	res, findings, err := InstantiateSkillAsset(context.Background(), sc, "logo.png", "", nil,
		InstantiateDeps{Store: store, Resolve: cleanResolve})
	if err != nil || len(findings) > 0 {
		t.Fatalf("err=%v findings=%+v", err, findings)
	}
	got := store.saved[res.Filename]
	if string(got.content) != string(pngMagic) {
		t.Errorf("binary content not copied verbatim")
	}
	if got.mime != "image/png" {
		t.Errorf("mime = %q, want image/png", got.mime)
	}
}

func TestInstantiate_PlainCopyRejectsSubstitutions(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	writeFile(t, filepath.Join(skillDir, "assets", "logo.txt"), "x")
	sc := SkillContext{Name: "s", LocalPath: skillDir}
	_, _, err := InstantiateSkillAsset(context.Background(), sc, "logo.txt", "",
		map[string]string{"k": "v"}, InstantiateDeps{Store: newFakeStore(), Resolve: cleanResolve})
	if err == nil || !strings.Contains(err.Error(), "substitutions are only valid for templates") {
		t.Fatalf("expected substitutions-rejected error, got %v", err)
	}
}

func TestInstantiate_BinaryMIMERejected(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	asset := writeTemplateAsset(t, skillDir, "logo.png", "binary-ish",
		"template:\n  file: logo.png\n  mime_type: image/png\n")
	sc := SkillContext{Name: "s", LocalPath: skillDir}
	_, _, err := InstantiateSkillAsset(context.Background(), sc, asset, "", nil,
		InstantiateDeps{Store: newFakeStore(), Resolve: cleanResolve})
	if err == nil || !strings.Contains(err.Error(), "binary mime_type") {
		t.Fatalf("expected binary mime_type rejection, got %v", err)
	}
}

func TestInstantiate_Findings(t *testing.T) {
	validData := []byte(`[{"month":"Jan","revenue":100}]`)

	cases := []struct {
		name      string
		asset     string
		subs      map[string]string
		setup     func(*fakeStore)
		resolve   EmbedResolveChecker
		wantKind  string
		wantInDet string
	}{
		{
			name:      "substitution unknown key",
			asset:     "report.html.samt",
			subs:      map[string]string{"prepared_by": "Ed", "preparedby": "x"},
			setup:     func(s *fakeStore) { s.put("sales_data.json", "application/json", validData) },
			resolve:   cleanResolve,
			wantKind:  errKindSubstitution,
			wantInDet: "did you mean",
		},
		{
			name:      "substitution required missing",
			asset:     "report.html.samt",
			subs:      nil,
			setup:     func(s *fakeStore) { s.put("sales_data.json", "application/json", validData) },
			resolve:   cleanResolve,
			wantKind:  errKindSubstitution,
			wantInDet: "no value and no default",
		},
		{
			name:      "schema violation",
			asset:     "report.html.samt",
			subs:      map[string]string{"prepared_by": "Ed"},
			setup:     func(s *fakeStore) { s.put("sales_data.json", "application/json", []byte(`[{"month":"Jan"}]`)) },
			resolve:   cleanResolve,
			wantKind:  errKindSchema,
			wantInDet: "revenue",
		},
		{
			name:      "missing required data artifact",
			asset:     "report.html.samt",
			subs:      map[string]string{"prepared_by": "Ed"},
			setup:     func(s *fakeStore) {},
			resolve:   cleanResolve,
			wantKind:  errKindNotFound,
			wantInDet: "sales_data.json",
		},
		{
			name:      "resolution error",
			asset:     "report.html.samt",
			subs:      map[string]string{"prepared_by": "Ed"},
			setup:     func(s *fakeStore) { s.put("sales_data.json", "application/json", validData) },
			resolve:   func(_ context.Context, _ string) []string { return []string{"embed «artifact_content:x» failed"} },
			wantKind:  errKindResolution,
			wantInDet: "failed",
		},
		{
			name:      "asset not found",
			asset:     "missing.html.samt",
			subs:      nil,
			setup:     func(s *fakeStore) {},
			resolve:   cleanResolve,
			wantKind:  errKindNotFound,
			wantInDet: "not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := buildTemplateSkill(t)
			store := newFakeStore()
			tc.setup(store)
			res, findings, err := InstantiateSkillAsset(context.Background(), sc, tc.asset, "", tc.subs,
				InstantiateDeps{Store: store, Resolve: tc.resolve})
			if err != nil {
				t.Fatalf("unexpected hard error: %v", err)
			}
			if res != nil {
				t.Fatalf("expected no result, got %+v", res)
			}
			if !hasFinding(findings, tc.wantKind, tc.wantInDet) {
				t.Fatalf("want kind=%q detail~=%q, got %+v", tc.wantKind, tc.wantInDet, findings)
			}
			if len(store.saved) != 0 {
				t.Errorf("fail-closed violated: %d artifacts saved", len(store.saved))
			}
		})
	}
}

func TestInstantiate_TemplateAssetMissing(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	writeFile(t, filepath.Join(skillDir, "assets", "placeholder.txt"), "x")
	sc := SkillContext{Name: "s", LocalPath: skillDir}
	_, findings, err := InstantiateSkillAsset(context.Background(), sc, "nope.samt", "", nil,
		InstantiateDeps{Store: newFakeStore(), Resolve: cleanResolve})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !hasFinding(findings, errKindNotFound, "template asset") {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestReadBundledTemplate(t *testing.T) {
	sc := buildTemplateSkill(t)

	t.Run("returns render filename, body, and sidecar", func(t *testing.T) {
		name, body, sidecar, err := ReadBundledTemplate(sc, "assets/report.html.samt")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if name != "report.html" {
			t.Errorf("render filename = %q, want report.html", name)
		}
		if body != quarterlyReportBody {
			t.Errorf("body mismatch: %q", body)
		}
		if sidecar != quarterlyReportSidecar {
			t.Errorf("sidecar mismatch: %q", sidecar)
		}
	})

	t.Run("bare filename is accepted (assets/ prefix optional)", func(t *testing.T) {
		if _, _, _, err := ReadBundledTemplate(sc, "report.html.samt"); err != nil {
			t.Errorf("err: %v", err)
		}
	})

	t.Run("non-.samt asset rejected", func(t *testing.T) {
		_, _, _, err := ReadBundledTemplate(sc, "assets/report.html")
		if err == nil || !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("want ErrInvalidParam, got %v", err)
		}
	})

	t.Run("missing asset surfaces os.ErrNotExist", func(t *testing.T) {
		_, _, _, err := ReadBundledTemplate(sc, "assets/nope.samt")
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("want os.ErrNotExist, got %v", err)
		}
	})
}

func TestInstantiate_RejectsReferencesEscape(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "s")
	writeFile(t, filepath.Join(skillDir, "references", "secret.md"), "secret")
	writeFile(t, filepath.Join(skillDir, "assets", "ok.txt"), "x")
	sc := SkillContext{Name: "s", LocalPath: skillDir}
	for _, asset := range []string{"assets/../references/secret.md", "../references/secret.md", "assets/../../etc/passwd"} {
		_, _, err := InstantiateSkillAsset(context.Background(), sc, asset, "", nil,
			InstantiateDeps{Store: newFakeStore(), Resolve: cleanResolve})
		if err == nil {
			t.Fatalf("asset %q: expected rejection, got nil error", asset)
		}
	}
}

func hasFinding(findings []InstantiateError, kind, detailSubstr string) bool {
	for _, f := range findings {
		if f.Kind == kind && strings.Contains(f.Detail, detailSubstr) {
			return true
		}
	}
	return false
}
