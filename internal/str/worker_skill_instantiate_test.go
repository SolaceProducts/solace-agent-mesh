// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

func instantiateArtifactParams(extra map[string]any) *remotetool.InvokeParams {
	args := map[string]any{"source_artifact": "report.html.samt", "substitutions": map[string]any{"title": "Q3"}}
	maps.Copy(args, extra)
	return &remotetool.InvokeParams{
		ToolName: toolInstantiateTemplate, AppName: testAgentName, UserID: testUserID, SessionID: testSessionID, Args: args,
	}
}

func TestInstantiateFromArtifact(t *testing.T) {
	pkg := func(t *testing.T) (*Worker, *artifact.ScopedService) {
		t.Helper()
		svc, _ := newTestScopedSvc()
		seedDataInput(t, svc)
		seedTemplateBody(t, svc)
		w := &Worker{logger: newDiscardLogger(), embedResolveCheck: func(_ context.Context, c string, _ *artifact.ScopedService, _, _, _ string) (string, error) {
			return strings.ReplaceAll(c, "«artifact_content:data.json:0»", "[1,2,3]"), nil
		}}
		if _, ierr := w.executePackageTemplate(t.Context(), templatizeParams(toolPackageTemplate, nil), svc); ierr != nil {
			t.Fatalf("package: %+v", ierr)
		}
		return w, svc
	}

	t.Run("renders and saves, embeds preserved and pinned", func(t *testing.T) {
		w, svc := pkg(t)
		res, ierr := w.executeInstantiateTemplate(t.Context(), instantiateArtifactParams(nil), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["filename"] != "out.html" {
			t.Fatalf("res: %+v", res)
		}
		loc := svc.Resolve(testAgentName, testUserID, testSessionID, "out.html")
		content, _, err := svc.Inner.Load(t.Context(), loc, nil)
		if err != nil {
			t.Fatalf("load saved artifact: %v", err)
		}
		got := string(content)
		if !strings.Contains(got, "<h1>Q3</h1>") {
			t.Errorf("substitution not applied: %q", got)
		}
		if !strings.Contains(got, "«artifact_content:data.json:0»") {
			t.Errorf("embed must be preserved unresolved, pinned to the validated version: %q", got)
		}
	})

	t.Run("output_filename override", func(t *testing.T) {
		w, svc := pkg(t)
		res, ierr := w.executeInstantiateTemplate(t.Context(), instantiateArtifactParams(map[string]any{"output_filename": "custom.html"}), svc)
		if ierr != nil {
			t.Fatalf("ierr: %+v", ierr)
		}
		if res["filename"] != "custom.html" {
			t.Errorf("filename = %v", res["filename"])
		}
	})

	t.Run("source_artifact not found", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		w := &Worker{logger: newDiscardLogger()}
		_, ierr := w.executeInstantiateTemplate(t.Context(), instantiateArtifactParams(map[string]any{"source_artifact": "missing.samt"}), svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})

	t.Run("invalid (non-zip) artifact rejected", func(t *testing.T) {
		svc, _ := newTestScopedSvc()
		w := &Worker{logger: newDiscardLogger()}
		store := w.newScopedStore(instantiateArtifactParams(nil), svc)
		if _, err := store.Save(t.Context(), "bad.samt", []byte("not a zip"), skillruntime.TemplateArtifactMIME, "bad"); err != nil {
			t.Fatal(err)
		}
		_, ierr := w.executeInstantiateTemplate(t.Context(), instantiateArtifactParams(map[string]any{"source_artifact": "bad.samt"}), svc)
		if ierr == nil || ierr.Code != remotetool.ErrInvalidRequest {
			t.Fatalf("want invalid request, got %+v", ierr)
		}
	})
}

func packageTemplateOnly(t *testing.T) (*Worker, *artifact.ScopedService) {
	t.Helper()
	svc, _ := newTestScopedSvc()
	seedTemplateBody(t, svc)
	w := &Worker{logger: newDiscardLogger(), embedResolveCheck: func(_ context.Context, c string, _ *artifact.ScopedService, _, _, _ string) (string, error) {
		return strings.ReplaceAll(c, "«artifact_content:upstream.json:0»", "[9,8,7]"), nil
	}}
	if _, ierr := w.executePackageTemplate(t.Context(), templatizeParams(toolPackageTemplate, nil), svc); ierr != nil {
		t.Fatalf("package: %+v", ierr)
	}
	return w, svc
}

func TestInstantiateFromArtifact_PinsBoundDataInput(t *testing.T) {
	w, svc := packageTemplateOnly(t)
	upstream := svc.Resolve(testAgentName, testUserID, testSessionID, "upstream.json")
	if _, err := svc.Inner.Save(t.Context(), upstream, []byte(`[9,8,7]`), "application/json"); err != nil {
		t.Fatal(err)
	}
	params := instantiateArtifactParams(map[string]any{"data_inputs": map[string]any{"rows": "upstream.json"}})
	res, ierr := w.executeInstantiateTemplate(t.Context(), params, svc)
	if ierr != nil {
		t.Fatalf("ierr: %+v", ierr)
	}
	if res["filename"] != "out.html" {
		t.Fatalf("res: %+v", res)
	}
	declared := svc.Resolve(testAgentName, testUserID, testSessionID, "data.json")
	if content, _, err := svc.Inner.Load(t.Context(), declared, nil); err != nil || content != nil {
		t.Fatalf("no artifact may be staged under the declared filename, got %q err=%v", content, err)
	}
	out := svc.Resolve(testAgentName, testUserID, testSessionID, "out.html")
	content, _, err := svc.Inner.Load(t.Context(), out, nil)
	if err != nil {
		t.Fatalf("load out.html: %v", err)
	}
	if !strings.Contains(string(content), "«artifact_content:upstream.json:0»") {
		t.Errorf("saved document must reference the pinned bound source: %q", content)
	}
}

func TestInstantiateFromArtifact_ContractFailureIsTerminal(t *testing.T) {
	w, svc := packageTemplateOnly(t)
	res, ierr := w.executeInstantiateTemplate(t.Context(), instantiateArtifactParams(nil), svc)
	if res != nil {
		t.Fatalf("expected no result on contract failure, got %+v", res)
	}
	if ierr == nil || ierr.Code != remotetool.ErrToolError {
		t.Fatalf("want terminal ErrToolError, got %+v", ierr)
	}
	if !strings.Contains(ierr.Message, "template contract failed") || !strings.Contains(ierr.Message, "data.json") {
		t.Errorf("message should echo the contract failure: %q", ierr.Message)
	}
}

func TestScanResolutionFailures(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		if d := scanResolutionFailures("<h1>ok</h1>", nil); len(d) != 0 {
			t.Fatalf("want clean, got %v", d)
		}
	})
	t.Run("hard error", func(t *testing.T) {
		d := scanResolutionFailures("", errors.New("ctx canceled"))
		if len(d) != 1 || !strings.Contains(d[0], "ctx canceled") {
			t.Fatalf("d = %v", d)
		}
	})
	t.Run("embed error marker", func(t *testing.T) {
		d := scanResolutionFailures("a [Embed Error: artifact not found] b", nil)
		if len(d) != 1 || !strings.Contains(d[0], "Embed Error") {
			t.Fatalf("d = %v", d)
		}
	})
	t.Run("truncation marker", func(t *testing.T) {
		d := scanResolutionFailures("x [Truncated: artifact size limit reached] y", nil)
		if len(d) != 1 || !strings.Contains(d[0], "Truncated") {
			t.Fatalf("d = %v", d)
		}
	})
	t.Run("template error marker", func(t *testing.T) {
		d := scanResolutionFailures("a [Template Error: bad jsonpath] b", nil)
		if len(d) != 1 || !strings.Contains(d[0], "Template Error") {
			t.Fatalf("d = %v", d)
		}
	})
	t.Run("literal bracket text is not a failure", func(t *testing.T) {
		if d := scanResolutionFailures("log line: [Error: connection refused] [Truncated output]", nil); len(d) != 0 {
			t.Fatalf("unexpected false positive: %v", d)
		}
	})
	t.Run("residual guillemet text is not a failure", func(t *testing.T) {
		if d := scanResolutionFailures("dit « bonjour » ici", nil); len(d) != 0 {
			t.Fatalf("unexpected false positive: %v", d)
		}
	})
}

func TestSubstitutionArgs(t *testing.T) {
	if substitutionArgs(map[string]any{}) != nil {
		t.Error("empty should be nil")
	}
	got := substitutionArgs(map[string]any{"substitutions": map[string]any{"a": "x", "n": float64(3)}})
	if got["a"] != "x" || got["n"] != "3" {
		t.Errorf("got = %+v", got)
	}
}

func TestInstantiateTemplateManifest(t *testing.T) {
	m := instantiateTemplateManifest()
	if m.ToolName != toolInstantiateTemplate || !m.IsBuiltin {
		t.Errorf("manifest = %+v", m)
	}
	if !strings.Contains(m.Description, "source_artifact") || !strings.Contains(m.Description, "skill_name") {
		t.Errorf("description should mention both sources: %q", m.Description)
	}
	props, _ := m.Parameters["properties"].(map[string]any)
	for _, key := range []string{"source_artifact", "skill_name", "asset", "substitutions", "data_inputs"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing param %q", key)
		}
	}
}

func TestSkillRegistry_DetectsTemplates(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "quarterly_report")
	mustWrite(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: quarterly_report\ndescription: d\n---\nbody")
	samt, err := skillruntime.BuildTemplateZip("report.html", "<h1>@@title@@</h1>",
		"template:\n  file: report.html\n  output_filename: out.html\n  mime_type: text/html\n  description: Quarterly\n")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteBytes(t, filepath.Join(skillDir, "assets", "report.html.samt"), samt)
	mustWrite(t, filepath.Join(skillDir, "assets", "logo.txt"), "logo")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	sk := reg.GetSkill("quarterly_report")
	if sk == nil || !sk.HasAssets || !sk.HasTemplates {
		t.Fatalf("skill = %+v", sk)
	}
	if len(sk.Templates) != 1 {
		t.Fatalf("templates = %+v", sk.Templates)
	}
	tmpl := sk.Templates[0]
	if tmpl.Name != "report.html" || tmpl.OutputFilename != "out.html" || tmpl.Asset != "assets/report.html.samt" {
		t.Errorf("template info = %+v", tmpl)
	}
}

func TestSkillAssetSummary(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "plain")
	mustWrite(t, filepath.Join(a, "SKILL.md"), "---\nname: plain\ndescription: d\n---\nx")
	mustWrite(t, filepath.Join(a, "assets", "logo.txt"), "x")

	reg := NewSkillRegistry(dir)
	if err := reg.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	w := &Worker{skillRegistry: reg}
	hasAssets, hasTemplates := w.skillAssetSummary()
	if !hasAssets || hasTemplates {
		t.Fatalf("hasAssets=%v hasTemplates=%v (want true,false)", hasAssets, hasTemplates)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustWriteBytes(t, path, []byte(content))
}

func mustWriteBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
